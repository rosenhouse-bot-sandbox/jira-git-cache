package cli_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gittest"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
)

func TestSyncPushesSnapshot(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)

	r := f.run(t, "sync")

	tip := f.branchTip(t)
	wantResult(t, r, result{0, `full refresh: no previous full refresh
PLAT   2 in window: 2 new, 0 changed, 0 reused, 0 removed
fetched 2 issues in 0s
pushed ` + tip[:7] + ` to dev/jira-cache
`, ""})
	want := fmt.Sprint("sync: PLAT=2 fetched=2 reused=0 removed=0 ", f.now.Unix())
	if got := gittest.Run(t, f.remote, "log", "-1", "--format=%s %ct", tip); got != want {
		t.Errorf("commit %q, want %q", got, want)
	}
	if got := gittest.Run(t, f.remote, "show", tip+":_meta/snapshot.json"); !strings.Contains(got, `"tool_version": "v0.1.0"`) {
		t.Errorf("snapshot.json:\n%s", got)
	}
}

func TestSyncFull(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	f.tick = time.Second
	f.mustRun(t, "sync")

	if out := f.mustRun(t, "sync", "--full=false"); !strings.HasPrefix(out, "PLAT   2 in window: 0 new, 0 changed, 2 reused, 0 removed\n") {
		t.Errorf("stdout:\n%s", out)
	}
	if out := f.mustRun(t, "sync", "--full"); !strings.HasPrefix(out, "full refresh: --full\nPLAT   2 in window: 0 new, 2 changed, 0 reused, 0 removed\n") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestSyncReportsElapsedTime(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	start := f.now
	f.tick = 161 * time.Second
	if out := f.mustRun(t, "sync"); !strings.Contains(out, "fetched 2 issues in 2m41s\n") {
		t.Errorf("stdout:\n%s", out)
	}
	if got := gittest.Run(t, f.remote, "log", "-1", "--format=%ct", "dev/jira-cache"); got != strconv.FormatInt(start.Unix(), 10) {
		t.Errorf("commit time %s, want the start %s", got, start)
	}
}

func TestSyncCreatesMissingBranch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mustRun(t, "setup", "--non-interactive", "--jira-url", f.jira.URL, "--projects", "PLAT", "--create-branch=false")

	r := f.run(t, "sync")

	if r.code != 0 || r.stderr != "" || !strings.HasSuffix(r.stdout, "pushed "+f.branchTip(t)[:7]+" to dev/jira-cache\n") {
		t.Errorf("%+v", r)
	}
}

func TestSyncReplacesUnreadableSnapshot(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	gittest.Seed(t, f.remote, "dev/jira-cache", map[string][]byte{"_meta/snapshot.json": []byte("x")})

	r := f.run(t, "sync")

	if r.code != 0 || !strings.HasPrefix(r.stderr, "warning: previous snapshot unreadable (") {
		t.Errorf("%+v", r)
	}
	if got := gittest.Run(t, f.remote, "ls-tree", "-r", "--name-only", "dev/jira-cache"); got != "PLAT/PLAT-1.json\nPLAT/PLAT-2.json\n_meta/fields.json\n_meta/snapshot.json" {
		t.Errorf("branch files:\n%s", got)
	}
}

func TestSyncAndPreviewRefuseBranchThatIsNotACache(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"sync"}, {"preview", t.TempDir()}} {
		f := newSetUpFixture(t)
		gittest.Seed(t, f.remote, "dev/jira-cache", map[string][]byte{"README": []byte("x")})
		tip := f.branchTip(t)

		r := f.run(t, args...)

		if r.code != 1 || r.stderr != "✗ dev/jira-cache on origin has no _meta/snapshot.json, so it is not a cache; refusing to replace it. Set cache_branch to an unused branch, or delete this one\n" {
			t.Errorf("%v: %+v", args, r)
		}
		if got := f.branchTip(t); got != tip {
			t.Errorf("%v: branch moved from %s to %s", args, tip, got)
		}
	}
}

func TestSyncWarnsAboutSkippedIssues(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	f.jira.OnRequest = func(r jiramock.Request) {
		if r.Path == "/rest/api/2/issue/PLAT-2" {
			f.jira.Delete("PLAT-2")
		}
	}

	r := f.run(t, "sync")

	if r.code != 0 || r.stderr != "warning: PLAT-2 left during sync; skipped\n" {
		t.Errorf("%+v", r)
	}
}

func TestSyncFailures(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		edit func(t *testing.T, f *fixture)
		want string
	}{
		"no token": {
			func(_ *testing.T, f *fixture) { delete(f.env, "JIRA_TOKEN") },
			"✗ JIRA_TOKEN is not set\n",
		},
		"no config": {
			func(t *testing.T, f *fixture) { gittest.Run(t, f.dir, "clean", "-fdxq") },
			"✗ no .jira-git-cache/config.yml in this repo; run jira-git-cache setup, or use a clone of a cache repo\n",
		},
		"not a repo": {
			func(t *testing.T, f *fixture) { f.dir = t.TempDir() },
			"✗ git rev-parse --show-toplevel: exit status 128: fatal: not a git repository (or any of the parent directories): .git\n",
		},
		"bad origin": {
			func(t *testing.T, f *fixture) {
				gittest.Run(t, f.dir, "remote", "set-url", "origin", f.remote+"-missing")
			},
			"✗ git fetch --no-tags --depth 1 origin +refs/heads/dev/jira-cache:refs/jira-git-cache/previous: exit status 128:",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newSetUpFixture(t)
			before := f.branchTip(t)
			tc.edit(t, f)
			r := f.run(t, "sync")
			if r.code != 1 || r.stdout != "" || !strings.HasPrefix(r.stderr, tc.want) {
				t.Errorf("%+v\nwant stderr %q", r, tc.want)
			}
			if f.branchTip(t) != before {
				t.Error("pushed")
			}
		})
	}
}

func TestSyncReportsFullRefreshBeforeFetching(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	f.env["JIRA_TOKEN"] = "wrong"
	wantResult(t, f.run(t, "sync"), result{1, "full refresh: no previous full refresh\n",
		"✗ check token: GET /rest/api/2/myself: 401 Unauthorized: Jira rejected the token (invalid or expired)\n"})
}
