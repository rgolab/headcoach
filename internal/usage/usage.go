// Package usage counts the tokens of a session, from its transcript and the transcripts of its sub-agents.
//
// Files are read incrementally: each call only parses what was appended since the last one.
package usage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Tokens is what a session, a sub-agent or a task has consumed.
type Tokens struct {
	In         int `json:"in"`
	Out        int `json:"out"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
}

// Add returns the sum of two counts.
func (t Tokens) Add(o Tokens) Tokens {
	return Tokens{t.In + o.In, t.Out + o.Out, t.CacheRead + o.CacheRead, t.CacheWrite + o.CacheWrite}
}

// Total is every token, whatever its kind.
func (t Tokens) Total() int { return t.In + t.Out + t.CacheRead + t.CacheWrite }

// file is the reading state of one transcript.
type file struct {
	offset  int64
	total   Tokens
	context int    // tokens the model read for its latest answer: how full the context window is
	model   string // the model of the latest answer
	// One answer is written as several lines that repeat its usage: count each message id once.
	byMessage map[string]Tokens
}

// line is the part of a transcript line this package looks at.
type line struct {
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			In         any `json:"input_tokens"`
			Out        any `json:"output_tokens"`
			CacheRead  any `json:"cache_read_input_tokens"`
			CacheWrite any `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// whole reads a count that should be a whole number. Anything else counts as zero.
func whole(v any) int {
	if f, ok := v.(float64); ok && f == float64(int(f)) {
		return int(f)
	}
	return 0
}

func (f *file) read(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Size() < f.offset { // the file was replaced
		*f = file{}
	}
	if info.Size() == f.offset {
		return
	}
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	if _, err := fh.Seek(f.offset, io.SeekStart); err != nil {
		return
	}
	chunk, err := io.ReadAll(fh)
	if err != nil {
		return
	}
	end := bytes.LastIndexByte(chunk, '\n') + 1 // a line still being written waits for the next call
	f.offset += int64(end)
	if f.byMessage == nil {
		f.byMessage = map[string]Tokens{}
	}
	for _, raw := range bytes.Split(chunk[:end], []byte("\n")) {
		if !bytes.Contains(raw, []byte(`"usage"`)) {
			continue
		}
		var entry line
		if json.Unmarshal(raw, &entry) != nil || entry.Message.Usage == nil {
			continue
		}
		used := entry.Message.Usage
		counts := Tokens{whole(used.In), whole(used.Out), whole(used.CacheRead), whole(used.CacheWrite)}
		key := entry.Message.ID
		if key == "" {
			key = fmt.Sprintf("line-%d", len(f.byMessage))
		}
		before := f.byMessage[key]
		f.byMessage[key] = counts
		f.total = Tokens{
			f.total.In - before.In, f.total.Out - before.Out,
			f.total.CacheRead - before.CacheRead, f.total.CacheWrite - before.CacheWrite,
		}.Add(counts)
		if entry.Message.Model != "<synthetic>" {
			f.context = counts.In + counts.CacheRead + counts.CacheWrite
			if entry.Message.Model != "" {
				f.model = entry.Message.Model
			}
		}
	}
}

var (
	mu    sync.Mutex
	files = map[string]*file{}
)

func state(path string) *file {
	if info, err := os.Stat(path); path == "" || err != nil || !info.Mode().IsRegular() {
		return nil
	}
	f := files[path]
	if f == nil {
		f = &file{}
		files[path] = f
	}
	f.read(path)
	return f
}

// OfFile returns the tokens used in one transcript file. Zeros when the file is missing.
func OfFile(path string) Tokens {
	mu.Lock()
	defer mu.Unlock()
	if f := state(path); f != nil {
		return f.total
	}
	return Tokens{}
}

// ContextOf returns the tokens in the context window at the session's latest answer.
// Zero before the first answer.
func ContextOf(path string) int {
	mu.Lock()
	defer mu.Unlock()
	if f := state(path); f != nil {
		return f.context
	}
	return 0
}

// ModelOf returns the model of the session's latest answer, such as claude-sonnet-4-5.
// Empty before the first answer.
func ModelOf(path string) string {
	mu.Lock()
	defer mu.Unlock()
	if f := state(path); f != nil {
		return f.model
	}
	return ""
}

// AgentFiles finds the sub-agent transcripts of a session, by agent id.
// They live in a directory named after the session.
func AgentFiles(transcriptPath string) map[string]string {
	found := map[string]string{}
	root, ok := strings.CutSuffix(transcriptPath, ".jsonl")
	if !ok || root == "" {
		return found
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // a directory that cannot be read has no transcripts to offer
		}
		name := d.Name()
		if strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".jsonl") {
			found[name[len("agent-"):len(name)-len(".jsonl")]] = path
		}
		return nil
	})
	return found
}

// OfSession returns the tokens of the session itself, and those of each sub-agent by agent id.
func OfSession(transcriptPath string) (own Tokens, byAgent map[string]Tokens) {
	byAgent = map[string]Tokens{}
	for id, path := range AgentFiles(transcriptPath) {
		byAgent[id] = OfFile(path)
	}
	return OfFile(transcriptPath), byAgent
}
