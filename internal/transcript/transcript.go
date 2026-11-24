// Package transcript reads a Claude Code session transcript for display.
//
// The JSONL format is internal to Claude Code and can change between releases, so this parser only looks
// at the few fields it needs and skips anything it does not recognise.
package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"

	"github.com/humblebundledore/headcoach/internal/board"
)

const (
	chunkBytes = 1 << 20
	textLimit  = 4000
)

// scanBytes is how much of its transcripts one log may read. A fixed tail is not enough: a session
// can write megabytes of tool output between two prompts.
var scanBytes int64 = 32 << 20

var noisePrefixes = []string{
	"<command-", "<local-command", "<system-reminder", "<task-notification", "Caveat:", "[Request interrupted by user",
}

var (
	commandNameRe = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgsRe = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

// Turn is one entry of a session log. Who is one of you, agent, tool.
type Turn struct {
	Who  string `json:"who"`
	Text string `json:"text"`
	// From is who sent a turn of yours that the board typed for another agent, as its label names
	// it: "coordinator 1a2b3c4d", "task 1a2b3c4d", "an agent" or "hc". Empty for your own words.
	From string `json:"from,omitempty"`
	HTML string `json:"html,omitempty"` // the text rendered from Markdown, for agent turns
	// Skipped counts the turns left out after this one, see Window.
	Skipped int `json:"skipped,omitempty"`
}

// Gap is the line that stands for the turns left out after this one. Empty when there are none.
func (t Turn) Gap() string {
	switch t.Skipped {
	case 0:
		return ""
	case 1:
		return "… 1 turn …"
	}
	return fmt.Sprintf("… %d turns …", t.Skipped)
}

// escapeHTML renders raw HTML of the source as text: an agent that writes <div> means the characters.
type escapeHTML struct{}

func (escapeHTML) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindRawHTML, func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			segments := n.(*ast.RawHTML).Segments
			for i := 0; i < segments.Len(); i++ {
				segment := segments.At(i)
				w.Write(util.EscapeHTML(segment.Value(source)))
			}
		}
		return ast.WalkSkipChildren, nil
	})
	reg.Register(ast.KindHTMLBlock, func(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			block := n.(*ast.HTMLBlock)
			w.WriteString("<p>")
			for i := 0; i < block.Lines().Len(); i++ {
				segment := block.Lines().At(i)
				w.Write(util.EscapeHTML(segment.Value(source)))
			}
			if block.HasClosure() {
				w.Write(util.EscapeHTML(block.ClosureLine.Value(source)))
			}
			w.WriteString("</p>\n")
		}
		return ast.WalkSkipChildren, nil
	})
}

// Raw HTML is escaped and unsafe link schemes are dropped, so the output is safe to insert in a page.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.Strikethrough),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(escapeHTML{}, 100))),
)

// Render turns the Markdown of an agent turn into HTML.
func Render(text string) string {
	var out bytes.Buffer
	if err := markdown.Convert([]byte(text), &out); err != nil {
		return "<p>" + string(util.EscapeHTML([]byte(text))) + "</p>\n"
	}
	return out.String()
}

func clip(text string) string {
	text = strings.TrimSpace(text)
	if runes := []rune(text); len(runes) > textLimit {
		return strings.TrimRight(string(runes[:textLimit]), " \t\r\n") + "\n…"
	}
	return text
}

// entry is the part of a transcript line this package looks at.
type entry struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	IsSummary   bool   `json:"isCompactSummary"` // what Claude Code wrote for itself when it compacted
	Message     *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Attachment *struct {
		Type        string `json:"type"`
		CommandMode string `json:"commandMode"`
		IsMeta      bool   `json:"isMeta"`
		Origin      *struct {
			Kind string `json:"kind"`
		} `json:"origin"`
		Prompt json.RawMessage `json:"prompt"`
	} `json:"attachment"`
}

// typed tells whether the entry is a message you typed while the agent worked: it reaches the agent
// beside a tool result, as an attachment. The report of a sub-agent is queued the same way.
func (e *entry) typed() bool {
	a := e.Attachment
	return e.Type == "attachment" && a != nil && a.Type == "queued_command" && a.CommandMode == "prompt" &&
		!a.IsMeta && (a.Origin == nil || a.Origin.Kind == "human")
}

type block struct {
	Type  string `json:"type"`
	Text  any    `json:"text"`
	Name  string `json:"name"`
	Input any    `json:"input"`
}

// blocks decodes the content of a message when it is a list. Items that are not objects come out empty.
func blocks(content json.RawMessage) []block {
	var items []json.RawMessage
	if json.Unmarshal(content, &items) != nil {
		return nil
	}
	out := make([]block, len(items))
	for i, item := range items {
		_ = json.Unmarshal(item, &out[i])
	}
	return out
}

