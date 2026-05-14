package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/session"

	"github.com/go-git/go-git/v6/plumbing"
)

// shadowBranchLockPath returns the per-shadow-branch flock file path. Lock
// files live in <git-common-dir>/entire-shadow-locks/ so they don't pollute
// the session-state directory. Branch names are slash-escaped because some
// filesystems disallow nested directory names that mirror the shadow branch
// hierarchy (entire/<hash>).
func shadowBranchLockPath(ctx context.Context, branchName string) (string, error) {
	commonDir, err := session.GetGitCommonDir(ctx)
	if err != nil {
		return "", fmt.Errorf("get git common dir: %w", err)
	}
	lockDir := filepath.Join(commonDir, "entire-shadow-locks")
	if err := os.MkdirAll(lockDir, 0o750); err != nil {
		return "", fmt.Errorf("create shadow lock directory: %w", err)
	}
	safe := strings.ReplaceAll(branchName, "/", "_")
	return filepath.Join(lockDir, safe+".lock"), nil
}

// withShadowBranchFlock acquires the per-shadow-branch flock, runs fn, and
// releases the flock. Serializes all WriteTemporary callers that target the
// same shadow branch — across goroutines AND across processes — so the CAS
// in casUpdateShadowBranchRef only sees external writers as contention.
func withShadowBranchFlock(ctx context.Context, branchName string, fn func() error) error {
	path, err := shadowBranchLockPath(ctx, branchName)
	if err != nil {
		return err
	}
	release, err := acquireShadowFlock(path)
	if err != nil {
		return fmt.Errorf("acquire shadow flock %s: %w", branchName, err)
	}
	defer release()
	return fn()
}

// ErrShadowRefBusy is returned by casUpdateShadowBranchRef when the ref has
// moved since the caller read it. Callers retry with a fresh parent.
var ErrShadowRefBusy = errors.New("shadow branch ref moved (CAS mismatch)")

// shadowRefMaxRetries bounds the WriteTemporary retry loop so a pathologically
// hot shadow branch can't hang a hook indefinitely. 16 is well above the
// number of concurrent sessions we've ever observed; the wins-required-to-
// finish distribution is bounded by N (number of concurrent writers).
const shadowRefMaxRetries = 16

// shadowRefMaxJitter is the upper bound for randomized backoff between CAS
// retries. Random jitter avoids thundering-herd retry patterns when many
// sessions hit the same shadow branch simultaneously.
const shadowRefMaxJitter = 8 * time.Millisecond

// zeroOID is the all-zeros object id that git accepts as the <oldvalue>
// argument to `git update-ref` to mean "must not exist".
const zeroOID = "0000000000000000000000000000000000000000"

// casUpdateShadowBranchRef atomically updates a shadow branch ref via
// `git update-ref <ref> <new> <old>`. Pass plumbing.ZeroHash as expectedHash
// to require the ref to NOT exist (first-checkpoint case).
//
// Returns ErrShadowRefBusy when git reports the ref moved since expectedHash
// was observed; callers retry with a fresh parent. Any other failure is
// returned wrapped.
//
// Why shell out: git's ref-locking is the canonical cross-process atomic
// CAS — go-git's CheckAndSetReference doesn't interoperate with native git's
// .lock files, and shadow branches can be touched concurrently by separate
// `entire` hook processes.
func casUpdateShadowBranchRef(ctx context.Context, branchName string, newHash, expectedHash plumbing.Hash) error {
	refName := "refs/heads/" + branchName

	oldValue := zeroOID
	if expectedHash != plumbing.ZeroHash {
		oldValue = expectedHash.String()
	}

	cmd := exec.CommandContext(ctx, "git", "update-ref", refName, newHash.String(), oldValue)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	out := string(output)
	// Git's CAS-failure messages: "cannot lock ref ..." (covers both
	// "is at X but expected Y" and "reference already exists" for the
	// zero-OID case). Other failures propagate.
	if strings.Contains(out, "cannot lock ref") || strings.Contains(out, "but expected") {
		return ErrShadowRefBusy
	}
	return fmt.Errorf("git update-ref %s: %s: %w", refName, strings.TrimSpace(out), err)
}

// shadowRefBackoff sleeps for a small random jitter before the next CAS
// retry. After several retries the jitter doubles to slow the thundering
// herd further. Respects context cancellation.
func shadowRefBackoff(ctx context.Context, attempt int) error {
	d := time.Duration(rand.Int64N(int64(shadowRefMaxJitter))) //nolint:gosec // jitter, not security-sensitive
	if attempt > 4 {
		d *= 2
	}
	if d <= 0 {
		d = time.Millisecond
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // canonical context cancellation
	}
}
