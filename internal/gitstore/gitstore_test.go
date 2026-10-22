package gitstore_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gitstore"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gittest"
)

func TestMain(m *testing.M) {
	cleanup := gittest.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

const previous = "refs/jira-git-cache/previous"

var when = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func open(t *testing.T, dir string) *gitstore.Repo {
	t.Helper()
	repo, err := gitstore.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func publish(t *testing.T, repo *gitstore.Repo, branch, expect string, files map[string][]byte) string {
	t.Helper()
	commit, err := repo.Publish(context.Background(), "origin", branch, expect, files, "msg", when)
	if err != nil {
		t.Fatal(err)
	}
	return commit
}

func fetch(t *testing.T, repo *gitstore.Repo, branch string) (string, map[string][]byte) {
	t.Helper()
	commit, files, err := repo.FetchFiles(context.Background(), "origin", branch, previous)
	if err != nil {
		t.Fatal(err)
	}
	return commit, files
}

func TestOpenFindsTopLevelFromSubdirectory(t *testing.T) {
	clone := gittest.Clone(t, gittest.Remote(t))
	sub := filepath.Join(clone, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := open(t, sub)
	want, err := filepath.EvalSymlinks(clone)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Dir != want {
		t.Errorf("Dir = %q, want %q", repo.Dir, want)
	}
}

func TestOpenOutsideRepoFails(t *testing.T) {
	_, err := gitstore.Open(context.Background(), t.TempDir())
	var gitErr *gitstore.Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("err = %v, want *gitstore.Error", err)
	}
	if !strings.Contains(gitErr.Stderr, "not a git repository") {
		t.Errorf("Stderr = %q", gitErr.Stderr)
	}
	if !reflect.DeepEqual(gitErr.Args, []string{"rev-parse", "--show-toplevel"}) {
		t.Errorf("Args = %q", gitErr.Args)
	}
	if !strings.Contains(err.Error(), "git rev-parse --show-toplevel") || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestDefaultBranch(t *testing.T) {
	remote := gittest.Remote(t)
	gittest.Seed(t, remote, "trunk", nil)
	gittest.Run(t, remote, "symbolic-ref", "HEAD", "refs/heads/trunk")
	repo := open(t, gittest.Clone(t, remote))
	got, err := repo.DefaultBranch(context.Background(), "origin")
	if err != nil {
		t.Fatal(err)
	}
	if got != "trunk" {
		t.Errorf("DefaultBranch = %q, want trunk", got)
	}
}

func TestDefaultBranchWithoutSymrefFails(t *testing.T) {
	remote := gittest.Remote(t)
	repo := open(t, gittest.Clone(t, remote))
	gittest.Run(t, remote, "symbolic-ref", "HEAD", "refs/heads/gone")
	_, err := repo.DefaultBranch(context.Background(), "origin")
	if err == nil {
		t.Fatal("want error")
	}
}

func TestDefaultBranchUnknownRemoteFails(t *testing.T) {
	repo := open(t, gittest.Clone(t, gittest.Remote(t)))
	_, err := repo.DefaultBranch(context.Background(), "nope")
	var gitErr *gitstore.Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("err = %v, want *gitstore.Error", err)
	}
}

func TestRemoteHasBranch(t *testing.T) {
	remote := gittest.Remote(t)
	gittest.Seed(t, remote, "dev/jira-cache", nil)
	gittest.Seed(t, remote, "x/dev/jira-cache-old", nil)
	repo := open(t, gittest.Clone(t, remote))
	for branch, want := range map[string]bool{
		"dev/jira-cache": true,
		"jira-cache":     false,
		"dev/jira":       false,
		"missing":        false,
	} {
		got, err := repo.RemoteHasBranch(context.Background(), "origin", branch)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("RemoteHasBranch(%q) = %v, want %v", branch, got, want)
		}
	}
}

func TestRemoteHasBranchUnknownRemoteFails(t *testing.T) {
	repo := open(t, gittest.Clone(t, gittest.Remote(t)))
	_, err := repo.RemoteHasBranch(context.Background(), "origin2", "main")
	var gitErr *gitstore.Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("err = %v, want *gitstore.Error", err)
	}
}

