// Package pull renders the cache branch into .jira-git-cache/.
package pull

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/config"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gitstore"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/render"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/textfmt"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/version"
)

const (
	remote     = "origin"
	pulledRef  = "refs/jira-git-cache/pulled"
	timeLayout = "2006-01-02T15:04:05Z"
	upgrade    = "upgrade: go install github.com/rosenhouse-bot-sandbox/jira-git-cache/cmd/jira-git-cache@latest"
)

var projectKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// FetchError reports that the cache branch could not be fetched.
type FetchError struct{ Err error }

// Error returns the fetch failure.
func (e *FetchError) Error() string { return e.Err.Error() }

// Unwrap returns the fetch failure.
func (e *FetchError) Unwrap() error { return e.Err }

// StaleError reports a snapshot older than MaxAge, or one never synced.
type StaleError struct {
	FetchedAt   *time.Time // nil means never synced
	Age, MaxAge time.Duration
	Branch      string
}

// Error describes the snapshot and the limit it exceeds.
func (e *StaleError) Error() string {
	if e.FetchedAt == nil {
		return ".jira-git-cache/ holds " + neverSynced(e.Branch)
	}
	return fmt.Sprintf(".jira-git-cache/ holds %s, older than %s", fetched(*e.FetchedAt, e.Age), textfmt.Duration(e.MaxAge))
}

func neverSynced(branch string) string {
	return "an empty snapshot: " + branch + " was never synced"
}

func fetched(at time.Time, age time.Duration) string {
	return fmt.Sprintf("the snapshot fetched %s (%s ago)", at.UTC().Format(timeLayout), textfmt.Age(age))
}

// Options configures Run.
type Options struct {
	Dir            string        // any directory inside the clone
	MaxAge         time.Duration // 0 means no limit
	Now            time.Time
	ToolVersion    string
	Stdout, Stderr io.Writer
}

// Run fetches the cache branch and renders it into .jira-git-cache/. It
// renders the tree even when it returns a *StaleError.
func Run(ctx context.Context, opts Options) error {
	repo, err := gitstore.Open(ctx, opts.Dir)
	if err != nil {
		return err
	}
	root, err := config.FindRoot(opts.Dir, repo.Dir)
	if err != nil {
		return err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(repo.Dir, root)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	c := cache{repo: repo, dir: filepath.Join(root, config.Dir), git: path.Join(filepath.ToSlash(rel), config.Dir)}
	if err := c.requireOutsideReplaced(opts.Dir); err != nil {
		return err
	}

	probes := []string{".lock", ".tmp-check/x", "_meta/snapshot.json"}
	for _, p := range cfg.Projects {
		probes = append(probes, p+"/_index.md")
	}
	if err := c.requireIgnored(ctx, probes); err != nil {
		return err
	}
	unlock, err := lock(ctx, filepath.Join(c.dir, ".lock"))
	if err != nil {
		return err
	}
	defer unlock()

	_, files, err := repo.FetchFiles(ctx, remote, cfg.CacheBranch, pulledRef)
	if err != nil {
		return &FetchError{fmt.Errorf("fetch %s from %s: %w", cfg.CacheBranch, remote, err)}
	}
	snap, err := snapshot.Decode(files)
	if newer := (*snapshot.NewerSchemaError)(nil); errors.As(err, &newer) {
		return fmt.Errorf("%w; %s", err, upgrade)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", cfg.CacheBranch, err)
	}
	if theirs := snap.Meta.ToolVersion; version.Newer(theirs, opts.ToolVersion) {
		fmt.Fprintf(opts.Stderr, "warning: the snapshot was written by jira-git-cache %s, newer than yours (%s); your view may differ from other readers'\n", theirs, opts.ToolVersion)
	}
	tree := render.Tree(snap)
	if err := c.requireIgnored(ctx, slices.Sorted(maps.Keys(tree))); err != nil {
		return err
	}
	if err := c.install(ctx, tree); err != nil {
		return err
	}

	meta := snap.Meta
	var age time.Duration
	if meta.FetchedAt != nil {
		age = opts.Now.Sub(*meta.FetchedAt)
	}
	if opts.MaxAge > 0 && (meta.FetchedAt == nil || age > opts.MaxAge) {
		return &StaleError{FetchedAt: meta.FetchedAt, Age: age, MaxAge: opts.MaxAge, Branch: cfg.CacheBranch}
	}
	report(opts.Stdout, cfg, meta, age)
	return nil
}

func report(w io.Writer, cfg config.Config, meta snapshot.Meta, age time.Duration) {
	if meta.FetchedAt == nil {
		fmt.Fprintf(w, "✓ .jira-git-cache/ now holds %s\n", neverSynced(cfg.CacheBranch))
		return
	}
	fmt.Fprintf(w, "✓ .jira-git-cache/ now holds %s\n", fetched(*meta.FetchedAt, age))
	order := slices.Clone(cfg.Projects)
	for _, p := range slices.Sorted(maps.Keys(meta.Projects)) {
		if !slices.Contains(cfg.Projects, p) {
			order = append(order, p)
		}
	}
	var counts []string
	for _, p := range order {
		if m, ok := meta.Projects[p]; ok {
			counts = append(counts, p+" "+textfmt.Count(m.Count)+" issues")
		}
	}
	fmt.Fprintf(w, "  %s\n", strings.Join(counts, ", "))
}

// cache locates .jira-git-cache. dir is its path on disk. git is its path
// relative to the repo top level.
type cache struct {
	repo     *gitstore.Repo
	dir, git string
}

func (c cache) gitPaths(names []string) []string {
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = c.git + "/" + name
	}
	return paths
}

