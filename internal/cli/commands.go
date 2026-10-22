package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/config"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gitstore"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/pull"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/render"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/setup"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/syncer"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/version"
)

const (
	origin        = "origin"
	previousRef   = "refs/jira-git-cache/previous"
	previewMarker = ".jira-git-cache-preview"
)

// toolVersion prefers the -ldflags version, then a module version that go run can pin.
func toolVersion(ldflags, build string) string {
	if ldflags != "dev" {
		return ldflags
	}
	if version.Pinnable(build) {
		return build
	}
	return "dev"
}

func (e Env) setup(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	nonInteractive := flags.Bool("non-interactive", false, "")
	toolVersion := flags.String("tool-version", e.toolVersion(), "")
	var answers setup.Answers
	presets := map[string]**string{
		"jira-url": &answers.JiraURL, "projects": &answers.Projects, "days": &answers.Days,
		"unresolved-days": &answers.UnresolvedDays, "runner-labels": &answers.RunnerLabels,
		"cache-branch": &answers.CacheBranch, "fields": &answers.Fields, "create-branch": &answers.CreateBranch,
	}
	flags.Bool("create-branch", true, "")
	for name := range presets {
		if flags.Lookup(name) == nil {
			flags.String(name, "", "")
		}
	}
	if _, err := parse(flags, args, 0); err != nil {
		return err
	}
	flags.Visit(func(f *flag.Flag) {
		if preset, ok := presets[f.Name]; ok {
			value := f.Value.String()
			*preset = &value
		}
	})
	return setup.Run(ctx, setup.Options{
		Dir:            e.Dir,
		Stdin:          e.Stdin,
		Stdout:         e.Stdout,
		Token:          e.Getenv("JIRA_TOKEN"),
		ReadPassword:   e.ReadPassword,
		HTTP:           e.HTTP,
		ToolVersion:    *toolVersion,
		Now:            e.Now(),
		NonInteractive: *nonInteractive,
		Answers:        answers,
	})
}

func (e Env) sync(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	full := flags.Bool("full", false, "")
	if _, err := parse(flags, args, 0); err != nil {
		return err
	}
	token := e.Getenv("JIRA_TOKEN")
	if token == "" {
		return errors.New("JIRA_TOKEN is not set")
	}
	s, err := e.runSyncer(ctx, *full, token)
	if err != nil {
		return err
	}
	commit, err := s.repo.Publish(ctx, origin, s.cfg.CacheBranch, s.lease, s.result.Snapshot.Encode(), s.result.CommitMessage(), s.start)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "pushed %s to %s\n", commit[:7], s.cfg.CacheBranch)
	return nil
}

func (e Env) preview(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("preview", flag.ContinueOnError)
	full := flags.Bool("full", false, "")
	positional, err := parse(flags, args, 1)
	if err != nil {
		return err
	}
	name := positional[0]
	dir := name
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(e.Dir, dir)
	}
	if err := checkPreviewDir(dir, name); err != nil {
		return err
	}
	token := e.Getenv("JIRA_TOKEN")
	if token == "" {
		if token, err = e.ReadPassword("Jira token (hidden; or set JIRA_TOKEN): "); err != nil {
			return fmt.Errorf("read token: %w", err)
		}
	}
	s, err := e.runSyncer(ctx, *full, token)
	if err != nil {
		return err
	}
	tree := render.Tree(s.result.Snapshot)
	tree[previewMarker] = nil
	if err := writeTree(dir, tree); err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "wrote %s (not pushed)\n", name)
	return nil
}

// checkPreviewDir accepts a missing or empty dir, or an earlier preview.
func checkPreviewDir(dir, name string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("preview: %w", err)
	}
	names := map[string]bool{}
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	if len(names) == 0 || names[previewMarker] && !names["config.yml"] {
		return nil
	}
	return fmt.Errorf("refusing to replace %s: it is neither empty nor an earlier preview", name)
}

func writeTree(dir string, tree map[string][]byte) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("preview: %w", err)
	}
	for name, data := range tree {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("preview: %w", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("preview: %w", err)
		}
	}
	return nil
}

func (e Env) pull(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("pull", flag.ContinueOnError)
	maxAge := flags.Duration("max-age", 0, "")
	if _, err := parse(flags, args, 0); err != nil {
		return err
	}
	return pull.Run(ctx, pull.Options{
		Dir:         e.Dir,
		MaxAge:      *maxAge,
		Now:         e.Now(),
		ToolVersion: e.toolVersion(),
		Stdout:      e.Stdout,
		Stderr:      e.Stderr,
	})
}

type synced struct {
	repo   *gitstore.Repo
	cfg    config.Config
	lease  string
	start  time.Time
	result syncer.Result
}

// runSyncer syncs the cache branch's snapshot with Jira and reports what changed.
func (e Env) runSyncer(ctx context.Context, full bool, token string) (synced, error) {
	s := synced{start: e.Now()}
	var err error
	if s.repo, err = gitstore.Open(ctx, e.Dir); err != nil {
		return s, err
	}
	root, err := config.FindRoot(e.Dir, s.repo.Dir)
	if err != nil {
		return s, err
	}
	if s.cfg, err = config.Load(root); err != nil {
		return s, err
	}
	var files map[string][]byte
	s.lease, files, err = s.repo.FetchFiles(ctx, origin, s.cfg.CacheBranch, previousRef)
	if err != nil && !errors.Is(err, gitstore.ErrBranchNotFound) {
		return s, err
	}
	previous, err := snapshot.Decode(files)
	if err != nil && s.lease != "" {
		fmt.Fprintf(e.Stderr, "warning: previous snapshot unreadable (%v); fetching everything\n", err)
	}
	s.result, err = syncer.Run(ctx, syncer.Options{
		Config:      s.cfg,
		Jira:        &jira.Client{BaseURL: s.cfg.JiraURL, Token: token, HTTP: e.HTTP},
		Previous:    previous,
		Full:        full,
		Now:         s.start,
		ToolVersion: e.toolVersion(),
	})
	if err != nil {
		return s, err
	}
	if reason := s.result.FullRefreshReason; reason != "" {
		fmt.Fprintf(e.Stdout, "full refresh: %s\n", reason)
	}
	s.result.Report(e.Stdout, e.Now().Sub(s.start))
	for _, key := range s.result.Skipped {
		fmt.Fprintf(e.Stderr, "warning: %s left during sync; skipped\n", key)
	}
	return s, nil
}
