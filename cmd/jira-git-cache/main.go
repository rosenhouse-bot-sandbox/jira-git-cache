// Command jira-git-cache syncs Jira Data Center issues into a git branch and renders them for grep.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		return 1
	}
	var buildVersion string
	if info, ok := debug.ReadBuildInfo(); ok {
		buildVersion = info.Main.Version
	}
	return cli.Run(ctx, cli.Env{
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		Dir:          dir,
		Getenv:       os.Getenv,
		HTTP:         &http.Client{Timeout: 60 * time.Second},
		Now:          time.Now,
		ReadPassword: readPassword,
		BuildVersion: buildVersion,
	}, os.Args[1:])
}

// readPassword prompts on stderr and reads a line from the terminal without echo.
func readPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd()) //nolint:gosec // File descriptors fit in an int.
	if !term.IsTerminal(fd) {
		return "", errors.New("stdin is not a terminal; set JIRA_TOKEN")
	}
	fmt.Fprint(os.Stderr, prompt)
	token, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(token), nil
}
