package server

import (
	"regexp"
	"slices"
	"strings"

	"github.com/humblebundledore/headcoach/internal/sh"
	"github.com/humblebundledore/headcoach/internal/store"
)

// efforts are the effort levels claude --effort takes.
var efforts = []string{"low", "medium", "high", "xhigh", "max"}

// modelName is what a model looks like: an alias such as sonnet, or a full name such as
// claude-sonnet-4-5 or opus[1m]. Never an option.
var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/\[\]-]{0,99}$`)

// launchFields reads the model and the effort of a request, the ones present in data, into set.
// An empty value, or default, is the profile's own.
func launchFields(data map[string]any, set store.Fields) error {
	if _, ok := data["model"]; ok {
		model := strings.TrimSpace(str(data, "model"))
		if strings.EqualFold(model, "default") {
			model = ""
		}
		if model != "" && !modelName.MatchString(model) {
			return fail(400, "'%s' is not a model: use an alias such as sonnet, or a full model name", model)
		}
		set["model"] = model
	}
	if _, ok := data["effort"]; ok {
		effort := strings.ToLower(strings.TrimSpace(str(data, "effort")))
		if effort == "default" {
			effort = ""
		}
		if effort != "" && !slices.Contains(efforts, effort) {
			return fail(400, "Unknown effort '%s'. Known: %s", effort, strings.Join(efforts, ", "))
		}
		set["effort"] = effort
	}
	return nil
}

// launchFlags are the options that start a session on a task's model and effort.
func launchFlags(task *store.Task) string {
	out := ""
	if task.Model != "" {
		out += " --model " + sh.Quote(task.Model)
	}
	if task.Effort != "" {
		out += " --effort " + task.Effort
	}
	return out
}
