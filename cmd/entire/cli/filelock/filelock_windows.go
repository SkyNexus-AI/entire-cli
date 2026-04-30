//go:build windows

package filelock

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

type windowsLock struct {
	f *os.File
}

func (w *windowsLock) release() error {
	overlapped := &windows.Overlapped{}
	_ = windows.UnlockFileEx(windows.Handle(w.f.Fd()), 0, ^uint32(0), ^uint32(0), overlapped) //nolint:errcheck // Close below also releases
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("close lock file: %w", err)
	}
	return nil
}

//nolint:ireturn // intentional: cross-platform interface used only by filelock.Acquire
func acquireImpl(path string) (lockImpl, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // path is built from caller-validated session ID
	if err != nil {
		return nil, fmt.Errorf("open lock file %q: %w", path, err)
	}
	overlapped := &windows.Overlapped{}
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, ^uint32(0), ^uint32(0), overlapped); err != nil {
		_ = f.Close() //nolint:errcheck // best-effort cleanup on lock failure
		return nil, fmt.Errorf("LockFileEx %q: %w", path, err)
	}
	return &windowsLock{f: f}, nil
}
