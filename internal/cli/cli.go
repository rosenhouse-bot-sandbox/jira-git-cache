// Package cli dispatches jira-git-cache subcommands.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/pull"
)

// Version is the tool version, overridden at build time with -ldflags.
var Version = "dev"

const usage = `Usage: jira-git-cache <command> [flags]

Commands:
  setup     configure this repo and create the cache branch
              --non-interactive  use defaults for unset answers; needs JIRA_TOKEN
              --jira-url URL  --projects A,B  --days N  --unresolved-days N
              --runner-labels a,b  --cache-branch NAME  --fields SELECTION
              --create-branch=BOOL  --tool-version vX.Y.Z
  sync      fetch changed issues from Jira and push the cache branch; needs JIRA_TOKEN
              --full  refetch every issue
  preview   sync into DIR instead of pushing: preview [--full] DIR
  pull      render the cache branch into .jira-git-cache/
              --max-age DURATION  exit 1 if the snapshot is older, e.g. 2h
  version   print the tool version
  help      print this help
`

// Env is the process environment a command runs in.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Dir            string
	Getenv         func(string) string
	HTTP           *http.Client
	Now            func() time.Time
	ReadPassword   func(prompt string) (string, error) // errors when stdin is not a terminal
	BuildVersion   string                              // the main module's version from debug.ReadBuildInfo
}

type usageError struct{ error }

// Run executes the command named by args[0] and returns the process exit code.
func Run(ctx context.Context, env Env, args []string) int {
	err := dispatch(ctx, env, args)
	if err == nil {
		return 0
	}
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(env.Stdout, usage)
		return 0
	}
	fmt.Fprintf(env.Stderr, "✗ %v\n", err)
	var fetchErr *pull.FetchError
	switch {
	case errors.As(err, &usageError{}):
		fmt.Fprint(env.Stderr, "\n"+usage)
		return 2
	case errors.As(err, &fetchErr):
		return 2
	}
	return 1
}

func dispatch(ctx context.Context, env Env, args []string) error {
	if len(args) == 0 {
		return usageError{errors.New("missing command")}
	}
	switch name, args := args[0], args[1:]; name {
	case "help", "-h", "--help":
		return flag.ErrHelp
	case "version":
		if _, err := parse(flag.NewFlagSet(name, flag.ContinueOnError), args, 0); err != nil {
			return err
		}
		fmt.Fprintln(env.Stdout, env.toolVersion())
		return nil
	case "setup":
		return env.setup(ctx, args)
	case "sync":
		return env.sync(ctx, args)
	case "preview":
		return env.preview(ctx, args)
	case "pull":
		return env.pull(ctx, args)
	default:
		return usageError{fmt.Errorf("unknown command %q", name)}
	}
}

// parse parses flags and returns exactly n positional arguments.
func parse(fs *flag.FlagSet, args []string, n int) ([]string, error) {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil, flag.ErrHelp
	} else if err != nil {
		return nil, usageError{err}
	}
	if fs.NArg() != n {
		return nil, usageError{fmt.Errorf("%s takes %d arguments, got %v", fs.Name(), n, fs.Args())}
	}
	return fs.Args(), nil
}

func (e Env) toolVersion() string { return toolVersion(Version, e.BuildVersion) }