func TestPublishThenFetchRoundTrips(t *testing.T) {
	remote := gittest.Remote(t)
	writer := open(t, gittest.Clone(t, remote))
	files := map[string][]byte{
		"_meta/snapshot.json":    []byte("{}\n"),
		"PLAT/PLAT-1.json":       []byte("one\n"),
		"PLAT/PLAT-10.json":      []byte(""),
		"name with spaces.json":  []byte("spaces"),
		"unicode-ä/\"quote\".md": []byte("\x00binary\r\n"),
	}
	commit := publish(t, writer, "dev/jira-cache", "", files)

	if got := gittest.Run(t, remote, "rev-parse", "refs/heads/dev/jira-cache"); got != commit {
		t.Errorf("remote branch = %s, want %s", got, commit)
	}
	reader := open(t, gittest.Clone(t, remote))
	gotCommit, gotFiles := fetch(t, reader, "dev/jira-cache")
	if gotCommit != commit {
		t.Errorf("fetched commit = %s, want %s", gotCommit, commit)
	}
	if !reflect.DeepEqual(gotFiles, files) {
		t.Errorf("files = %q, want %q", gotFiles, files)
	}
	if got := gittest.Run(t, reader.Dir, "rev-parse", previous); got != commit {
		t.Errorf("%s = %s, want %s", previous, got, commit)
	}
}

func TestPublishWritesParentlessBotCommit(t *testing.T) {
	remote := gittest.Remote(t)
	writer := open(t, gittest.Clone(t, remote))
	commit := publish(t, writer, "cache", "", map[string][]byte{"a": []byte("a")})

	got := gittest.Run(t, remote, "log", "-1", "--format=%P|%an|%ae|%at|%cn|%ce|%ct|%B", commit)
	want := fmt.Sprintf("|%s|%s|%d|%s|%s|%d|msg", gitstore.BotName, gitstore.BotEmail, when.Unix(),
		gitstore.BotName, gitstore.BotEmail, when.Unix())
	if got != want {
		t.Errorf("commit = %q, want %q", got, want)
	}
	if mode := gittest.Run(t, remote, "ls-tree", commit, "a"); !strings.HasPrefix(mode, "100644 blob ") {
		t.Errorf("ls-tree = %q, want mode 100644", mode)
	}
}

func TestPublishLeavesNoTraceInWorkTree(t *testing.T) {
	remote := gittest.Remote(t)
	clone := gittest.Clone(t, remote)
	writer := open(t, clone)
	publish(t, writer, "cache", "", map[string][]byte{"a/b": []byte("b")})

	if got := gittest.Run(t, clone, "status", "--porcelain", "--ignored"); got != "" {
		t.Errorf("status = %q, want clean", got)
	}
	if got := gittest.Run(t, clone, "rev-parse", "HEAD:README.md"); got == "" {
		t.Error("README.md missing from HEAD")
	}
}

func TestPublishEmptyMapGivesEmptyTree(t *testing.T) {
	remote := gittest.Remote(t)
	writer := open(t, gittest.Clone(t, remote))
	commit := publish(t, writer, "cache", "", map[string][]byte{})

	if got := gittest.Run(t, remote, "rev-parse", commit+"^{tree}"); got != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" {
		t.Errorf("tree = %s, want the empty tree", got)
	}
	_, files := fetch(t, open(t, gittest.Clone(t, remote)), "cache")
	if len(files) != 0 {
		t.Errorf("files = %q, want none", files)
	}
}

func TestPublishReplacesBranchWhenLeaseHolds(t *testing.T) {
	remote := gittest.Remote(t)
	writer := open(t, gittest.Clone(t, remote))
	first := publish(t, writer, "cache", "", map[string][]byte{"a": []byte("1")})
	second := publish(t, writer, "cache", first, map[string][]byte{"b": []byte("2")})

	if got := gittest.Run(t, remote, "rev-parse", "refs/heads/cache"); got != second {
		t.Errorf("remote branch = %s, want %s", got, second)
	}
	_, files := fetch(t, writer, "cache")
	if !reflect.DeepEqual(files, map[string][]byte{"b": []byte("2")}) {
		t.Errorf("files = %q", files)
	}
}

func TestPublishFailsWhenRemoteMoved(t *testing.T) {
	remote := gittest.Remote(t)
	writer := open(t, gittest.Clone(t, remote))
	first := publish(t, writer, "cache", "", map[string][]byte{"a": []byte("1")})
	concurrent := gittest.Seed(t, remote, "cache", map[string][]byte{"c": []byte("c")})

	_, err := writer.Publish(context.Background(), "origin", "cache", first, map[string][]byte{"b": []byte("2")}, "msg", when)
	if !errors.Is(err, gitstore.ErrRemoteMoved) {
		t.Fatalf("err = %v, want ErrRemoteMoved", err)
	}
	if got := gittest.Run(t, remote, "rev-parse", "refs/heads/cache"); got != concurrent {
		t.Errorf("remote branch = %s, want %s", got, concurrent)
	}
}

func TestPublishWithEmptyLeaseFailsWhenBranchExists(t *testing.T) {
	remote := gittest.Remote(t)
	existing := gittest.Seed(t, remote, "cache", nil)
	writer := open(t, gittest.Clone(t, remote))

	_, err := writer.Publish(context.Background(), "origin", "cache", "", map[string][]byte{"b": []byte("2")}, "msg", when)
	if !errors.Is(err, gitstore.ErrRemoteMoved) {
		t.Fatalf("err = %v, want ErrRemoteMoved", err)
	}
	if got := gittest.Run(t, remote, "rev-parse", "refs/heads/cache"); got != existing {
		t.Errorf("remote branch = %s, want %s", got, existing)
	}
}

