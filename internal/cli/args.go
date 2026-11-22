package cli

import (
	"fmt"
	"strings"
)

// flag describes one option of a command.
type flag struct {
	name  string // the long name, without dashes: the key in the parsed result
	short string // one letter, or empty
	value bool   // whether the option takes a value
	multi bool   // whether the option may be given several times
	help  string
}

// parsed is what a command line turned into.
type parsed struct {
	flags map[string][]string
	args  []string
}

func (p parsed) has(name string) bool { return len(p.flags[name]) > 0 }

// get returns the last value given for an option, or fallback.
func (p parsed) get(name, fallback string) string {
	if values := p.flags[name]; len(values) > 0 {
		return values[len(values)-1]
	}
	return fallback
}

// parse reads options and arguments in any order, the way people type them: `hc show abc -n 5`.
// Everything after `--` is an argument.
func parse(argv []string, flags []flag) (parsed, error) {
	out := parsed{flags: map[string][]string{}}
	find := func(word string) *flag {
		for i := range flags {
			if word == "--"+flags[i].name || (flags[i].short != "" && word == "-"+flags[i].short) {
				return &flags[i]
			}
		}
		return nil
	}
	for i := 0; i < len(argv); i++ {
		word := argv[i]
		if word == "--" {
			out.args = append(out.args, argv[i+1:]...)
			break
		}
		if !strings.HasPrefix(word, "-") || word == "-" {
			out.args = append(out.args, word)
			continue
		}
		name, value, inline := strings.Cut(word, "=")
		f := find(name)
		switch {
		case f == nil:
			return out, fmt.Errorf("unknown option %s", name)
		case !f.value && inline:
			return out, fmt.Errorf("option %s takes no value", name)
		case !f.value:
			value = "true"
		case !inline:
			if i+1 >= len(argv) {
				return out, fmt.Errorf("option %s needs a value", name)
			}
			i++
			value = argv[i]
		}
		if !f.multi {
			out.flags[f.name] = nil
		}
		out.flags[f.name] = append(out.flags[f.name], value)
	}
	return out, nil
}
