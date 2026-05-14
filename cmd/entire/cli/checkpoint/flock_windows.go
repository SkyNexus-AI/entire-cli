//go:build windows

package checkpoint

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// acquireShadowFlock takes an exclusive lock on path via Windows LockFileEx.
// The returned release unlocks and closes the file. Callers must call release
// exactly once.
//
// Mirrors strategy.acquireStateFileLock; duplicated rather than imported to
// avoid pulling the strategy package into checkpoint.
func acquireShadowFlock(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // path built from validated branch name
	if err != nil {
		return nil, fmt.Errorf("open shadow lock: %w", err)
	}
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock shadow lock: %w", err)
	}
	return func() {
		_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlapped)
		_ = f.Close()
	}, nil
}
