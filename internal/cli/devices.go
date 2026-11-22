package cli

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/humblebundledore/headcoach/internal/format"
	"github.com/humblebundledore/headcoach/internal/store"
)

func cmdPair(e *env, p parsed) error {
	var out struct {
		Code      string  `json:"code"`
		Name      string  `json:"name"`
		ExpiresAt float64 `json:"expires_at"`
	}
	if err := e.client.Call("POST", "/api/pair/code", map[string]string{"name": strings.Join(p.args, " ")}, &out); err != nil {
		return err
	}
	e.printf("Open the board on %s and type this code, within %d minutes:\n\n    %s\n\n",
		out.Name, int(time.Until(time.Unix(int64(out.ExpiresAt), 0)).Round(time.Minute).Minutes()), out.Code)
	e.printf("It works once. hc devices lists the paired devices, hc devices rm %s unpairs this one.\n", out.Name)
	return nil
}

func cmdDevices(e *env, p parsed) error {
	action := "ls"
	if len(p.args) > 0 {
		action = p.args[0]
	}
	switch {
	case action == "ls" && len(p.args) <= 1:
		var all []store.Device
		if err := e.client.Call("GET", "/api/devices", nil, &all); err != nil {
			return err
		}
		if p.has("json") {
			return e.json(all)
		}
		if len(all) == 0 {
			e.printf("No paired devices. Pair one with: hc pair <name>\n")
			return nil
		}
		const columns = "%-24s %-16s %s\n"
		e.printf("%s", e.paint(fmt.Sprintf(columns, "NAME", "LAST SEEN", "PAIRED"), "dim"))
		now := time.Now()
		for _, d := range all {
			e.printf(columns, d.Name, format.When(d.LastSeen, now), format.When(d.CreatedAt, now))
		}
		return nil
	case action == "rm" && len(p.args) >= 2:
		name := strings.Join(p.args[1:], " ")
		if err := e.client.Call("DELETE", "/api/devices/"+url.PathEscape(name), nil, nil); err != nil {
			return err
		}
		e.printf("%s is unpaired: it needs a new code to reach the board\n", name)
		return nil
	}
	return usageError{"usage: hc devices [ls | rm <name>]"}
}
