//go:build windows

package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryLockExclusive(f *os.File) error {
	handle := windows.Handle(f.Fd())
	var ol windows.Overlapped
	// LOCKFILE_FAIL_IMMEDIATELY is the Windows equivalent of LOCK_NB.
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
