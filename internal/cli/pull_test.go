package cli_test

import (
	"testing"
	"time"
)

func TestPullNeverSynced(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	wantResult(t, f.run(t, "pull"), result{0, "✓ .jira-git-cache/ now holds an empty snapshot: dev/jira-cache was never synced\n", ""})
	wantResult(t, f.run(t, "pull", "--max-age", "1h"), result{1, "", "✗ .jira-git-cache/ holds an empty snapshot: dev/jira-cache was never synced\n"})
}

func TestPullStale(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	f.mustRun(t, "sync")
	f.now = f.now.Add(3*time.Hour + 11*time.Minute)

	wantResult(t, f.run(t, "pull", "--max-age", "2h"), result{1, "",
		"✗ .jira-git-cache/ holds the snapshot fetched 2026-10-22T07:00:00Z (3h11m ago), older than 2h\n"})
	wantResult(t, f.run(t, "pull"), result{0, "✓ .jira-git-cache/ now holds the snapshot fetched 2026-10-22T07:00:00Z (3h11m ago)\n  PLAT 2 issues\n", ""})
}

func TestPullWarnsAboutNewerTool(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	f.version = "v0.2.0"
	f.mustRun(t, "sync")
	f.version = "v0.1.0"

	r := f.run(t, "pull")

	if want := "warning: the snapshot was written by jira-git-cache v0.2.0, newer than yours (v0.1.0); your view may differ from other readers'\n"; r.code != 0 || r.stderr != want {
		t.Errorf("%+v", r)
	}
}

func TestPullFetchFailureExits2(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustRun(t, "setup", "--non-interactive", "--jira-url", f.jira.URL, "--projects", "PLAT", "--create-branch=false")
	wantResult(t, f.run(t, "pull"), result{2, "", "✗ fetch dev/jira-cache from origin: branch not found on remote\n"})
}

func TestPullWithoutConfigExits1(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	wantResult(t, f.run(t, "pull"), result{1, "",
		"✗ no .jira-git-cache/config.yml in this repo; run jira-git-cache setup, or use a clone of a cache repo\n"})
}
