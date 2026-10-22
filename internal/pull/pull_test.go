package pull_test

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/config"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gitstore"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gittest"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/pull"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/render"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
)

func TestMain(m *testing.M) {
	cleanup := gittest.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

const (
	branch    = "dev/jira-cache"
	configYML = "jira_url: https://jira.example.com\ncache_branch: dev/jira-cache\nprojects: [PLAT, INFRA, NEW]\n"
	gitignore = "/*\n!/config.yml\n!/.gitignore\n"
)

var (
	fetchedAt = time.Date(2026, 10, 22, 5, 0, 41, 0, time.UTC)
	now       = fetchedAt.Add(12*time.Minute + 30*time.Second)
)

type fixture struct {
	remote, dir    string
	stdout, stderr strings.Builder
	opts           pull.Options
}

// newFixture clones a remote whose main branch holds mainFiles.
func newFixture(t *testing.T, mainFiles map[string]string) *fixture {
	t.Helper()
	t.Parallel()
	f := &fixture{remote: gittest.Remote(t)}
	files := map[string][]byte{}
	for path, content := range mainFiles {
		files[path] = []byte(content)
	}
	gittest.Seed(t, f.remote, "main", files)
	f.dir = gittest.Clone(t, f.remote)
	f.opts = pull.Options{Dir: f.dir, Now: now, ToolVersion: "v0.1.0", Stdout: &f.stdout, Stderr: &f.stderr}
	return f
}

func defaultMain() map[string]string {
	return map[string]string{".jira-git-cache/config.yml": configYML, ".jira-git-cache/.gitignore": gitignore}
}

func (f *fixture) seed(t *testing.T, s snapshot.Snapshot) {
	t.Helper()
	gittest.Seed(t, f.remote, branch, s.Encode())
}

func (f *fixture) run(t *testing.T) error {
	t.Helper()
	f.stdout.Reset()
	f.stderr.Reset()
	return pull.Run(t.Context(), f.opts)
}

func (f *fixture) mustRun(t *testing.T) {
	t.Helper()
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) write(t *testing.T, path, content string) {
	t.Helper()
	path = filepath.Join(f.dir, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func issue(key string) snapshot.Issue {
	return snapshot.Issue{
		ID: "1", Key: key, Summary: "Issue " + key, Status: "Open", Type: "Task",
		Labels: []string{}, Links: []snapshot.Link{},
		Created: "2026-10-01T14:02:11.999Z", Updated: "2026-10-20T09:15:40.000Z",
		Custom: map[string]any{}, Comments: []snapshot.Comment{},
	}
}

func synced(fetched *time.Time, projects map[string]int, keys ...string) snapshot.Snapshot {
	s := snapshot.Snapshot{
		Meta: snapshot.Meta{
			FetchedAt: fetched, LastFullAt: fetched, JiraURL: "https://jira.example.com",
			Projects: map[string]snapshot.ProjectMeta{}, SchemaVersion: snapshot.SchemaVersion, ToolVersion: "v0.1.0",
		},
		Fields: []snapshot.Field{},
		Issues: map[string]snapshot.Issue{},
	}
	for p, n := range projects {
		s.Meta.Projects[p] = snapshot.ProjectMeta{JQL: "project = " + p, Count: n}
	}
	for _, key := range keys {
		s.Issues[key] = issue(key)
	}
	return s
}

// cacheFiles reads every file under cache except config.yml, .gitignore and .lock.
func cacheFiles(t *testing.T, cache string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(cache, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(cache, path)
		if err != nil {
			return err
		}
		if rel == "config.yml" || rel == ".gitignore" || rel == ".lock" {
			return nil
		}
		files[filepath.ToSlash(rel)], err = os.ReadFile(path) //nolint:gosec // test reads its own temp dir
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertFiles(t *testing.T, got, want map[string][]byte) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("files: got %q, want %q", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
}

func TestPullRendersSnapshotFromSubdirectory(t *testing.T) {
	f := newFixture(t, defaultMain())
	s := synced(&fetchedAt, map[string]int{"INFRA": 387, "PLAT": 1204, "ZED": 0, "OLD": 1}, "PLAT-1", "PLAT-10", "INFRA-7", "OLD-1")
	f.seed(t, s)
	f.opts.Dir = filepath.Join(f.dir, "docs")
	if err := os.Mkdir(f.opts.Dir, 0o750); err != nil {
		t.Fatal(err)
	}

	f.mustRun(t)

	assertFiles(t, cacheFiles(t, filepath.Join(f.dir, ".jira-git-cache")), render.Tree(s))
	const want = "✓ .jira-git-cache/ now holds the snapshot fetched 2026-10-22T05:00:41Z (12m ago)\n" +
		"  PLAT 1,204 issues, INFRA 387 issues, OLD 1 issue, ZED 0 issues\n"
	if got := f.stdout.String(); got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
	}
	if got := f.stderr.String(); got != "" {
		t.Errorf("stderr: %q", got)
	}
}

func TestPullRefusesToRunInsideADirectoryItReplaces(t *testing.T) {
	for _, dir := range []string{"PLAT", "PLAT/PLAT-1", "_meta"} {
		t.Run(dir, func(t *testing.T) {
			f := newFixture(t, defaultMain())
			s := synced(&fetchedAt, map[string]int{"PLAT": 1}, "PLAT-1")
			f.seed(t, s)
			f.mustRun(t)
			f.opts.Dir = filepath.Join(f.dir, ".jira-git-cache", dir)

			err := f.run(t)

			want := "pull replaces .jira-git-cache/" + strings.Split(dir, "/")[0] + "; run it from outside that directory"
			if err == nil || err.Error() != want {
				t.Errorf("got %v, want %s", err, want)
			}
			if _, err := os.Stat(f.opts.Dir); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestPullRefusesToRunInsideALinkToADirectoryItReplaces(t *testing.T) {
	f := newFixture(t, defaultMain())
	f.seed(t, synced(&fetchedAt, map[string]int{"PLAT": 1}, "PLAT-1"))
	f.mustRun(t)
	f.opts.Dir = filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(f.dir, ".jira-git-cache", "PLAT"), f.opts.Dir); err != nil {
		t.Fatal(err)
	}

	if err := f.run(t); err == nil || !strings.HasPrefix(err.Error(), "pull replaces ") {
		t.Errorf("got %v", err)
	}
}

func TestPullRunsInsideCacheDirectory(t *testing.T) {
	f := newFixture(t, defaultMain())
	f.seed(t, synced(&fetchedAt, map[string]int{"PLAT": 1}, "PLAT-1"))
	f.write(t, ".jira-git-cache/notes/x", "")
	for _, dir := range []string{".jira-git-cache", ".jira-git-cache/notes"} {
		f.opts.Dir = filepath.Join(f.dir, dir)
		f.mustRun(t)
	}
}

func TestPullNeverShowsAHalfWrittenProject(t *testing.T) {
	f := newFixture(t, defaultMain())
	var keys []string
	for i := range 500 {
		keys = append(keys, fmt.Sprint("PLAT-", i+1))
	}
	f.seed(t, synced(&fetchedAt, map[string]int{"PLAT": len(keys)}, keys...))
	project := filepath.Join(f.dir, ".jira-git-cache", "PLAT")
	stop, missing := make(chan struct{}), make(chan string, 1)
	go func() {
		defer close(missing)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if key := missingIssue(project); key != "" {
				missing <- key
				return
			}
		}
	}()

	f.mustRun(t)
	close(stop)

	if key := <-missing; key != "" {
		t.Errorf("PLAT/_index.md listed %s before its issue.md existed", key)
	}
}

// missingIssue returns a key that dir/_index.md lists without its issue.md.
// It holds dir open, so a rename of dir does not change what it sees.
func missingIssue(dir string) string {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }()
	index, err := root.Open("_index.md")
	if err != nil {
		return ""
	}
	defer func() { _ = index.Close() }()
	data, err := io.ReadAll(index)
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(data)) {
		key, _, _ := strings.Cut(line, " | ")
		if _, err := root.Stat(key + "/issue.md"); err != nil {
			return key
		}
	}
	return ""
}

func TestPullWritesReadableFiles(t *testing.T) {
	f := newFixture(t, defaultMain())
	f.seed(t, synced(&fetchedAt, map[string]int{"PLAT": 1}, "PLAT-1"))
	f.mustRun(t)

	ref := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ref, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ref, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"_meta":                  "dir",
		"PLAT":                   "dir",
		"PLAT/PLAT-1":            "dir",
		"_meta/snapshot.json":    "file",
		"PLAT/_index.md":         "file",
		"PLAT/PLAT-1/issue.md":   "file",
		"PLAT/PLAT-1/issue.json": "file",
	} {
		if got, want := mode(t, filepath.Join(f.dir, ".jira-git-cache", path)), mode(t, filepath.Join(ref, want)); got != want {
			t.Errorf("%s: mode %v, want %v", path, got, want)
		}
	}
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}

func TestPullFindsConfigInRepoSubdirectory(t *testing.T) {
	f := newFixture(t, map[string]string{
		"sub/.jira-git-cache/config.yml": configYML,
		"sub/.jira-git-cache/.gitignore": gitignore,
	})
	s := synced(&fetchedAt, map[string]int{"PLAT": 1}, "PLAT-1")
	f.seed(t, s)
	f.opts.Dir = filepath.Join(f.dir, "sub")

	f.mustRun(t)

	assertFiles(t, cacheFiles(t, filepath.Join(f.dir, "sub", ".jira-git-cache")), render.Tree(s))
}

func TestPullReplacesPreviousRenderAndKeepsOtherFiles(t *testing.T) {
	f := newFixture(t, defaultMain())
	f.seed(t, synced(&fetchedAt, map[string]int{"PLAT": 2, "OLD": 1}, "PLAT-1", "PLAT-2", "OLD-1"))
	f.mustRun(t)
	keep := map[string]string{"NOTES.md": "notes\n", "README": "readme\n", "notes.txt": "local\n"}
	for name, content := range keep {
		f.write(t, ".jira-git-cache/"+name, content)
	}
	gittest.Run(t, f.dir, "add", "--force", ".jira-git-cache/NOTES.md", ".jira-git-cache/README")
	gittest.Run(t, f.dir, "commit", "--quiet", "-m", "notes")
	f.write(t, ".jira-git-cache/PLAT/PLAT-1/local.md", "lost\n")
	f.write(t, ".jira-git-cache/GONE/x", "lost\n")
	f.write(t, ".jira-git-cache/.tmp-crashed/new/x", "lost\n")
	s := synced(&fetchedAt, map[string]int{"PLAT": 2}, "PLAT-2", "PLAT-3")
	f.seed(t, s)

	f.mustRun(t)

	want := render.Tree(s)
	for name, content := range keep {
		want[name] = []byte(content)
	}
	assertFiles(t, cacheFiles(t, filepath.Join(f.dir, ".jira-git-cache")), want)
}

func TestPullRefusesUnignoredPaths(t *testing.T) {
	for _, tc := range []struct{ gitignore, path string }{
		{"", ".jira-git-cache/.lock"},
		{gitignore + "!/.tmp-*/\n", ".jira-git-cache/.tmp-check/x"},
		{gitignore + "!/_meta/\n", ".jira-git-cache/_meta/snapshot.json"},
		{gitignore + "!/NEW/\n", ".jira-git-cache/NEW/_index.md"},
		{gitignore + "!/OLD/\n", ".jira-git-cache/OLD/OLD-1/issue.json"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			main := defaultMain()
			main[".jira-git-cache/.gitignore"] = tc.gitignore
			f := newFixture(t, main)
			f.seed(t, synced(&fetchedAt, map[string]int{"PLAT": 1, "OLD": 1}, "PLAT-1", "OLD-1"))

			err := f.run(t)

			want := ".jira-git-cache/.gitignore does not ignore " + tc.path + "; restore it from setup"
			if err == nil || err.Error() != want {
				t.Errorf("got %v, want %s", err, want)
			}
			assertFiles(t, cacheFiles(t, filepath.Join(f.dir, ".jira-git-cache")), map[string][]byte{})
		})
	}
}

func TestPullFetchFailureLeavesCacheAlone(t *testing.T) {
	for _, tc := range []struct {
		name       string
		breakFetch func(t *testing.T, f *fixture)
		branchGone bool
	}{
		{"missing branch", func(t *testing.T, f *fixture) {
			gittest.Run(t, f.remote, "update-ref", "-d", "refs/heads/"+branch)
		}, true},
		{"unreachable origin", func(t *testing.T, f *fixture) {
			gittest.Run(t, f.dir, "remote", "set-url", "origin", "file://"+filepath.Join(f.remote, "missing"))
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, defaultMain())
			s := synced(&fetchedAt, map[string]int{"PLAT": 1}, "PLAT-1")
			f.seed(t, s)
			f.mustRun(t)
			tc.breakFetch(t, f)

			err := f.run(t)

			var fetchErr *pull.FetchError
			if !errors.As(err, &fetchErr) {
				t.Fatalf("got %v, want *FetchError", err)
			}
			if got := errors.Is(err, gitstore.ErrBranchNotFound); got != tc.branchGone {
				t.Errorf("ErrBranchNotFound: %v; err %v", got, err)
			}
			if !strings.HasPrefix(err.Error(), "fetch dev/jira-cache from origin: ") {
				t.Errorf("err: %v", err)
			}
			assertFiles(t, cacheFiles(t, filepath.Join(f.dir, ".jira-git-cache")), render.Tree(s))
			if f.stdout.Len() > 0 {
				t.Errorf("stdout: %q", f.stdout.String())
			}
		})
	}
}

func TestPullRefusesNewerSchema(t *testing.T) {
	f := newFixture(t, defaultMain())
	gittest.Seed(t, f.remote, branch, map[string][]byte{"_meta/snapshot.json": []byte(`{"schema_version":2}`)})

	err := f.run(t)

	var newer *snapshot.NewerSchemaError
	if !errors.As(err, &newer) {
		t.Fatalf("got %v, want *NewerSchemaError", err)
	}
	const want = "snapshot schema_version 2 is newer than this jira-git-cache supports (1)" +
		"; upgrade: go install github.com/rosenhouse-bot-sandbox/jira-git-cache/cmd/jira-git-cache@latest"
	if err.Error() != want {
		t.Errorf("got %v", err)
	}
	assertFiles(t, cacheFiles(t, filepath.Join(f.dir, ".jira-git-cache")), map[string][]byte{})
}

func TestPullRefusesBranchThatIsNotASnapshot(t *testing.T) {
	f := newFixture(t, defaultMain())
	gittest.Seed(t, f.remote, branch, map[string][]byte{"README.md": []byte("hi\n")})

	err := f.run(t)

	if err == nil || !strings.HasSuffix(err.Error(), "not a jira-git-cache snapshot") {
		t.Errorf("got %v", err)
	}
	var fetchErr *pull.FetchError
	if errors.As(err, &fetchErr) {
		t.Errorf("got *FetchError")
	}
	assertFiles(t, cacheFiles(t, filepath.Join(f.dir, ".jira-git-cache")), map[string][]byte{})
}

func TestPullWarnsWhenSnapshotToolIsNewer(t *testing.T) {
	for _, tc := range []struct{ theirs, ours, warning string }{
		{"v0.2.0", "v0.1.0", "warning: the snapshot was written by jira-git-cache v0.2.0, newer than yours (v0.1.0); your view may differ from other readers'\n"},
		{"v0.1.0", "v0.0.0-20261022131232-d376b2d5fe4d", "warning: the snapshot was written by jira-git-cache v0.1.0, newer than yours (v0.0.0-20261022131232-d376b2d5fe4d); your view may differ from other readers'\n"},
		{"v0.1.0", "v0.1.0", ""},
		{"v0.1.0", "v0.2.0", ""},
	} {
		t.Run(tc.theirs+" "+tc.ours, func(t *testing.T) {
			f := newFixture(t, defaultMain())
			s := synced(&fetchedAt, nil)
			s.Meta.ToolVersion = tc.theirs
			f.seed(t, s)
			f.opts.ToolVersion = tc.ours

			f.mustRun(t)

			if got := f.stderr.String(); got != tc.warning {
				t.Errorf("stderr %q, want %q", got, tc.warning)
			}
		})
	}
}

func TestPullNeverSyncedSnapshot(t *testing.T) {
	f := newFixture(t, defaultMain())
	s := synced(nil, nil)
	f.seed(t, s)

	f.mustRun(t)

	assertFiles(t, cacheFiles(t, filepath.Join(f.dir, ".jira-git-cache")), render.Tree(s))
	const want = "✓ .jira-git-cache/ now holds an empty snapshot: dev/jira-cache was never synced\n"
	if got := f.stdout.String(); got != want {
		t.Errorf("stdout %q, want %q", got, want)
	}
}

func TestPullMaxAge(t *testing.T) {
	fetched := time.Date(2026, 10, 22, 2, 30, 12, 0, time.UTC)
	age := 3*time.Hour + 11*time.Minute + 30*time.Second
	for _, tc := range []struct {
		name      string
		fetchedAt *time.Time
		maxAge    time.Duration
		want      string
	}{
		{"no limit", &fetched, 0, ""},
		{"at limit", &fetched, age, ""},
		{"over limit", &fetched, 2 * time.Hour,
			".jira-git-cache/ holds the snapshot fetched 2026-10-22T02:30:12Z (3h11m ago), older than 2h"},
		{"just over limit", &fetched, age - time.Second,
			".jira-git-cache/ holds the snapshot fetched 2026-10-22T02:30:12Z (3h11m ago), older than 3h11m29s"},
		{"never synced", nil, 24 * time.Hour,
			".jira-git-cache/ holds an empty snapshot: dev/jira-cache was never synced"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, defaultMain())
			s := synced(tc.fetchedAt, map[string]int{"PLAT": 1}, "PLAT-1")
			f.seed(t, s)
			f.opts.MaxAge = tc.maxAge
			f.opts.Now = fetched.Add(age)

			err := f.run(t)

			assertFiles(t, cacheFiles(t, filepath.Join(f.dir, ".jira-git-cache")), render.Tree(s))
			if tc.want == "" {
				if err != nil || f.stdout.Len() == 0 {
					t.Errorf("err %v, stdout %q", err, f.stdout.String())
				}
				return
			}
			var stale *pull.StaleError
			if !errors.As(err, &stale) {
				t.Fatalf("got %v, want *StaleError", err)
			}
			if err.Error() != tc.want {
				t.Errorf("got %q, want %q", err, tc.want)
			}
			wantAge := age
			if tc.fetchedAt == nil {
				wantAge = 0
			}
			wantErr := pull.StaleError{FetchedAt: tc.fetchedAt, Age: wantAge, MaxAge: tc.maxAge, Branch: branch}
			if !reflect.DeepEqual(*stale, wantErr) {
				t.Errorf("got %+v, want %+v", *stale, wantErr)
			}
			if f.stdout.Len() > 0 {
				t.Errorf("stdout: %q", f.stdout.String())
			}
		})
	}
}

func TestPullReportsLockFailure(t *testing.T) {
	f := newFixture(t, defaultMain())
	f.write(t, ".jira-git-cache/.lock/x", "")

	if err := f.run(t); err == nil || !strings.HasPrefix(err.Error(), "lock: ") {
		t.Errorf("got %v", err)
	}
}

func TestPullNeedsConfig(t *testing.T) {
	f := newFixture(t, map[string]string{"README.md": "hi\n"})

	if err := f.run(t); !errors.Is(err, config.ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

func TestPullRejectsInvalidConfig(t *testing.T) {
	f := newFixture(t, map[string]string{".jira-git-cache/config.yml": "jira_url: x\n"})

	if err := f.run(t); err == nil || !strings.HasPrefix(err.Error(), "config.yml: jira_url") {
		t.Errorf("got %v", err)
	}
}

func TestPullNeedsGitRepo(t *testing.T) {
	t.Parallel()
	var gitErr *gitstore.Error
	err := pull.Run(t.Context(), pull.Options{Dir: t.TempDir()})
	if !errors.As(err, &gitErr) {
		t.Errorf("got %v", err)
	}
}
