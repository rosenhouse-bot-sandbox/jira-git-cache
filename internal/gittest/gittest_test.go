package gittest_test

import (
	"os"
	"testing"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/gittest"
)

func TestIsolateClearsInheritedGitVariables(t *testing.T) {
	outer := t.TempDir()
	t.Setenv("GIT_DIR", outer)
	t.Setenv("GIT_INDEX_FILE", outer+"/index")
	t.Setenv("HOME", outer)
	cleanup := gittest.Isolate()
	defer cleanup()

	for _, name := range []string{"GIT_DIR", "GIT_INDEX_FILE"} {
		if value, ok := os.LookupEnv(name); ok {
			t.Errorf("%s = %q, want unset", name, value)
		}
	}
	if got := os.Getenv("GIT_CONFIG_GLOBAL"); got != os.DevNull {
		t.Errorf("GIT_CONFIG_GLOBAL = %q, want %q", got, os.DevNull)
	}
	if home := os.Getenv("HOME"); home == outer {
		t.Error("HOME is unchanged")
	}
}