func userText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []string
	for _, b := range blocks(content) {
		if b.Type == "text" {
			s, _ := b.Text.(string)
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

func isNoise(text string) bool {
	for _, prefix := range noisePrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// slashCommand is a slash command the way you typed it, out of the tags Claude Code stores it in.
// Empty for any other text.
func slashCommand(text string) string {
	name := commandNameRe.FindStringSubmatch(text)
	if !strings.HasPrefix(text, "<command-") || name == nil {
		return ""
	}
	args := commandArgsRe.FindStringSubmatch(text)
	if args == nil {
		args = []string{"", ""}
	}
	return strings.TrimSpace(strings.TrimSpace(name[1]) + " " + strings.TrimSpace(args[1]))
}

// yours is the turn of something you typed, or nothing when it is empty or not from you.
func yours(content json.RawMessage) []Turn {
	text := strings.TrimSpace(userText(content))
	if command := slashCommand(text); command != "" {
		text = command
	}
	if text == "" || isNoise(text) {
		return nil
	}
	return []Turn{{Who: "you", Text: clip(text)}}
}

// turnsOf returns the turns one line of a transcript holds, oldest first.
func turnsOf(line []byte) []Turn {
	var e entry
	if json.Unmarshal(line, &e) != nil || e.IsSidechain || e.IsMeta || e.IsSummary {
		return nil
	}
	if e.typed() {
		return yours(e.Attachment.Prompt)
	}
	if e.Message == nil {
		return nil
	}
	switch e.Type {
	case "user":
		return yours(e.Message.Content)
	case "assistant":
		var turns []Turn
		for _, b := range blocks(e.Message.Content) {
			text, _ := b.Text.(string)
			switch {
			case b.Type == "text" && strings.TrimSpace(text) != "":
				turns = append(turns, Turn{Who: "agent", Text: clip(text)})
			case b.Type == "tool_use":
				turns = append(turns, Turn{Who: "tool", Text: board.SummarizeTool(b.Name, b.Input)})
			}
		}
		return turns
	}
	return nil
}

// walk reads a transcript from its end and hands over the turns of each line, newest line first,
// until visit returns false or the budget of bytes is spent. It returns what is left of the budget.
func walk(path string, budget int64, visit func([]Turn) bool) int64 {
	fh, err := os.Open(path)
	if err != nil {
		return budget
	}
	defer fh.Close()
	info, err := fh.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return budget
	}
	var head []byte // the end of a line whose start is in the chunk before
	for pos := info.Size(); pos > 0 && budget > 0; {
		n := min(pos, budget, chunkBytes)
		chunk := make([]byte, n, n+int64(len(head)))
		if _, err := fh.ReadAt(chunk, pos-n); err != nil {
			return budget
		}
		pos, budget = pos-n, budget-n
		lines := bytes.Split(append(chunk, head...), []byte("\n"))
		head = nil
		if pos > 0 {
			head, lines = lines[0], lines[1:]
		}
		for i := len(lines) - 1; i >= 0; i-- {
			if turns := turnsOf(lines[i]); len(turns) > 0 && !visit(turns) {
				return budget
			}
		}
	}
	return budget
}

// Window returns the last `limit` turns. Your last prompt comes first when it is older than that,
// with the count of the turns left out after it: what the agent works on stays in sight.
func Window(turns []Turn, limit int) []Turn {
	if limit <= 0 {
		return nil
	}
	cut := len(turns) - limit
	if cut <= 0 {
		return turns
	}
	for _, turn := range turns[cut:] {
		if turn.Who == "you" {
			return turns[cut:]
		}
	}
	for i := cut - 1; i >= 0; i-- {
		if turns[i].Who == "you" {
			prompt := turns[i]
			prompt.Skipped += cut - 1 - i
			return append([]Turn{prompt}, turns[cut:]...)
		}
	}
	return turns[cut:]
}

// Log returns the end of a task's conversation, see Window, from the transcripts of its sessions,
// oldest session first. It reads them from the end, and no further back than it needs. A transcript
// that is missing counts for nothing.
func Log(paths []string, limit int) []Turn {
	if limit <= 0 {
		return nil
	}
	var found [][]Turn // newest first
	count, prompted, budget := 0, false, scanBytes
	for i := len(paths) - 1; i >= 0 && budget > 0 && (count < limit || !prompted); i-- {
		budget = walk(paths[i], budget, func(turns []Turn) bool {
			found = append(found, turns)
			count += len(turns)
			prompted = prompted || turns[0].Who == "you"
			return count < limit || !prompted
		})
	}
	all := make([]Turn, 0, count)
	for i := len(found) - 1; i >= 0; i-- {
		all = append(all, found[i]...)
	}
	return Window(all, limit)
}
