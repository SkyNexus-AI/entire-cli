// Package filelock provides a small cross-platform exclusive file lock.
//
// Used to serialize concurrent processes operating on the same per-session
// state — e.g., when Cursor IDE forwards a single user prompt to both its own
// hooks and Claude Code's, both `entire` hook processes race to initialize the
// session. Without serialization the second writer can clobber the first.
package filelock

// Lock is held until Release is called. Calling Release is mandatory; missing
// it leaves the lock held until the process exits and the OS reclaims the
// file descriptor.
type Lock struct {
	impl lockImpl
}

// Release drops the lock and closes the backing file descriptor.
// Safe to call multiple times — subsequent calls are no-ops.
func (l *Lock) Release() error {
	if l == nil || l.impl == nil {
		return nil
	}
	err := l.impl.release()
	l.impl = nil
	return err
}

// Acquire blocks until an exclusive lock on path is obtained. The path's
// parent directory must exist; the lock file itself is created if missing.
// The contents of the lock file are intentionally not used — only its
// existence as a kernel-tracked inode matters.
func Acquire(path string) (*Lock, error) {
	impl, err := acquireImpl(path)
	if err != nil {
		return nil, err
	}
	return &Lock{impl: impl}, nil
}

type lockImpl interface {
	release() error
}