func TestPublishOtherPushFailureIsGitError(t *testing.T) {
	remote := gittest.Remote(t)
	writer := open(t, gittest.Clone(t, remote))
	hook := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := writer.Publish(context.Background(), "origin", "cache", "", map[string][]byte{"a": []byte("1")}, "msg", when)
	var gitErr *gitstore.Error
	if !errors.As(err, &gitErr) || errors.Is(err, gitstore.ErrRemoteMoved) {
		t.Fatalf("err = %v, want a *gitstore.Error that is not ErrRemoteMoved", err)
	}
}

func TestPublishRejectsInvalidPaths(t *testing.T) {
	remote := gittest.Remote(t)
	writer := open(t, gittest.Clone(t, remote))
	for _, path := range []string{"../escape", ".git/config", "a//b", "/abs", ""} {
		files := map[string][]byte{path: nil, "ok": nil}
		_, err := writer.Publish(context.Background(), "origin", "cache", "", files, "msg", when)
		if err == nil || !strings.HasSuffix(err.Error(), fmt.Sprintf("rejected the paths [%q]", path)) {
			t.Errorf("%q: err = %v, want a rejected path", path, err)
		}
	}
	if got := gittest.Run(t, remote, "branch", "--list", "cache"); got != "" {
		t.Errorf("remote has cache branch: %q", got)
	}
}

func TestPublishRemovesTemporaryFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	writer := open(t, gittest.Clone(t, gittest.Remote(t)))
	publish(t, writer, "cache", "", map[string][]byte{"a/b": []byte("b")})

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("TMPDIR holds %v, want nothing", entries)
	}
}

func TestPublishFromShallowClone(t *testing.T) {
	remote := gittest.Remote(t)
	gittest.Seed(t, remote, "main", map[string][]byte{"README.md": []byte("again\n")})
	clone := gittest.Clone(t, remote, "--depth", "1")
	if got := gittest.Run(t, clone, "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Fatalf("clone is not shallow")
	}
	commit := publish(t, open(t, clone), "cache", "", map[string][]byte{"a": []byte("a")})

	_, files := fetch(t, open(t, gittest.Clone(t, remote)), "cache")
	if !reflect.DeepEqual(files, map[string][]byte{"a": []byte("a")}) {
		t.Errorf("files = %q", files)
	}
	if got := gittest.Run(t, remote, "rev-parse", "refs/heads/cache"); got != commit {
		t.Errorf("remote branch = %s, want %s", got, commit)
	}
}

func TestFetchMissingBranch(t *testing.T) {
	repo := open(t, gittest.Clone(t, gittest.Remote(t)))
	_, _, err := repo.FetchFiles(context.Background(), "origin", "dev/jira-cache", previous)
	if !errors.Is(err, gitstore.ErrBranchNotFound) {
		t.Fatalf("err = %v, want ErrBranchNotFound", err)
	}
}

func TestFetchFailureForAnotherReasonIsGitError(t *testing.T) {
	remote := gittest.Remote(t)
	gittest.Seed(t, remote, "cache", nil)
	repo := open(t, gittest.Clone(t, remote))
	for name, args := range map[string][2]string{
		"unknown remote":    {"nope", previous},
		"invalid local ref": {"origin", "refs/bad..ref"},
	} {
		_, _, err := repo.FetchFiles(context.Background(), args[0], "cache", args[1])
		var gitErr *gitstore.Error
		if !errors.As(err, &gitErr) || gitErr.Args[0] != "fetch" {
			t.Errorf("%s: err = %v, want a fetch *gitstore.Error", name, err)
		}
	}
}

func TestFetchReturnsOnlyRegularFiles(t *testing.T) {
	remote := gittest.Remote(t)
	work := gittest.Clone(t, remote)
	for _, args := range [][]string{
		{"rm", "--quiet", "README.md"},
		{"update-index", "--add", "--cacheinfo", "100755," + hashObject(t, work, "#!/bin/sh\n") + ",run.sh"},
		{"update-index", "--add", "--cacheinfo", "120000," + hashObject(t, work, "run.sh") + ",link"},
		{"update-index", "--add", "--cacheinfo", "160000," + gittest.Run(t, work, "rev-parse", "HEAD") + ",sub"},
		{"commit", "--quiet", "-m", "special"},
		{"push", "--quiet", "origin", "HEAD:refs/heads/cache"},
	} {
		gittest.Run(t, work, args...)
	}

	_, files := fetch(t, open(t, gittest.Clone(t, remote)), "cache")
	if !reflect.DeepEqual(files, map[string][]byte{"run.sh": []byte("#!/bin/sh\n")}) {
		t.Errorf("files = %q", files)
	}
}

