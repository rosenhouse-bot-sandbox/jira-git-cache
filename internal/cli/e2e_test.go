package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gittest"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
)

// newCacheRepo runs setup with PLAT-123 in Jira, then commits and pushes main.
func newCacheRepo(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.jira.Put(jiramock.ExampleIssue())
	f.mustRun(t, "setup", "--non-interactive", "--jira-url", f.jira.URL, "--projects", "PLAT")
	gittest.Run(t, f.dir, "add", ".jira-git-cache", ".github")
	gittest.Run(t, f.dir, "commit", "--quiet", "-m", "Configure jira-git-cache")
	gittest.Run(t, f.dir, "push", "--quiet")
	return f
}

func TestSetupSyncPullTwice(t *testing.T) {
	t.Parallel()
	f := newCacheRepo(t)

	out := f.mustRun(t, "sync")
	if !strings.HasPrefix(out, "full refresh: no previous full refresh\nPLAT   3 in window: 3 new, 0 changed, 0 reused, 0 removed\n") {
		t.Errorf("first sync:\n%s", out)
	}
	if out := f.mustRun(t, "pull"); out != "✓ .jira-git-cache/ now holds the snapshot fetched 2026-10-22T07:00:00Z (0m ago)\n  PLAT 3 issues\n" {
		t.Errorf("first pull:\n%s", out)
	}
	first := blobs(t, f.remote)

	f.now = f.now.Add(30 * time.Minute)
	out = f.mustRun(t, "sync")
	if !strings.HasPrefix(out, "PLAT   3 in window: 0 new, 0 changed, 3 reused, 0 removed\nfetched 0 issues in 0s\n") {
		t.Errorf("second sync:\n%s", out)
	}
	second := blobs(t, f.remote)
	for path, sha := range first {
		if changed := second[path] != sha; changed != (path == "_meta/snapshot.json") {
			t.Errorf("%s: blob changed %t", path, changed)
		}
	}
	if len(second) != len(first) {
		t.Errorf("files: %v, then %v", first, second)
	}

	f.now = f.now.Add(12 * time.Minute)
	if out := f.mustRun(t, "pull"); out != "✓ .jira-git-cache/ now holds the snapshot fetched 2026-10-22T07:30:00Z (12m ago)\n  PLAT 3 issues\n" {
		t.Errorf("second pull:\n%s", out)
	}
	if got := f.read(t, ".jira-git-cache/PLAT/_index.md"); got != "PLAT-1 | Open | Unassigned | Task | Issue PLAT-1\n"+
		"PLAT-2 | Open | Unassigned | Task | Issue PLAT-2\n"+
		"PLAT-123 | In Progress | Jane Doe | Bug | Controller leaks watches on restart\n" {
		t.Errorf("_index.md:\n%s", got)
	}
	issue := f.read(t, ".jira-git-cache/PLAT/PLAT-123/issue.md")
	for _, line := range []string{"- Sprint: Sprint 41, Sprint 42\n", "- Story Points: 3\n", "- URL: " + f.jira.URL + "/browse/PLAT-123\n"} {
		if !strings.Contains(issue, line) {
			t.Errorf("issue.md lacks %q:\n%s", line, issue)
		}
	}
	if got := gittest.Run(t, f.dir, "status", "--porcelain"); got != "" {
		t.Errorf("git status:\n%s", got)
	}
}

// blobs maps each path on the cache branch to its blob id.
func blobs(t *testing.T, remote string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, line := range strings.Split(gittest.Run(t, remote, "ls-tree", "-r", "dev/jira-cache"), "\n") {
		meta, path, _ := strings.Cut(line, "\t")
		m[path] = strings.Fields(meta)[2]
	}
	return m
}

func TestSyncFromShallowClone(t *testing.T) {
	t.Parallel()
	f := newCacheRepo(t)
	shallow := gittest.Clone(t, f.remote, "--depth", "1", "--no-tags", "--single-branch")

	for range 2 {
		if r := f.runIn(t, shallow, "sync"); r.code != 0 {
			t.Fatalf("%+v", r)
		}
		f.now = f.now.Add(time.Hour)
	}
	if got := gittest.Run(t, f.remote, "log", "--format=%s", "dev/jira-cache"); got != "sync: PLAT=3 fetched=0 reused=3 removed=0" {
		t.Errorf("branch log:\n%s", got)
	}
}

func TestPullInBloblessClone(t *testing.T) {
	t.Parallel()
	f := newCacheRepo(t)
	f.mustRun(t, "sync")
	blobless := gittest.Clone(t, f.remote, "--filter=blob:none")

	if r := f.runIn(t, blobless, "pull"); r.code != 0 || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(blobless, ".jira-git-cache/PLAT/PLAT-123/issue.md")); err != nil {
		t.Error(err)
	}
}
