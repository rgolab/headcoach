package install

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// isolate points every path the installer touches into a temporary directory.
func isolate(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("HC_STATE_DIR", filepath.Join(tmp, "default-state"))
	t.Setenv("HC_DATA_DIR", filepath.Join(tmp, "default-data"))
	t.Setenv("HC_CONFIG", filepath.Join(tmp, "config", "config.toml"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return tmp
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func edit(t *testing.T, dir string, remove bool) bool {
	t.Helper()
	_, changed, err := EditSettings(dir, remove)
	if err != nil {
		t.Fatal(err)
	}
	return changed
}

func decode(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%v in %s", err, text)
	}
	return out
}

const other = `{"hooks": [{"type": "command", "command": "/usr/local/bin/notify.sh"}]}`

func TestHookScriptIsCopiedToAStableExecutablePath(t *testing.T) {
	tmp := isolate(t)
	path, err := HookScript()
	if err != nil || path != filepath.Join(tmp, "default-data", "hc-hook.sh") {
		t.Fatalf("HookScript = %s, %v", path, err)
	}
	source, _ := assets.ReadFile("assets/hc-hook.sh")
	for _, name := range []string{"hc-hook.sh", "hc-status.sh"} {
		info, err := os.Stat(filepath.Join(tmp, "default-data", name))
		if err != nil || info.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable: %v", name, err)
		}
	}
	write(t, path, "stale") // an upgrade ships a newer script
	HookScript()
	if read(t, path) != string(source) {
		t.Error("a stale script was kept")
	}
}

func TestInstallAddsEveryEventAndKeepsOtherHooks(t *testing.T) {
	tmp := isolate(t)
	dir := filepath.Join(tmp, "claude")
	before := "{\n    \"zebra\": 1.50,\n    \"model\": \"x <y> & z\",\n    \"hooks\": {\n        \"Stop\": [\n            " +
		strings.ReplaceAll(other, "\n", "") + "\n        ]\n    }\n}\n"
	write(t, filepath.Join(dir, "settings.json"), before)
	if !edit(t, dir, false) {
		t.Fatal("nothing changed")
	}
	text := read(t, filepath.Join(dir, "settings.json"))
	// The file keeps its indentation, the order of its keys and the way its values are written.
	if !strings.HasPrefix(text, "{\n    \"zebra\": 1.50,\n    \"model\": \"x <y> & z\",\n    \"hooks\": {\n        \"Stop\": [") {
		t.Errorf("the file was reshaped:\n%s", text)
	}
	hooks := decode(t, text)["hooks"].(map[string]any)
	if len(hooks) != len(HookEvents) {
		t.Errorf("%d events have a hook", len(hooks))
	}
	stop := hooks["Stop"].([]any)
	if !reflect.DeepEqual(stop[0], any(decode(t, other))) {
		t.Errorf("the other hook changed: %v", stop[0])
	}
	script, _ := HookScript()
	want := map[string]any{"type": "command", "command": script, "async": true}
	if ours := stop[1].(map[string]any)["hooks"].([]any)[0]; !reflect.DeepEqual(ours, any(want)) {
		t.Errorf("our hook = %v", ours)
	}
	if read(t, filepath.Join(dir, "settings.json.hc-backup")) != before {
		t.Error("the backup is not the file as it was")
	}

	// Uninstalling gives the file back, byte for byte.
	if !edit(t, dir, true) || read(t, filepath.Join(dir, "settings.json")) != strings.Replace(before, strings.ReplaceAll(other, "\n", ""),
		"{\n                \"hooks\": [\n                    {\n                        \"type\": \"command\",\n"+
			"                        \"command\": \"/usr/local/bin/notify.sh\"\n                    }\n                ]\n            }", 1) {
		t.Errorf("after uninstall:\n%s", read(t, filepath.Join(dir, "settings.json")))
	}
}

func TestInstallTwiceChangesNothingAndReplacesAnOldPath(t *testing.T) {
	tmp := isolate(t)
	dir := filepath.Join(tmp, "claude")
	path := filepath.Join(dir, "settings.json")
	if !edit(t, dir, false) { // no settings.json yet: it is created
		t.Fatal("the first install changed nothing")
	}
	if edit(t, dir, false) {
		t.Error("the second install changed the file")
	}
	script, _ := HookScript()
	write(t, path, strings.ReplaceAll(read(t, path), script, "/old/checkout/hooks/hc-hook.sh"))
	if !edit(t, dir, false) || strings.Contains(read(t, path), "/old/checkout") {
		t.Error("an old hook path was kept")
	}
	for event, entries := range decode(t, read(t, path))["hooks"].(map[string]any) {
		if len(entries.([]any)) != 1 {
			t.Errorf("%s has %d entries", event, len(entries.([]any)))
		}
	}
	edit(t, dir, true)
	if got := read(t, path); got != "{}\n" {
		t.Errorf("after uninstall: %q", got)
	}
}

func TestSettingsBehindASymlinkStayASymlink(t *testing.T) {
	tmp := isolate(t)
	real := filepath.Join(tmp, "dotfiles", "settings.json")
	write(t, real, "{}\n")
	dir := filepath.Join(tmp, "claude")
	os.Mkdir(dir, 0o755)
	os.Symlink(real, filepath.Join(dir, "settings.json"))
	edit(t, dir, false)
	if info, _ := os.Lstat(filepath.Join(dir, "settings.json")); info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file")
	}
	if !strings.Contains(read(t, real), "hc-hook.sh") {
		t.Error("the file behind the symlink was not written")
	}
}

