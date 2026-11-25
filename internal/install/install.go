// Package install is everything `hc` sets up on a machine: the hook script, the hooks in settings.json
// and the login service.
package install

import (
	"bytes"
	"context"
	"embed"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/rouxlabs/headcoach/internal/config"
	"github.com/rouxlabs/headcoach/internal/sh"
)

//go:embed assets
var assets embed.FS

const (
	hookName   = "hc-hook.sh"
	statusName = "hc-status.sh"
	label      = "local.headcoach"
	unit       = "headcoach.service"
)

// HookEvents are the Claude Code events the board listens to.
var HookEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification",
	"Stop", "SubagentStart", "SubagentStop", "SessionEnd",
}

// keptEnv are the variables the daemon needs again when the service manager starts it instead of a shell.
var keptEnv = []string{
	"PATH", "LANG", "HC_CONFIG", "HC_STATE_DIR", "HC_DATA_DIR", "HC_PORT", "HC_DB", "XDG_CONFIG_HOME",
	"XDG_STATE_HOME", "XDG_DATA_HOME", "TMUX_TMPDIR",
}

// --- hook script ---------------------------------------------------------------------

// script copies a packaged script to a path that survives upgrades, and returns that path.
func script(name string) (string, error) {
	source, err := assets.ReadFile("assets/" + name)
	if err != nil {
		return "", err
	}
	target := filepath.Join(config.DataDir(), name)
	if current, err := os.ReadFile(target); err != nil || !bytes.Equal(current, source) {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, source, 0o755); err != nil {
			return "", err
		}
	}
	return target, os.Chmod(target, 0o755)
}

// HookScript installs both scripts and returns the path of the one Claude Code calls on each event.
func HookScript() (string, error) {
	if _, err := StatusScript(); err != nil {
		return "", err
	}
	return script(hookName)
}

// StatusScript installs the forwarder a status line script calls, so the board learns the usage limits.
func StatusScript() (string, error) { return script(statusName) }

// hooksBlock builds the `hooks` object that points every event at command.
func hooksBlock(command string) *object {
	hooks := newObject()
	for _, event := range HookEvents {
		hook := newObject()
		hook.set("type", "command")
		hook.set("command", command)
		hook.set("async", true)
		entry := newObject()
		entry.set("hooks", []any{hook})
		hooks.set(event, []any{entry})
	}
	return hooks
}

// HooksJSON is what `hc hooks print` shows: the block to paste into a settings.json by hand.
func HooksJSON(command string) string {
	settings := newObject()
	settings.set("hooks", hooksBlock(command))
	return formatJSON(settings, "  ")
}

func isOurs(hook any) bool {
	h, ok := hook.(*object)
	if !ok {
		return false
	}
	command, _ := h.values["command"].(string)
	return strings.HasSuffix(strings.TrimRight(command, `'" `), hookName)
}

// removeHooks drops every headcoach hook from a settings object. Other hooks are left as they are.
func removeHooks(settings *object) {
	hooks, ok := settings.values["hooks"].(*object)
	if !ok {
		return
	}
	for _, event := range append([]string(nil), hooks.keys...) {
		entries, ok := hooks.values[event].([]any)
		if !ok {
			continue
		}
		kept := []any{}
		for _, entry := range entries {
			e, _ := entry.(*object)
			var list []any
			if e != nil {
				list, _ = e.values["hooks"].([]any)
			}
			if list == nil {
				kept = append(kept, entry) // not a shape we know: leave it alone
				continue
			}
			rest := []any{}
			for _, hook := range list {
				if !isOurs(hook) {
					rest = append(rest, hook)
				}
			}
			if len(rest) == 0 && len(list) > 0 {
				continue // the entry only held our hook
			}
			e.set("hooks", rest)
			kept = append(kept, e)
		}
		if len(kept) > 0 {
			hooks.set(event, kept)
		} else {
			hooks.delete(event)
		}
	}
	if len(hooks.keys) == 0 {
		settings.delete("hooks")
	}
}

// addHooks points every event at command, replacing an older headcoach hook.
func addHooks(settings *object, command string) error {
	removeHooks(settings)
	if _, there := settings.get("hooks"); !there {
		settings.set("hooks", newObject())
	}
	hooks, ok := settings.values["hooks"].(*object)
	if !ok {
		return errors.New("The 'hooks' key of settings.json is not an object")
	}
	ours := hooksBlock(command)
	for _, event := range HookEvents {
		var entries []any
		if current, there := hooks.get(event); there {
			if entries, ok = current.([]any); !ok {
				return fmt.Errorf("The '%s' hooks of settings.json are not a list", event)
			}
		}
		hooks.set(event, append(entries, ours.values[event].([]any)...))
	}
	return nil
}

