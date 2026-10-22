package cli_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/cli"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gittest"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
)

func TestMain(m *testing.M) {
	cleanup := gittest.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// fixture is a clone of a bare origin, next to a fake Jira holding PLAT-1 and PLAT-2.
type fixture struct {
	jira    *jiramock.Server
	remote  string
	dir     string
	env     map[string]string
	now     time.Time
	tick    time.Duration // added to now after each call of Env.Now
	version string
	prompts []string
	stdin   string
	ctx     context.Context // t.Context() when nil

	requests atomic.Int64 // sent through Env.HTTP
}

type countingTransport struct{ n *atomic.Int64 }

func (c countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	srv := jiramock.New(t)
	srv.Put(
		jiramock.NewIssue("PLAT-1", 10001, jiramock.DefaultNow.Add(-time.Hour)),
		jiramock.NewIssue("PLAT-2", 10002, jiramock.DefaultNow.Add(-2*time.Hour)),
	)
	remote := gittest.Remote(t)
	return &fixture{
		jira:    srv,
		remote:  remote,
		dir:     gittest.Clone(t, remote),
		env:     map[string]string{"JIRA_TOKEN": srv.Token},
		now:     jiramock.DefaultNow,
		version: "v0.1.0",
	}
}

// newSetUpFixture runs setup for project PLAT.
func newSetUpFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.mustRun(t, "setup", "--non-interactive", "--jira-url", f.jira.URL, "--projects", "PLAT")
	return f
}

type result struct {
	code           int
	stdout, stderr string
}

func (f *fixture) run(t *testing.T, args ...string) result {
	t.Helper()
	return f.runIn(t, f.dir, args...)
}

func (f *fixture) runIn(t *testing.T, dir string, args ...string) result {
	t.Helper()
	ctx := f.ctx
	if ctx == nil {
		ctx = t.Context()
	}
	var stdout, stderr strings.Builder
	code := cli.Run(ctx, cli.Env{
		Stdin:  strings.NewReader(f.stdin),
		Stdout: &stdout,
		Stderr: &stderr,
		Dir:    dir,
		Getenv: func(k string) string { return f.env[k] },
		HTTP:   &http.Client{Transport: countingTransport{&f.requests}},
		Now: func() time.Time {
			now := f.now
			f.now = f.now.Add(f.tick)
			return now
		},
		ReadPassword: func(prompt string) (string, error) {
			f.prompts = append(f.prompts, prompt)
			if token, ok := f.env["PASSWORD"]; ok {
				return token, nil
			}
			return "", errors.New("stdin is not a terminal")
		},
		BuildVersion: f.version,
	}, args)
	return result{code, stdout.String(), stderr.String()}
}

func (f *fixture) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	r := f.run(t, args...)
	if r.code != 0 {
		t.Fatalf("%q: exit %d\nstdout:\n%s\nstderr:\n%s", args, r.code, r.stdout, r.stderr)
	}
	return r.stdout
}

func (f *fixture) path(rel string) string { return filepath.Join(f.dir, filepath.FromSlash(rel)) }

func (f *fixture) read(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(f.path(rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *fixture) touch(t *testing.T, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.path(rel)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path(rel), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) branchTip(t *testing.T) string {
	t.Helper()
	return gittest.Run(t, f.remote, "rev-parse", "dev/jira-cache")
}

func wantResult(t *testing.T, got, want result) {
	t.Helper()
	if got != want {
		t.Errorf("exit %d, want %d\nstdout:\n%s\nwant:\n%s\nstderr:\n%s\nwant:\n%s",
			got.code, want.code, got.stdout, want.stdout, got.stderr, want.stderr)
	}
}

func TestNoArgsIsUsageError(t *testing.T) {
	t.Parallel()
	r := newFixture(t).run(t)
	if r.code != 2 || r.stdout != "" || !strings.HasPrefix(r.stderr, "✗ missing command\n\nUsage: jira-git-cache <command>") {
		t.Errorf("%+v", r)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	t.Parallel()
	r := newFixture(t).run(t, "frobnicate")
	if r.code != 2 || !strings.HasPrefix(r.stderr, "✗ unknown command \"frobnicate\"\n\nUsage:") {
		t.Errorf("%+v", r)
	}
}

func TestHelpPrintsUsage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}, {"sync", "-h"}, {"preview", "--help"}} {
		r := f.run(t, args...)
		if r.code != 0 || !strings.HasPrefix(r.stdout, "Usage: jira-git-cache <command>") || r.stderr != "" {
			t.Errorf("%q: %+v", args, r)
		}
	}
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"help", "x"}, "help takes no arguments, got [x]"},
		{[]string{"--help", "x"}, "--help takes no arguments, got [x]"},
		{[]string{"version", "x"}, "version takes no arguments, got [x]"},
		{[]string{"sync", "x"}, "sync takes no arguments, got [x]"},
		{[]string{"sync", "--bogus"}, "flag provided but not defined: -bogus"},
		{[]string{"pull", "--max-age", "2d"}, `invalid value "2d" for flag -max-age: parse error`},
		{[]string{"pull", "--max-age", "-1ns"}, "--max-age must not be negative, got -1ns"},
		{[]string{"pull", "x"}, "pull takes no arguments, got [x]"},
		{[]string{"setup", "x"}, "setup takes no arguments, got [x]"},
		{[]string{"preview"}, "preview takes DIR, got []"},
		{[]string{"preview", "a", "b"}, "preview takes DIR, got [a b]"},
		{[]string{"preview", "a", "--full"}, "preview takes DIR, got [a --full]"},
	} {
		r := f.run(t, tc.args...)
		if r.code != 2 || r.stdout != "" || !strings.HasPrefix(r.stderr, "✗ "+tc.want+"\n\nUsage:") {
			t.Errorf("%q: %+v", tc.args, r)
		}
	}
}

func TestVersionPrintsBuildVersion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for build, want := range map[string]string{
		"v0.1.0":                               "v0.1.0",
		"v0.1.1-0.20261022070000-0123456789ab": "v0.1.1-0.20261022070000-0123456789ab",
		"v0.1.1-0.20261022070000-0123456789ab+dirty": "dev",
		"(devel)": "dev",
		"":        "dev",
	} {
		f.version = build
		wantResult(t, f.run(t, "version"), result{0, want + "\n", ""})
	}
}

func TestCommandsUseEnvHTTP(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, args := range [][]string{
		{"setup", "--non-interactive", "--jira-url", f.jira.URL, "--projects", "PLAT"},
		{"sync"},
		{"preview", "out"},
	} {
		before := f.requests.Load()
		f.mustRun(t, args...)
		if f.requests.Load() == before {
			t.Errorf("%q sent no requests through Env.HTTP", args)
		}
	}
}

func TestCommandsStopWhenContextEnds(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f.ctx = ctx
	for _, args := range [][]string{{"setup", "--non-interactive"}, {"sync"}, {"preview", "out"}, {"pull"}} {
		wantResult(t, f.run(t, args...), result{1, "", "✗ git rev-parse --show-toplevel: context canceled\n"})
	}
}
