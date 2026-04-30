package filelock

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquire_SerializesGoroutines(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "test.lock")

	const goroutines = 8
	var (
		holders  atomic.Int32
		maxConc  atomic.Int32
		finished atomic.Int32
	)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock, err := Acquire(path)
			if err != nil {
				t.Errorf("Acquire() error = %v", err)
				return
			}
			defer func() {
				if rErr := lock.Release(); rErr != nil {
					t.Errorf("Release() error = %v", rErr)
				}
				finished.Add(1)
			}()

			// Track concurrent holders. Must always be 1 under exclusive lock.
			cur := holders.Add(1)
			if cur > maxConc.Load() {
				maxConc.Store(cur)
			}

			// Hold the lock briefly to give other goroutines a real chance to
			// race for it. Without this, Goroutine 1 might release before any
			// peer attempts Acquire, masking a broken lock.
			time.Sleep(5 * time.Millisecond)

			holders.Add(-1)
		}()
	}
	wg.Wait()

	if finished.Load() != goroutines {
		t.Fatalf("expected %d goroutines to finish, got %d", goroutines, finished.Load())
	}
	if got := maxConc.Load(); got != 1 {
		t.Errorf("max concurrent holders = %d, want 1 (lock is not exclusive)", got)
	}
}

func TestRelease_Idempotent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "test.lock")
	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first Release() error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("second Release() should be a no-op, got: %v", err)
	}
}

func TestRelease_NilLock_NoOp(t *testing.T) {
	t.Parallel()

	var lock *Lock
	if err := lock.Release(); err != nil {
		t.Errorf("Release() on nil lock should be a no-op, got: %v", err)
	}
}
