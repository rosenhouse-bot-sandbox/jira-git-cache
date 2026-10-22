package cli_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gittest"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
)

func TestSetupFlagsAreAnswers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.jira.Put(jiramock.ExampleIssue())

	out := f.mustRun(t, "setup", "--non-interactive", "--jira-url", f.jira.URL+"/", "--projects", "plat",
		"--days", "7", "--unresolved-days", "90", "--runner-labels", "a, b", "--cache-branch", "jira/cache",
		"--fields", "2", "--create-branch=false", "--tool-version", "v0.2.0")

	if !strings.Contains(out, "Projects (comma-separated keys): plat\n"+
		"Keep every issue updated within days [30]: 7\n"+
		"Also keep unresolved issues updated within days [180]: 90\n") ||
		!strings.Contains(out, "Include which fields? [1,2,3]: 2\n") ||
		!strings.Contains(out, "Create jira/cache on origin with an empty snapshot? [Y/n] false\nSkipped jira/cache\n") {
		t.Errorf("stdout:\n%s", out)
	}
	want := "jira_url: " + f.jira.URL + `
cache_branch: jira/cache
projects: [PLAT]
window:
  days: 7
  unresolved_days: 90
full_refresh_every: 24h
concurrency: 8
fields:
  customfield_10007: epic_link
`
	if got := f.read(t, ".jira-git-cache/config.yml"); got != want {
		t.Errorf("config.yml:\n%s\nwant:\n%s", got, want)
	}
	workflow := f.read(t, ".github/workflows/jira-sync.yaml")
	if !strings.Contains(workflow, "runs-on: [a, b]\n") || !strings.Contains(workflow, "/cmd/jira-git-cache@v0.2.0\n") {
		t.Errorf("workflow:\n%s", workflow)
	}
	if got := gittest.Run(t, f.remote, "branch", "--list", "jira/cache"); got != "" {
		t.Errorf("branch created: %s", got)
	}
}

func TestSetupInteractiveReadsStdinAndPassword(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	delete(f.env, "JIRA_TOKEN")
	f.env["PASSWORD"] = f.jira.Token
	f.stdin = f.jira.URL + "\nPLAT\n\n\n\n\n\n"

	out := f.mustRun(t, "setup")

	if want := "Jira URL: ✓ Authenticated as svc-jira-cache"; !strings.HasPrefix(out, want) {
		t.Errorf("stdout:\n%s\nwant prefix %q", out, want)
	}
	if want := "Jira token (hidden; or set JIRA_TOKEN): "; len(f.prompts) != 1 || f.prompts[0] != want {
		t.Errorf("prompts %q, want %q", f.prompts, want)
	}
	if !strings.Contains(out, "✓ Pushed dev/jira-cache (never synced)\n") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestSetupPinsBuildVersion(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	if got := gittest.Run(t, f.remote, "log", "-1", "--format=%ct", "dev/jira-cache"); got != strconv.FormatInt(f.now.Unix(), 10) {
		t.Errorf("commit time %s, want %d", got, f.now.Unix())
	}
	if got := f.read(t, ".github/workflows/jira-sync.yaml"); !strings.Contains(got, "/cmd/jira-git-cache@v0.1.0\n") {
		t.Errorf("workflow:\n%s", got)
	}
	if got := gittest.Run(t, f.remote, "show", "dev/jira-cache:_meta/snapshot.json"); !strings.Contains(got, `"tool_version": "v0.1.0"`) {
		t.Errorf("snapshot.json:\n%s", got)
	}
}

func TestSetupRefusesUnpinnableVersion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.version = "(devel)"
	r := f.run(t, "setup", "--non-interactive", "--jira-url", f.jira.URL, "--projects", "PLAT")
	wantResult(t, r, result{1, "", "✗ cannot pin version dev in the workflow; pass --tool-version vX.Y.Z\n"})
}
