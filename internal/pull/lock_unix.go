//go:build unix

package pull

import (
	"context"
	"fmt"
	"os"
	"syscall"
)

// lock waits until it holds an exclusive lock on path or ctx is done.
func lock(ctx context.Context, path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // The lock lives in the repo's own cache directory.
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	fd := int(f.Fd()) //nolint:gosec // File descriptors fit in an int.
	locked := make(chan error, 1)
	go func() { locked <- syscall.Flock(fd, syscall.LOCK_EX) }()
	select {
	case err := <-locked:
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("lock: %w", err)
		}
		return func() { _ = f.Close() }, nil
	case <-ctx.Done():
		go func() {
			<-locked
			_ = f.Close()
		}()
		return nil, fmt.Errorf("lock: %w", ctx.Err())
	}
}
