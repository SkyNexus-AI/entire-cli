//go:build windows

package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockExclusive returns ErrLocked on contention
// (ERROR_LOCK_VIOLATION), nil on success, other errors on I/O failures.
// LOCKFILE_FAIL_IMMEDIATELY makes the call non-blocking, matching the
// LOCK_NB semantics used on Unix.
func tryLockExclusive(f *os.File) error {
	handle := windows.Handle(f.Fd())
	var ol windows.Overlapped
	err := windows.LockFileEx(
		handle,
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		^uint32(0), ^uint32(0), // lock the entire file (max DWORD low/high)
		&ol,
	)
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrLocked
	}
	return err
}

func unlock(f *os.File) error {
	handle := windows.Handle(f.Fd())
	var ol windows.Overlapped
	return windows.UnlockFileEx(handle, 0, ^uint32(0), ^uint32(0), &ol)
}
