package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The macOS app ships its CLI inside the bundle, off the PATH a login service sees.
const tailscaleApp = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"

// ErrTailscaleSkipped wraps every reason the board is not put on the tailnet that is not a failure:
// no tailscale CLI, logged out, or port 443 already serving something else.
var ErrTailscaleSkipped = errors.New("not on the tailnet")

// ErrTailscaleDown is the skip worth trying again: at login tailscale may not be up yet.
var ErrTailscaleDown = errors.New("tailscale is not logged in and up")

func skipped(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrTailscaleSkipped}, args...)...)
}

func tailscaleCLI() (string, error) {
	if path, err := lookPath("tailscale"); err == nil {
		return path, nil
	}
	if path, err := lookPath(tailscaleApp); err == nil {
		return path, nil
	}
	return "", skipped("the tailscale CLI is not installed")
}

// tailscaleJSON runs a tailscale command that prints JSON and decodes it into out.
func tailscaleJSON(out any, args ...string) error {
	status, text, err := run(args...)
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("%s failed: %s", strings.Join(args[1:], " "), strings.TrimSpace(text))
	}
	if strings.TrimSpace(text) == "" {
		return nil // an empty Serve config may print nothing
	}
	return json.Unmarshal([]byte(text), out)
}

// tailnetName is this machine's name on the tailnet, or a skip when tailscale is not logged in and up.
func tailnetName(cli string) (string, error) {
	var status struct {
		BackendState string
		Self         struct{ DNSName string }
	}
	if err := tailscaleJSON(&status, cli, "status", "--json"); err != nil {
		// The app or tailscaled is not running, as at login before it starts.
		return "", fmt.Errorf("%w: %w: %v", ErrTailscaleSkipped, ErrTailscaleDown, err)
	}
	if status.BackendState != "Running" {
		return "", fmt.Errorf("%w: %w (%s)", ErrTailscaleSkipped, ErrTailscaleDown, status.BackendState)
	}
	return strings.TrimSuffix(status.Self.DNSName, "."), nil
}

// serveTarget is what Serve proxies "/" on 443 to, "" when nothing.
func serveTarget(cli, name string) (string, error) {
	var config struct {
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
	}
	if err := tailscaleJSON(&config, cli, "serve", "status", "--json"); err != nil {
		return "", err
	}
	return config.Web[name+":443"].Handlers["/"].Proxy, nil
}

func boardTarget(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) }

// TailscalePublish puts the board on HTTPS inside the tailnet, as `tailscale serve --bg <port>` does,
// and returns its address. It leaves a 443 route to anything else alone.
func TailscalePublish(port int) (string, error) {
	cli, err := tailscaleCLI()
	if err != nil {
		return "", err
	}
	name, err := tailnetName(cli)
	if err != nil {
		return "", err
	}
	url, target := "https://"+name, boardTarget(port)
	current, err := serveTarget(cli, name)
	switch {
	case err != nil:
		return "", err
	case current == target:
		return url, nil
	case current != "":
		return "", skipped("%s:443 already serves %s", name, current)
	}
	return url, checked(cli, "serve", "--bg", "--https=443", target)
}

// TailscaleUnpublish takes the board off the tailnet when Serve still points 443 at it,
// and tells whether it did. Without tailscale there is nothing to take off.
func TailscaleUnpublish(port int) (bool, error) {
	cli, err := tailscaleCLI()
	if err != nil {
		return false, nil
	}
	name, err := tailnetName(cli)
	if errors.Is(err, ErrTailscaleSkipped) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if current, err := serveTarget(cli, name); err != nil || current != boardTarget(port) {
		return false, err
	}
	// Only "/" goes: other paths on 443 belong to someone else.
	return true, checked(cli, "serve", "--https=443", "--set-path=/", "off")
}
