// Package cli dispatches jira-git-cache subcommands.
package cli

import (
	"fmt"
	"io"
)

// Version is the tool version, overridden at build time with -ldflags.
var Version = "dev"

const usage = `Usage: jira-git-cache <command> [flags]

Commands:
  version   print the tool version
`

// Run executes the command named by args[0] and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version":
		fmt.Fprintln(stdout, Version)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
