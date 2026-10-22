// Command jira-git-cache syncs Jira Data Center issues into a git branch and renders them for grep.
package main

import (
	"os"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
