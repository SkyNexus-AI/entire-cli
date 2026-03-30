package strategy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
)

// pushRefIfNeeded pushes a custom ref to the given target if it exists locally.
// Custom refs (under refs/entire/) don't have remote-tracking refs, so there's
// no "has unpushed" optimization — we always attempt the push and let git handle
// the no-op case.
func pushRefIfNeeded(ctx context.Context, target string, refName plumbing.ReferenceName) error {
	repo, err := OpenRepository(ctx)
	if err != nil {
		return nil //nolint:nilerr // Hook must be silent on failure
	}

	if _, err := repo.Reference(refName, true); err != nil {
		return nil //nolint:nilerr // Ref doesn't exist locally, nothing to push
	}

	return doPushRef(ctx, target, refName)
}

// tryPushRef attempts to push a custom ref using an explicit refspec.
func tryPushRef(ctx context.Context, target string, refName plumbing.ReferenceName) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Use --no-verify to prevent recursive hook calls (this runs inside pre-push)
	refSpec := fmt.Sprintf("%s:%s", refName, refName)
	cmd := exec.CommandContext(ctx, "git", "push", "--no-verify", target, refSpec)
	cmd.Stdin = nil // Disconnect stdin to prevent hanging in hook context

	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "non-fast-forward") ||
			strings.Contains(string(output), "rejected") {
			return errors.New("non-fast-forward")
		}
		return fmt.Errorf("push failed: %s", output)
	}
	return nil
}

// doPushRef pushes a custom ref with fetch+merge recovery on conflict.
func doPushRef(ctx context.Context, target string, refName plumbing.ReferenceName) error {
	displayTarget := target
	if isURL(target) {
		displayTarget = "checkpoint remote"
	}

	shortRef := shortRefName(refName)
	fmt.Fprintf(os.Stderr, "[entire] Pushing %s to %s...", shortRef, displayTarget)
	stop := startProgressDots(os.Stderr)

	if err := tryPushRef(ctx, target, refName); err == nil {
		stop(" done")
		return nil
	}
	stop("")

	fmt.Fprintf(os.Stderr, "[entire] Syncing %s with remote...", shortRef)
	stop = startProgressDots(os.Stderr)

	if err := fetchAndMergeRef(ctx, target, refName); err != nil {
		stop("")
		fmt.Fprintf(os.Stderr, "[entire] Warning: couldn't sync %s: %v\n", shortRef, err)
		printCheckpointRemoteHint(target)
		return nil
	}
	stop(" done")

	fmt.Fprintf(os.Stderr, "[entire] Pushing %s to %s...", shortRef, displayTarget)
	stop = startProgressDots(os.Stderr)

	if err := tryPushRef(ctx, target, refName); err != nil {
		stop("")
		fmt.Fprintf(os.Stderr, "[entire] Warning: failed to push %s after sync: %v\n", shortRef, err)
		printCheckpointRemoteHint(target)
	} else {
		stop(" done")
	}

	return nil
}

// fetchAndMergeRef fetches a remote custom ref and merges it into the local ref.
// Placeholder — implemented in Task 4.
func fetchAndMergeRef(_ context.Context, _ string, _ plumbing.ReferenceName) error {
	return fmt.Errorf("fetchAndMergeRef not yet implemented")
}

// shortRefName returns a human-readable short form of a ref name for log output.
// e.g., "refs/entire/checkpoints/v2/main" -> "v2/main"
func shortRefName(refName plumbing.ReferenceName) string {
	const prefix = "refs/entire/checkpoints/"
	s := string(refName)
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}
