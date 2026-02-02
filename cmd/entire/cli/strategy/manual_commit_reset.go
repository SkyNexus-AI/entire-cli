package strategy

import (
	"fmt"
	"os"

	"entire.io/cli/cmd/entire/cli/paths"

	"github.com/charmbracelet/huh"
	"github.com/go-git/go-git/v5/plumbing"
)

// isAccessibleMode returns true if accessibility mode should be enabled.
// This checks the ACCESSIBLE environment variable.
func isAccessibleMode() bool {
	return os.Getenv("ACCESSIBLE") != ""
}

// Reset deletes the shadow branch and session state for the current HEAD.
// This allows starting fresh without existing checkpoints.
func (s *ManualCommitStrategy) Reset(force bool) error {
	repo, err := OpenRepository()
	if err != nil {
		return fmt.Errorf("failed to open git repository: %w", err)
	}

	// Get current HEAD
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("failed to get HEAD: %w", err)
	}

	// Get current worktree ID for shadow branch naming
	worktreePath, err := GetWorktreePath()
	if err != nil {
		return fmt.Errorf("failed to get worktree path: %w", err)
	}
	worktreeID, err := paths.GetWorktreeID(worktreePath)
	if err != nil {
		return fmt.Errorf("failed to get worktree ID: %w", err)
	}

	// Get shadow branch name for current HEAD
	shadowBranchName := getShadowBranchNameForCommit(head.Hash().String(), worktreeID)

	// Check if shadow branch exists
	refName := plumbing.NewBranchReferenceName(shadowBranchName)
	ref, err := repo.Reference(refName, true)
	hasShadowBranch := err == nil

	// Find sessions for this commit
	sessions, err := s.findSessionsForCommit(head.Hash().String())
	if err != nil {
		sessions = nil // Ignore error, treat as no sessions
	}

	// If nothing to reset, return early
	if !hasShadowBranch && len(sessions) == 0 {
		fmt.Fprintf(os.Stderr, "Nothing to reset for %s\n", shadowBranchName)
		return nil
	}

	// Confirm before deleting
	if !force {
		confirmed := false
		description := "This will delete:\n"
		if len(sessions) > 0 {
			description += fmt.Sprintf("  - %d session state file(s)\n", len(sessions))
		}
		if hasShadowBranch {
			description += fmt.Sprintf("  - Shadow branch %s\n", shadowBranchName)
		}
		description += "\nThis action cannot be undone."

		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Reset session data?").
					Description(description).
					Affirmative("Delete").
					Negative("Cancel").
					Value(&confirmed),
			),
		)
		if isAccessibleMode() {
			form = form.WithAccessible(true)
		}
		if err := form.Run(); err != nil {
			return fmt.Errorf("confirmation failed: %w", err)
		}
		if !confirmed {
			fmt.Fprintf(os.Stderr, "Cancelled\n")
			return nil
		}
	}

	// Clear all sessions for this commit
	clearedSessions := make([]string, 0)
	for _, state := range sessions {
		if err := s.clearSessionState(state.SessionID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to clear session state for %s: %v\n", state.SessionID, err)
		} else {
			clearedSessions = append(clearedSessions, state.SessionID)
		}
	}

	// Report cleared session states with session IDs
	if len(clearedSessions) > 0 {
		for _, sessionID := range clearedSessions {
			fmt.Fprintf(os.Stderr, "Cleared session state for %s\n", sessionID)
		}
	}

	// Delete the shadow branch if it exists
	if hasShadowBranch {
		if err := repo.Storer.RemoveReference(ref.Name()); err != nil {
			return fmt.Errorf("failed to delete shadow branch: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Deleted shadow branch %s\n", shadowBranchName)
	}

	return nil
}