func hashObject(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return gittest.Run(t, dir, "hash-object", "-w", path)
}

func manyFiles(n int) map[string][]byte {
	files := map[string][]byte{}
	for i := range n {
		files[fmt.Sprintf("PLAT/PLAT-%d.json", i)] = []byte(fmt.Sprintf("{\"n\": %d}\n", i))
	}
	return files
}

func looseObjects(t *testing.T, dir string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(gittest.Run(t, dir, "count-objects"), "%d objects", &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSecondFetchTransfersOnlyChangedObjects(t *testing.T) {
	remote := gittest.Remote(t)
	writer := open(t, gittest.Clone(t, remote))
	files := manyFiles(300)
	first := publish(t, writer, "cache", "", files)

	reader := open(t, gittest.Clone(t, remote))
	gittest.Run(t, reader.Dir, "config", "fetch.unpackLimit", "1000000")
	fetch(t, reader, "cache")
	before := looseObjects(t, reader.Dir)

	files["PLAT/PLAT-7.json"] = []byte("changed\n")
	publish(t, writer, "cache", first, files)
	_, got := fetch(t, reader, "cache")
	added := looseObjects(t, reader.Dir) - before

	if !reflect.DeepEqual(got, files) {
		t.Error("second fetch returned the wrong files")
	}
	if added > 4 {
		t.Errorf("second fetch added %d objects, want at most 4 (commit, two trees, one blob)", added)
	}
}

func TestFetchSkipsHistoryBehindTip(t *testing.T) {
	remote := gittest.Remote(t)
	gittest.Seed(t, remote, "cache", manyFiles(3))
	work := gittest.Clone(t, remote, "--branch", "cache")
	gittest.Run(t, work, "commit", "--quiet", "--allow-empty", "-m", "tip")
	gittest.Run(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/cache")
	repo := open(t, gittest.Clone(t, remote, "--single-branch"))

	fetch(t, repo, "cache")
	if got := gittest.Run(t, repo.Dir, "rev-list", "--count", previous); got != "1" {
		t.Errorf("fetched %s commits, want 1", got)
	}
}

func countFetches(t *testing.T, trace string) int {
	t.Helper()
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), `"name":"upload-pack"`)
}

func TestFetchFromBloblessCloneBatchesMissingBlobs(t *testing.T) {
	remote := gittest.Remote(t)
	files := manyFiles(10)
	commit := gittest.Seed(t, remote, "cache", files)
	repo := open(t, gittest.Clone(t, remote, "--filter=blob:none"))
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("GIT_TRACE2_EVENT", trace)

	gotCommit, got := fetch(t, repo, "cache")
	if gotCommit != commit || !reflect.DeepEqual(got, files) {
		t.Errorf("fetched %s %q, want %s %q", gotCommit, got, commit, files)
	}
	if n := countFetches(t, trace); n != 2 {
		t.Errorf("ran %d fetches, want 2", n)
	}
}

func TestFetchFromFullCloneRunsOneFetch(t *testing.T) {
	remote := gittest.Remote(t)
	gittest.Seed(t, remote, "cache", manyFiles(3))
	gittest.Seed(t, remote, "other", nil)
	repo := open(t, gittest.Clone(t, remote))
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("GIT_TRACE2_EVENT", trace)

	fetch(t, repo, "cache")
	if n := countFetches(t, trace); n != 1 {
		t.Errorf("ran %d fetches, want 1", n)
	}
}

func TestNotIgnored(t *testing.T) {
	clone := gittest.Clone(t, gittest.Remote(t))
	if err := os.WriteFile(filepath.Join(clone, ".gitignore"), []byte("*.log\nREADME.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := open(t, clone)
	got, err := repo.NotIgnored(context.Background(), []string{"a.log", "b.txt", "README.md", "dir/c.log", "dir/d"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"b.txt", "README.md", "dir/d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("NotIgnored = %q, want %q", got, want)
	}
}

func TestNotIgnoredWhenNoneIgnored(t *testing.T) {
	repo := open(t, gittest.Clone(t, gittest.Remote(t)))
	paths := []string{"a", "b/c"}
	got, err := repo.NotIgnored(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, paths) {
		t.Errorf("NotIgnored = %q, want %q", got, paths)
	}
}

func TestNotIgnoredPathOutsideRepoFails(t *testing.T) {
	repo := open(t, gittest.Clone(t, gittest.Remote(t)))
	_, err := repo.NotIgnored(context.Background(), []string{"../outside"})
	var gitErr *gitstore.Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("err = %v, want *gitstore.Error", err)
	}
}
