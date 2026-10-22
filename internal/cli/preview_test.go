package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func files(t *testing.T, dir string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			paths = append(paths, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestPreviewRendersWithoutPushing(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	before := f.branchTip(t)

	r := f.run(t, "preview", "out")

	wantResult(t, r, result{0, `full refresh: no previous full refresh
PLAT   2 in window: 2 new, 0 changed, 0 reused, 0 removed
fetched 2 issues in 0s
wrote out (not pushed)
`, ""})
	want := ".jira-git-cache-preview PLAT/PLAT-1/issue.json PLAT/PLAT-1/issue.md PLAT/PLAT-2/issue.json PLAT/PLAT-2/issue.md " +
		"PLAT/_index.md _meta/fields.json _meta/snapshot.json"
	if got := strings.Join(files(t, f.path("out")), " "); got != want {
		t.Errorf("files: %s\nwant:  %s", got, want)
	}
	if got := f.read(t, "out/PLAT/_index.md"); got != "PLAT-1 | Open | Unassigned | Task | Issue PLAT-1\nPLAT-2 | Open | Unassigned | Task | Issue PLAT-2\n" {
		t.Errorf("_index.md:\n%s", got)
	}
	if f.branchTip(t) != before {
		t.Error("pushed")
	}
}

func TestPreviewReplacesEarlierPreview(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	f.mustRun(t, "sync")
	dir := filepath.Join(t.TempDir(), "preview")
	f.mustRun(t, "preview", dir)
	f.jira.Delete("PLAT-2")

	out := f.mustRun(t, "preview", "--full", dir)

	if !strings.HasPrefix(out, "full refresh: --full\nPLAT   1 in window: 0 new, 1 changed, 0 reused, 1 removed\n") {
		t.Errorf("stdout:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "PLAT/PLAT-2")); !os.IsNotExist(err) {
		t.Errorf("PLAT-2 left behind: %v", err)
	}
}

func TestPreviewPromptsForToken(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	delete(f.env, "JIRA_TOKEN")
	f.env["PASSWORD"] = f.jira.Token

	f.mustRun(t, "preview", t.TempDir())

	if want := "Jira token (hidden; or set JIRA_TOKEN): "; len(f.prompts) != 1 || f.prompts[0] != want {
		t.Errorf("prompts %q, want %q", f.prompts, want)
	}
}

func TestPreviewFailures(t *testing.T) {
	t.Parallel()
	f := newSetUpFixture(t)
	f.touch(t, "full/x")
	f.touch(t, "foreign/.jira-git-cache-preview")
	f.touch(t, "foreign/config.yml")
	f.touch(t, ".jira-git-cache/.jira-git-cache-preview")
	f.touch(t, "file")
	for _, dir := range []string{"full", "foreign", ".jira-git-cache"} {
		want := "✗ refusing to replace " + dir + ": it is neither empty nor an earlier preview\n"
		wantResult(t, f.run(t, "preview", dir), result{1, "", want})
	}
	if r := f.run(t, "preview", "file"); r.code != 1 || !strings.HasPrefix(r.stderr, "✗ preview: ") {
		t.Errorf("%+v", r)
	}
	if got := f.read(t, ".jira-git-cache/config.yml"); !strings.HasPrefix(got, "jira_url: ") {
		t.Errorf("config.yml:\n%s", got)
	}

	delete(f.env, "JIRA_TOKEN")
	wantResult(t, f.run(t, "preview", "new"), result{1, "", "✗ read token: stdin is not a terminal\n"})
}