// requireOutsideReplaced refuses a dir that install would move away.
func (c cache) requireOutsideReplaced(dir string) error {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	rel, _ := filepath.Rel(c.dir, dir)
	top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if top == "_meta" || projectKey.MatchString(top) {
		return fmt.Errorf("pull replaces %s/%s; run it from outside that directory", c.git, top)
	}
	return nil
}

func (c cache) requireIgnored(ctx context.Context, names []string) error {
	notIgnored, err := c.repo.NotIgnored(ctx, c.gitPaths(names))
	if err != nil {
		return err
	}
	if len(notIgnored) > 0 {
		return fmt.Errorf("%s/.gitignore does not ignore %s; restore it from setup", c.git, notIgnored[0])
	}
	return nil
}

// install writes tree into a temporary directory, moves each top-level entry
// into place, and removes ignored leftovers of earlier pulls.
func (c cache) install(ctx context.Context, tree map[string][]byte) error {
	tmp, err := os.MkdirTemp(c.dir, ".tmp-")
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := WriteTree(filepath.Join(tmp, "new"), tree); err != nil {
		return err
	}
	rendered := map[string]bool{}
	for name := range tree {
		top, _, _ := strings.Cut(name, "/")
		rendered[top] = true
	}
	if err := os.Mkdir(filepath.Join(tmp, "old"), 0o750); err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	for _, top := range slices.Sorted(maps.Keys(rendered)) {
		if err := os.Rename(filepath.Join(c.dir, top), filepath.Join(tmp, "old", top)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("pull: %w", err)
		}
		if err := os.Rename(filepath.Join(tmp, "new", top), filepath.Join(c.dir, top)); err != nil {
			return fmt.Errorf("pull: %w", err)
		}
	}
	return c.removeLeftovers(ctx, rendered)
}

// WriteTree writes tree's files under dir, readable by everyone the umask allows.
func WriteTree(dir string, tree map[string][]byte) error {
	for name, data := range tree {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // Others sharing the checkout read the cache.
			return fmt.Errorf("write tree: %w", err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // Others sharing the checkout read the cache.
			return fmt.Errorf("write tree: %w", err)
		}
	}
	return nil
}

// removeLeftovers removes ignored project directories that were not
// rendered, and temporary directories, including this pull's own.
func (c cache) removeLeftovers(ctx context.Context, rendered map[string]bool) error {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	var candidates []string
	for _, e := range entries {
		name := e.Name()
		if !rendered[name] && (projectKey.MatchString(name) || strings.HasPrefix(name, ".tmp-")) {
			candidates = append(candidates, name)
		}
	}
	keep, err := c.repo.NotIgnored(ctx, c.gitPaths(candidates))
	if err != nil {
		return err
	}
	for _, name := range candidates {
		if slices.Contains(keep, c.git+"/"+name) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(c.dir, name)); err != nil {
			return fmt.Errorf("pull: %w", err)
		}
	}
	return nil
}
