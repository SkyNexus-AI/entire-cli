package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/validation"
)

// Session state management functions shared across all strategies.
// SessionState is stored in .git/entire-sessions/{session_id}.json

// getSessionStateDir returns the path to the session state directory.
// This is stored in the git common dir so it's shared across all worktrees.
func getSessionStateDir(ctx context.Context) (string, error) {
	commonDir, err := GetGitCommonDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(commonDir, session.SessionStateDirName), nil
}

// sessionStateFile returns the path to a session state file.
func sessionStateFile(ctx context.Context, sessionID string) (string, error) {
	stateDir, err := getSessionStateDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, sessionID+".json"), nil
}

// LoadSessionState loads the session state for the given session ID.
// Returns (nil, nil) when session file doesn't exist or session is stale (not an error condition).
// Stale sessions are automatically deleted by the underlying StateStore.
func LoadSessionState(ctx context.Context, sessionID string) (*SessionState, error) {
	store, err := session.NewStateStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create state store: %w", err)
	}

	state, err := store.Load(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to load session state: %w", err)
	}
	return state, nil
}

// SaveSessionState saves the session state atomically.
func SaveSessionState(ctx context.Context, state *SessionState) error {
	// Validate session ID to prevent path traversal
	if err := validation.ValidateSessionID(state.SessionID); err != nil {
		return fmt.Errorf("invalid session ID: %w", err)
	}

	stateDir, err := getSessionStateDir(ctx)
	if err != nil {
		return fmt.Errorf("failed to get session state directory: %w", err)
	}

	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return fmt.Errorf("failed to create session state directory: %w", err)
	}

	data, err := jsonutil.MarshalIndentWithNewline(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session state: %w", err)
	}

	stateFile, err := sessionStateFile(ctx, state.SessionID)
	if err != nil {
		return fmt.Errorf("failed to get session state file path: %w", err)
	}

	// Atomic write: write to temp file, then rename
	tmpFile := stateFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write session state: %w", err)
	}
	if err := os.Rename(tmpFile, stateFile); err != nil {
		return fmt.Errorf("failed to rename session state file: %w", err)
	}
	return nil
}

// ListSessionStates returns all session states from the state directory.
// This is a package-level function that doesn't require a specific strategy instance.
func ListSessionStates(ctx context.Context) ([]*SessionState, error) {
	store, err := session.NewStateStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create state store: %w", err)
	}

	states, err := store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list session states: %w", err)
	}
	return states, nil
}

// FindMostRecentSession returns the session ID of the most recently interacted session
// (by LastInteractionTime) in the current worktree. Returns empty string if no sessions exist.
// Scoping to the current worktree prevents cross-worktree pollution in log routing.
// Falls back to unfiltered search if the worktree path can't be determined.
func FindMostRecentSession(ctx context.Context) string {
	states, err := ListSessionStates(ctx)
	if err != nil || len(states) == 0 {
		return ""
	}

	// Scope to current worktree to prevent cross-worktree pollution.
	worktreePath, wpErr := paths.WorktreeRoot(ctx)
	if wpErr == nil && worktreePath != "" {
		var filtered []*SessionState
		for _, s := range states {
			if s.WorktreePath == worktreePath {
				filtered = append(filtered, s)
			}
		}
		if len(filtered) > 0 {
			states = filtered
		}
		// If no sessions match the worktree, fall back to all sessions
	}

	var best *SessionState
	for _, s := range states {
		if s.LastInteractionTime == nil {
			continue
		}
		if best == nil || s.LastInteractionTime.After(*best.LastInteractionTime) {
			best = s
		}
	}
	if best != nil {
		return best.SessionID
	}

	// Fallback: return most recently started session
	for _, s := range states {
		if best == nil || s.StartedAt.After(best.StartedAt) {
			best = s
		}
	}
	if best != nil {
		return best.SessionID
	}
	return ""
}

// TransitionAndLog runs a session phase transition, applies actions via the
// handler, and logs the transition. Returns the first handler error from
// ApplyTransition (if any) so callers can surface it. The error is also
// logged internally for diagnostics.
// This is the single entry point for all state machine transitions to ensure
// consistent logging of phase changes.
func TransitionAndLog(goCtx context.Context, state *SessionState, event session.Event, ctx session.TransitionContext, handler session.ActionHandler) error {
	oldPhase := state.Phase
	result := session.Transition(oldPhase, event, ctx)
	logCtx := logging.WithComponent(goCtx, "session")

	handlerErr := session.ApplyTransition(goCtx, state, result, handler)
	if handlerErr != nil {
		logging.Error(logCtx, "action handler error during transition",
			slog.String("session_id", state.SessionID),
			slog.String("event", event.String()),
			slog.Any("error", handlerErr),
		)
	}

	if result.NewPhase != oldPhase {
		logging.Info(logCtx, "phase transition",
			slog.String("session_id", state.SessionID),
			slog.String("event", event.String()),
			slog.String("from", string(oldPhase)),
			slog.String("to", string(result.NewPhase)),
		)
	} else {
		logging.Debug(logCtx, "phase unchanged",
			slog.String("session_id", state.SessionID),
			slog.String("event", event.String()),
			slog.String("phase", string(result.NewPhase)),
			slog.Any("result", result),
		)
	}

	if handlerErr != nil {
		return fmt.Errorf("transition %s: %w", event, handlerErr)
	}
	return nil
}

// ClearSessionState removes the session state file for the given session ID.
func ClearSessionState(ctx context.Context, sessionID string) error {
	// Validate session ID to prevent path traversal
	if err := validation.ValidateSessionID(sessionID); err != nil {
		return fmt.Errorf("invalid session ID: %w", err)
	}

	stateFile, err := sessionStateFile(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get session state file path: %w", err)
	}

	if err := os.Remove(stateFile); err != nil {
		if os.IsNotExist(err) {
			return nil // Already gone, not an error
		}
		return fmt.Errorf("failed to remove session state file: %w", err)
	}
	return nil
}
