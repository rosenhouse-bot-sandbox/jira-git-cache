// Package gittest builds hermetic git repositories for tests.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Isolate points git at an empty config and a temporary HOME, and fixes the
// author and committer. Call it from TestMain and run the returned cleanup.
func Isolate() (cleanup func()) {
	home, err := os.MkdirTemp("", "gittest-home-")
	if err != nil {
		panic(err)
	}
	env := map[string]string{
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"HOME":                home,
		"GIT_AUTHOR_NAME":     "Test Author",
		"GIT_AUTHOR_EMAIL":    "author@example.com",
		"GIT_COMMITTER_NAME":  "Test Committer",
		"GIT_COMMITTER_EMAIL": "committer@example.com",
	}
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	return func() { _ = os.RemoveAll(home) }
}

// Run runs git in dir and returns its trimmed stdout. It fails t on error.
func Run(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// Remote creates a bare repository whose main branch holds one commit with a
// README. It allows filtered fetches.
func Remote(t testing.TB) (dir string) {
	t.Helper()
	dir = t.TempDir()
	Run(t, dir, "init", "--bare", "--initial-branch=main")
	Run(t, dir, "config", "uploadpack.allowFilter", "true")
	Seed(t, dir, "main", map[string][]byte{"README.md": []byte("# test\n")})
	return dir
}

// Clone clones remoteDir over file:// with extra clone args.
func Clone(t testing.TB, remoteDir string, args ...string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	Run(t, dir, append(append([]string{"clone", "--quiet"}, args...), "file://"+remoteDir, ".")...)
	return dir
}

// Seed pushes a parentless commit holding files to branch on remoteDir.
func Seed(t testing.TB, remoteDir, branch string, files map[string][]byte) (commit string) {
	t.Helper()
	work := t.TempDir()
	Run(t, work, "init", "--quiet")
	for name, data := range files {
		path := filepath.Join(work, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	Run(t, work, "add", "--all")
	Run(t, work, "commit", "--quiet", "--allow-empty", "-m", "seed")
	Run(t, work, "push", "--quiet", "--force", "file://"+remoteDir, "HEAD:refs/heads/"+branch)
	return Run(t, work, "rev-parse", "HEAD")
}