// DefaultConfigDirs is where Claude Code keeps its settings when no directory is named.
func DefaultConfigDirs() []string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return []string{dir}
	}
	home, _ := os.UserHomeDir()
	return []string{filepath.Join(home, ".claude")}
}

var indentRe = regexp.MustCompile(`(?m)^([ \t]+)\S`)

// EditSettings adds or removes the hooks in one Claude Code config directory.
// It returns the settings file and whether it changed.
func EditSettings(configDir string, remove bool) (string, bool, error) {
	configDir = config.ExpandUser(configDir)
	path := filepath.Join(configDir, "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return path, false, err
	}
	text := string(raw)
	settings := newObject()
	if strings.TrimSpace(text) != "" {
		parsed, err := parseJSON(text)
		if err != nil {
			return path, false, fmt.Errorf("%s is not valid JSON: %v", path, err)
		}
		var ok bool
		if settings, ok = parsed.(*object); !ok {
			return path, false, fmt.Errorf("%s does not hold a JSON object", path)
		}
	}
	if remove {
		removeHooks(settings)
	} else {
		command, err := HookScript()
		if err != nil {
			return path, false, err
		}
		if err := addHooks(settings, command); err != nil {
			return path, false, err
		}
	}
	indent := "  "
	if found := indentRe.FindStringSubmatch(text); found != nil {
		indent = found[1] // the file keeps its indentation
	}
	out := formatJSON(settings, indent) + "\n"
	if out == text {
		return path, false, nil
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return path, false, err
	}
	if text != "" {
		if err := os.WriteFile(filepath.Join(configDir, "settings.json.hc-backup"), raw, 0o644); err != nil {
			return path, false, err
		}
	}
	// Write through a symlink: settings.json is often a link into a dotfiles repository.
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	return path, true, os.WriteFile(target, []byte(out), 0o644)
}

// --- config file ---------------------------------------------------------------------

// InitConfig writes the commented example config unless a config file exists.
// It returns the path and whether the file was created.
func InitConfig() (string, bool, error) {
	path := config.Path()
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	}
	example, err := assets.ReadFile("assets/config.example.toml")
	if err != nil {
		return path, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, false, err
	}
	return path, true, os.WriteFile(path, example, 0o644)
}

// --- login service -------------------------------------------------------------------

// goos and lookPath are variables so that tests can stand in for another machine.
var (
	goos     = runtime.GOOS
	lookPath = exec.LookPath
)

// run runs a service manager command and returns its exit status and its output. Tests replace it.
var run = func(args ...string) (int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	var exit *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return 0, "", fmt.Errorf("%s is not installed", args[0])
	case ctx.Err() != nil:
		return 0, "", fmt.Errorf("%s timed out", strings.Join(args[:min(2, len(args))], " "))
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out), nil
	case err != nil:
		return 0, "", err
	}
	return 0, string(out), nil
}

// checked runs a command that must succeed.
func checked(args ...string) error {
	status, out, err := run(args...)
	if err != nil {
		return err
	}
	if status != 0 {
		if out = strings.TrimSpace(out); out != "" {
			return errors.New(out)
		}
		return fmt.Errorf("%s failed", strings.Join(args[:min(2, len(args))], " "))
	}
	return nil
}

// Manager names the service manager of this machine: launchd or systemd.
func Manager() (string, error) {
	if goos == "darwin" {
		return "launchd", nil
	}
	if goos == "linux" {
		if _, err := lookPath("systemctl"); err == nil {
			return "systemd", nil
		}
	}
	return "", errors.New("No service manager is supported here. Run the daemon yourself with: hc serve")
}

// ServicePath is the file that describes the service to the service manager.
func ServicePath() (string, error) {
	manager, err := Manager()
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	if manager == "launchd" {
		return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", unit), nil
}

// executable is the path the service starts. A stable path on PATH wins over the resolved one:
// a package manager keeps the first across upgrades and moves the second.
func executable() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	if onPath, err := lookPath(filepath.Base(self)); err == nil {
		if resolved, err := filepath.EvalSymlinks(onPath); err == nil && resolved == self {
			if abs, err := filepath.Abs(onPath); err == nil {
				return abs, nil
			}
		}
	}
	return self, nil
}

