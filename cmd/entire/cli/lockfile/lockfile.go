// Package lockfile provides a tiny cross-platform file-lock primitive
// built on OS advisory locks (flock on Unix, LockFileEx on Windows).
// The lock is auto-released by the kernel when the holding process
// exits, so there is no stale-lock recovery problem.
package lockfile

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ErrLocked is returned by Acquire when another process holds the lock.
var ErrLocked = errors.New("lockfile already held")

// Lock represents an acquired exclusive lock on a path. Release exactly
// once when done; the OS will also release on process exit.
type Lock struct {
	f *os.File
}

// Acquire opens path with O_CREATE|O_RDWR mode 0600, attempts a
// non-blocking exclusive OS-level lock, and on success writes the
// current PID into the file (advisory diagnostic only). Returns
// ErrLocked if another process holds the lock; non-ErrLocked errors
// indicate I/O or permission failures and should be reported with
// context. The Go runtime sets FD_CLOEXEC on os.OpenFile, so the FD is
// not inherited by subprocesses.
func Acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // G304: lock path is supplied by the caller (trusted)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := tryLockExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := writePID(f); err != nil {
		_ = unlock(f) //nolint:errcheck // best-effort cleanup; the writePID failure is what propagates
		_ = f.Close()
		return nil, fmt.Errorf("write PID to lock file %s: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Release releases the OS lock and closes the underlying file handle.
// Safe to call exactly once; subsequent calls are no-ops.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	unlockErr := unlock(f)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	if closeErr != nil {
		return fmt.Errorf("close lock file: %w", closeErr)
	}
	return nil
}

// ReadHolderPID returns the PID written into the lock file, or 0 if the
// file is empty/unreadable/contains garbage. Best-effort; for diagnostic
// messages only.
func ReadHolderPID(path string) int {
	data, err := os.ReadFile(path) //nolint:gosec // G304: lock path is supplied by the caller (trusted)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// writePID truncates the lock file and writes the current PID. The lock
// must already be held when this is called — callers in this package
// invoke it only after a successful tryLockExclusive.
func writePID(f *os.File) error {
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		return fmt.Errorf("write PID: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	return nil
}
