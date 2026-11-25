// Package herdr is everything that touches Herdr, a terminal multiplexer for coding agents. The board talks
// to a session in a Herdr pane through the herdr command, over the socket of the server that holds the pane.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rouxlabs/headcoach/internal/tmux"
)

// keyNames are the Herdr names of the tmux keys that differ. Herdr reads the others as they are.
var keyNames = map[string]string{"BTab": "shift+tab"}

// Agent is a Claude Code agent in a Herdr pane: the session id it runs, empty when Herdr does not know it,
// and the directory it runs in.
type Agent struct {
	Session string
	Cwd     string
}

type agentCache struct {
	at     time.Time
	agents map[string]Agent
}

// Client runs herdr commands. The zero value is ready to use.
type Client struct {
	mu    sync.Mutex
	cache map[string]agentCache // by socket
}

// run runs one herdr command against the server at socket. Herdr answers in JSON, and errors with a message.
func run(socket string, args ...string) ([]byte, error) {
	if socket == "" {
		return nil, errors.New("no Herdr socket")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "herdr", args...)
	// The daemon may run inside a Herdr pane: only the socket of the session's own server counts.
	cmd.Env = append(env(), "HERDR_SOCKET_PATH="+socket)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return nil, errors.New("herdr is not installed")
	case ctx.Err() != nil:
		return nil, fmt.Errorf("herdr %s timed out", strings.Join(args[:2], " "))
	case err != nil:
		var answer struct {
			Error struct{ Message string } `json:"error"`
		}
		if json.Unmarshal(stderr.Bytes(), &answer) == nil && answer.Error.Message != "" {
			return nil, errors.New(answer.Error.Message)
		}
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return nil, errors.New(detail)
		}
		return nil, fmt.Errorf("herdr %s failed", strings.Join(args[:2], " "))
	}
	return stdout.Bytes(), nil
}

// Agents returns the Claude Code agents of one Herdr server, by pane. A server that cannot be reached has none. Cached for two seconds: the board asks on
// each refresh.
func (c *Client) Agents(socket string) map[string]Agent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.cache[socket]; ok && time.Since(cached.at) < 2*time.Second {
		return cached.agents
	}
	agents := map[string]Agent{}
	var answer struct {
		Result struct {
			Agents []struct {
				Agent        string `json:"agent"`
				PaneID       string `json:"pane_id"`
				Cwd          string `json:"cwd"`
				AgentSession struct {
					Value string `json:"value"`
				} `json:"agent_session"`
			} `json:"agents"`
		} `json:"result"`
	}
	if out, err := run(socket, "agent", "list"); err == nil && json.Unmarshal(out, &answer) == nil {
		for _, agent := range answer.Result.Agents {
			if agent.Agent == "claude" && agent.PaneID != "" {
				agents[agent.PaneID] = Agent{agent.AgentSession.Value, agent.Cwd}
			}
		}
	}
	if c.cache == nil {
		c.cache = map[string]agentCache{}
	}
	c.cache[socket] = agentCache{time.Now(), agents}
	return agents
}

// Prompt types a message into the agent of a pane and submits it. Herdr refuses an agent that shows a dialog.
func (c *Client) Prompt(pane, text, socket string) error {
	_, err := run(socket, "agent", "prompt", pane, text)
	return err
}

// SendKeys presses the keys tmux.SendKeys allows in the agent of a pane, for answering a dialog.
func (c *Client) SendKeys(pane string, pressed []string, socket string) error {
	if err := tmux.CheckKeys(pressed); err != nil {
		return err
	}
	for _, key := range pressed {
		if name, ok := keyNames[key]; ok {
			key = name
		}
		if _, err := run(socket, "agent", "send-keys", pane, key); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// Focus brings a pane to the front of the Herdr clients.
func (c *Client) Focus(pane, socket string) error {
	_, err := run(socket, "agent", "focus", pane)
	return err
}

// Capture returns the text the agent of a pane is showing right now. Empty when it cannot be read.
func (c *Client) Capture(pane, socket string) string {
	out, _ := run(socket, "agent", "read", pane, "--source", "visible")
	return string(out)
}