// launcher writes a script named after the project that starts the daemon, and returns its path.
//
// macOS lists a login item under the name of its program: this makes it read "headcoach", not "hc".
func launcher() (string, error) {
	self, err := executable()
	if err != nil {
		return "", err
	}
	target := filepath.Join(config.DataDir(), "headcoach")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	text := "#!/bin/sh\nexec " + sh.Quote(self) + " serve \"$@\"\n"
	if err := os.WriteFile(target, []byte(text), 0o755); err != nil {
		return "", err
	}
	return target, os.Chmod(target, 0o755)
}

func serviceEnv() [][2]string {
	var env [][2]string
	for _, name := range keptEnv {
		if value := os.Getenv(name); value != "" {
			env = append(env, [2]string{name, value})
		}
	}
	return env
}

// LogPath is where launchd sends the output of the daemon.
func LogPath() string { return filepath.Join(config.StateDir(), "daemon.log") }

func xmlText(s string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

func launchdPlist() (string, error) {
	program, err := launcher()
	if err != nil {
		return "", err
	}
	var env strings.Builder
	for _, kv := range serviceEnv() {
		fmt.Fprintf(&env, "\t\t<key>%s</key>\n\t\t<string>%s</string>\n", xmlText(kv[0]), xmlText(kv[1]))
	}
	log := xmlText(LogPath())
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlText(program) + `</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
` + env.String() + `	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + log + `</string>
	<key>StandardErrorPath</key>
	<string>` + log + `</string>
</dict>
</plist>
`, nil
}

func systemdUnit() (string, error) {
	program, err := launcher()
	if err != nil {
		return "", err
	}
	quote := func(value string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
	}
	var env strings.Builder
	for _, kv := range serviceEnv() {
		env.WriteString("Environment=" + quote(kv[0]+"="+kv[1]) + "\n")
	}
	return "[Unit]\nDescription=headcoach: a task board for Claude Code sessions\n\n" +
		"[Service]\nExecStart=" + quote(program) + "\n" + env.String() +
		"Restart=on-failure\nRestartSec=2\n\n" +
		"[Install]\nWantedBy=default.target\n", nil
}

func domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

// bootout unloads the agent and waits until launchd has let go of it: loading it again too early fails.
func bootout() {
	_, _, _ = run("launchctl", "bootout", domain()+"/"+label)
	for i := 0; i < 40; i++ {
		if status, _, err := run("launchctl", "print", domain()+"/"+label); err != nil || status != 0 {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// ServiceInstalled tells whether the service file exists.
func ServiceInstalled() bool {
	path, err := ServicePath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// ServiceInstall starts the daemon now and at every login. It returns the service file.
func ServiceInstall() (string, error) {
	manager, err := Manager()
	if err != nil {
		return "", err
	}
	path, _ := ServicePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if manager == "launchd" {
		plist, err := launchdPlist()
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(LogPath()), 0o755); err != nil {
			return "", err
		}
		bootout()
		if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
			return "", err
		}
		return path, checked("launchctl", "bootstrap", domain(), path)
	}
	text, err := systemdUnit()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", unit}, {"restart", unit}} {
		if err := checked(append([]string{"systemctl", "--user"}, args...)...); err != nil {
			return "", err
		}
	}
	return path, nil
}

// ServiceUninstall stops the daemon and removes the service file, which it returns.
func ServiceUninstall() (string, error) {
	manager, err := Manager()
	if err != nil {
		return "", err
	}
	path, _ := ServicePath()
	if manager == "launchd" {
		bootout()
	} else {
		_, _, _ = run("systemctl", "--user", "disable", "--now", unit)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if manager == "systemd" {
		_, _, _ = run("systemctl", "--user", "daemon-reload")
	}
	return path, nil
}

// ServiceRestart restarts the daemon through its service manager.
func ServiceRestart() error {
	if !ServiceInstalled() {
		return errors.New("No service is installed. Install it with: hc service install")
	}
	manager, _ := Manager()
	if manager == "systemd" {
		return checked("systemctl", "--user", "restart", unit)
	}
	if status, _, err := run("launchctl", "kickstart", "-k", domain()+"/"+label); err != nil || status != 0 {
		// Not loaded, for example after `launchctl bootout` by hand.
		path, _ := ServicePath()
		return checked("launchctl", "bootstrap", domain(), path)
	}
	return nil
}
