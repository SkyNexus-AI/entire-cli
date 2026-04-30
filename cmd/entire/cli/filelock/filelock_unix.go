//go:build !windows

package filelock

import (
	"fmt"
	"os"
	"syscall"
)

type unixLock struct {
	f *os.File
}

func (u *unixLock) release() error {
	// Flock releases when the fd is closed. Closing alone is sufficient,
	// but call LOCK_UN explicitly so the lock drops before any deferred
	// fsyncs on close.
	_ = syscall.Flock(int(u.f.Fd()), syscall.LOCK_UN) //nolint:errcheck,gosec // Fd() is a small descriptor; Close below also releases
	if err := u.f.Close(); err != nil {
		return fmt.Errorf("close lock file: %w", err)
	}
	return nil
}

func acquireImpl(path string) (lockImpl, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // path is built from caller-validated session ID
	if err != nil {
		return nil, fmt.Errorf("open lock file %q: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // Fd() is a small descriptor
		_ = f.Close()
		return nil, fmt.Errorf("flock %q: %w", path, err)
	}
	return &unixLock{f: f}, nil
}
