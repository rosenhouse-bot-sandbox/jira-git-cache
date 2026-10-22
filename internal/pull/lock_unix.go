//go:build unix

package pull

import (
	"fmt"
	"os"
	"syscall"
)

// lock blocks until it holds an exclusive lock on path.
func lock(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // The lock lives in the repo's own cache directory.
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // File descriptors fit in an int.
		_ = f.Close()
		return nil, fmt.Errorf("lock: %w", err)
	}
	return func() { _ = f.Close() }, nil
}
