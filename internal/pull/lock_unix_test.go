//go:build unix

package pull_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/pull"
)

func (f *fixture) holdLock(t *testing.T, how int) *os.File {
	t.Helper()
	lock, err := os.OpenFile(filepath.Join(f.dir, ".jira-git-cache", ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), how); err != nil { //nolint:gosec // File descriptors fit in an int.
		t.Fatal(err)
	}
	return lock
}

func TestPullWaitsForOtherLockHolders(t *testing.T) {
	for name, how := range map[string]int{"exclusive": syscall.LOCK_EX, "shared": syscall.LOCK_SH} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, defaultMain())
			f.seed(t, synced(&fetchedAt, map[string]int{"PLAT": 1}, "PLAT-1"))
			lock := f.holdLock(t, how)
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
		})
	}
}

func TestPullStopsWaitingWhenCancelled(t *testing.T) {
	f := newFixture(t, defaultMain())
	f.seed(t, synced(&fetchedAt, map[string]int{"PLAT": 1}, "PLAT-1"))
	lock := f.holdLock(t, syscall.LOCK_EX)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- pull.Run(ctx, f.opts) }()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pull still waiting after cancel")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	f.mustRun(t)
}
