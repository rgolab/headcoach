package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/humblebundledore/headcoach/internal/install"
	"github.com/humblebundledore/headcoach/internal/update"
	"github.com/humblebundledore/headcoach/internal/version"
)

// confirmUpdate asks before the binary is replaced. Tests replace it.
var confirmUpdate = func(e *env, question string) bool { return confirm(e, question) }

func cmdUpdate(e *env, p parsed) error {
	current := version.Version
	rel, newer, err := update.Check(current)
	if errors.Is(err, update.ErrSource) {
		return fmt.Errorf("hc %s is %w", current, err)
	}
	if err != nil {
		return err
	}
	if !newer {
		e.printf("hc %s is the latest release\n", current)
		return nil
	}
	e.printf("hc %s -> %s  %s\n\n%s\n\n", current, rel.Tag, rel.URL, strings.TrimSpace(rel.Notes))
	if !p.has("yes") && !confirmUpdate(e, "Install "+rel.Tag+"?") {
		return fmt.Errorf("not updated")
	}
	exe, err := update.Install(rel.Tag, current)
	if err != nil {
		return err
	}
	e.printf("hc %s installed at %s, checked against SHA256SUMS\n", rel.Tag, exe)
	if !install.ServiceInstalled() {
		e.printf("Restart your daemon to run it. An open board reloads itself once the daemon runs %s.\n", rel.Tag)
		return nil
	}
	if err := cmdRestart(e, p); err != nil {
		return err
	}
	e.printf("An open board reloads itself.\n")
	return nil
}
