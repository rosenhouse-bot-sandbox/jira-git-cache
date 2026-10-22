//go:build unix

package pull_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPullWaitsForOtherPulls(t *testing.T) {
	f := newFixture(t, defaultMain())
	f.seed(t, synced(&fetchedAt, map[string]int{"PLAT": 1}, "PLAT-1"))
	lock, err := os.OpenFile(filepath.Join(f.dir, ".jira-git-cache", ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.run(t) }()

	select {
	case err := <-done:
		t.Fatalf("pull finished while locked: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