func TestInvalidSettingsAreRefusedAndLeftAlone(t *testing.T) {
	tmp := isolate(t)
	dir := filepath.Join(tmp, "claude")
	path := filepath.Join(dir, "settings.json")
	for text, want := range map[string]string{
		"{ not json":             "is not valid JSON",
		"{} trailing":            "is not valid JSON",
		"[1, 2]":                 "does not hold a JSON object",
		`{"hooks": 4}`:           "'hooks' key of settings.json is not an object",
		`{"hooks": {"Stop": 4}}`: "'Stop' hooks of settings.json are not a list",
	} {
		write(t, path, text)
		_, changed, err := EditSettings(dir, false)
		if err == nil || changed || !strings.Contains(err.Error(), want) {
			t.Errorf("%s gave %v", text, err)
		}
		if read(t, path) != text {
			t.Errorf("%s was rewritten", text)
		}
	}
}

func TestHooksJSONAndDefaultConfigDir(t *testing.T) {
	tmp := isolate(t)
	block := decode(t, HooksJSON("/x/hc-hook.sh"))["hooks"].(map[string]any)
	for _, event := range HookEvents {
		entries := block[event].([]any)
		hook := entries[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
		if len(entries) != 1 || hook["command"] != "/x/hc-hook.sh" || hook["type"] != "command" {
			t.Errorf("%s = %v", event, entries)
		}
	}
	if got := DefaultConfigDirs(); !reflect.DeepEqual(got, []string{filepath.Join(tmp, ".claude")}) {
		t.Errorf("default = %v", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/elsewhere")
	if got := DefaultConfigDirs(); !reflect.DeepEqual(got, []string{"/elsewhere"}) {
		t.Errorf("with CLAUDE_CONFIG_DIR = %v", got)
	}
}

func TestConfigInitWritesOnce(t *testing.T) {
	tmp := isolate(t)
	path, created, err := InitConfig()
	if err != nil || !created || path != filepath.Join(tmp, "config", "config.toml") {
		t.Fatalf("InitConfig = %s %v %v", path, created, err)
	}
	if !strings.Contains(read(t, path), "[profiles]") {
		t.Error("the example config is not the one packaged")
	}
	write(t, path, "port = 9999\n")
	if _, created, _ := InitConfig(); created || read(t, path) != "port = 9999\n" {
		t.Error("an existing config was overwritten")
	}
}

func TestServiceFiles(t *testing.T) {
	tmp := isolate(t)
	t.Setenv("PATH", "/opt/my tools/bin:"+os.Getenv("PATH"))
	t.Setenv("HC_CONFIG", "/etc/hc & co.toml")
	t.Setenv("HC_PORT", "7444")
	t.Setenv("HC_DB", "/tmp/other.db")
	program := filepath.Join(tmp, "default-data", "headcoach")

	unitText, err := systemdUnit(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, piece := range []string{
		"ExecStart=\"" + program + "\"\n", "Environment=\"PATH=/opt/my tools/bin:", "Environment=\"HC_CONFIG=/etc/hc & co.toml\"\n",
		"Environment=\"HC_PORT=7444\"\n", "Environment=\"HC_DB=/tmp/other.db\"\n", "Restart=on-failure", "WantedBy=default.target",
	} {
		if !strings.Contains(unitText, piece) {
			t.Errorf("the unit lacks %q:\n%s", piece, unitText)
		}
	}
	// The launcher is a script named after the project that starts this very program.
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	if got := read(t, program); got != "#!/bin/sh\nexec "+self+" serve \"$@\"\n" {
		t.Errorf("launcher = %q", got)
	}
	if info, _ := os.Stat(program); info.Mode()&0o111 == 0 {
		t.Error("the launcher is not executable")
	}

	plist, err := launchdPlist(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, piece := range []string{
		"<key>Label</key>\n\t<string>local.headcoach</string>", "<array>\n\t\t<string>" + program + "</string>\n\t</array>",
		"<key>HC_CONFIG</key>\n\t\t<string>/etc/hc &amp; co.toml</string>", "<key>RunAtLoad</key>\n\t<true/>",
		"<key>KeepAlive</key>\n\t<true/>", "<key>StandardErrorPath</key>\n\t<string>" + filepath.Join(tmp, "default-state", "daemon.log"),
	} {
		if !strings.Contains(plist, piece) {
			t.Errorf("the plist lacks %q:\n%s", piece, plist)
		}
	}
	if _, err := exec.LookPath("plutil"); err == nil { // on macOS, let the system judge the file
		path := filepath.Join(tmp, "check.plist")
		write(t, path, plist)
		if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
			t.Errorf("plutil: %s", out)
		}
	}
}

// fakeManager records service manager commands instead of running them.
func fakeManager(t *testing.T, system string) *[]string {
	t.Helper()
	var calls []string
	oldRun, oldGOOS, oldLook := run, goos, lookPath
	t.Cleanup(func() { run, goos, lookPath = oldRun, oldGOOS, oldLook })
	goos = system
	lookPath = func(name string) (string, error) {
		if name == "systemctl" {
			return "/usr/bin/systemctl", nil
		}
		return "", errors.New("not found")
	}
	run = func(args ...string) (int, string, error) {
		if args[0] == "launchctl" && args[1] == "print" {
			return 1, "", nil // nothing is loaded
		}
		verb := args[1]
		if args[0] == "systemctl" {
			verb = args[2]
		}
		calls = append(calls, args[0]+" "+verb)
		return 0, "", nil
	}
	return &calls
}

func TestServiceInstallRestartUninstall(t *testing.T) {
	for system, want := range map[string][]string{
		"darwin": {"launchctl bootout", "launchctl bootstrap", "launchctl kickstart", "launchctl bootout"},
		"linux": {"systemctl daemon-reload", "systemctl enable", "systemctl restart", "systemctl restart",
			"systemctl disable", "systemctl daemon-reload"},
	} {
		tmp := isolate(t)
		calls := fakeManager(t, system)
		if err := ServiceRestart(); err == nil || !strings.Contains(err.Error(), "No service is installed") {
			t.Errorf("%s: restart without a service gave %v", system, err)
		}
		start := time.Now()
		path, err := ServiceInstall(false)
		if err != nil || !strings.HasPrefix(path, tmp) || !ServiceInstalled() {
			t.Fatalf("%s: install = %s, %v", system, path, err)
		}
		if err := ServiceRestart(); err != nil {
			t.Errorf("%s: restart = %v", system, err)
		}
		if _, err := ServiceUninstall(); err != nil || ServiceInstalled() {
			t.Errorf("%s: uninstall = %v", system, err)
		}
		if !reflect.DeepEqual(*calls, want) {
			t.Errorf("%s ran %v, want %v", system, *calls, want)
		}
		if time.Since(start) > 2*time.Second {
			t.Errorf("%s: waited for a service that is not loaded", system)
		}
	}
}

func TestUnsupportedPlatform(t *testing.T) {
	fakeManager(t, "windows")
	if _, err := Manager(); err == nil || !strings.Contains(err.Error(), "hc serve") {
		t.Errorf("Manager = %v", err)
	}
	if _, err := ServiceInstall(false); err == nil {
		t.Error("a service was installed without a service manager")
	}
}
