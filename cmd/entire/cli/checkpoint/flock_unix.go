//go:build unix

package checkpoint

import (
	"fmt"
	"os"
	"syscall"
)

// acquireShadowFlock takes an exclusive POSIX advisory lock on path. The
// returned release closes the file (which drops the flock). Callers must call
// release exactly once. The lock file persists between runs — flock state is
// held by the file descriptor, not the inode on disk.
//
// Mirrors strategy.acquireStateFileLock; duplicated rather than imported to
// avoid pulling the strategy package into checkpoint.
func acquireShadowFlock(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // path built from validated branch name
	if err != nil {
		return nil, fmt.Errorf("open shadow lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // file descriptors are non-negative; standard Go pattern for syscall.Flock
		_ = f.Close()
		return nil, fmt.Errorf("flock shadow lock: %w", err)
	}
	return func() { _ = f.Close() }, nil
}
