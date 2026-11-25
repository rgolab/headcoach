// hc is the command line of headcoach: a task board for Claude Code sessions.
package main

import (
	"os"

	"github.com/rouxlabs/headcoach/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr)) }
