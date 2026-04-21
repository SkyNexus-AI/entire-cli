package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/session"
)

const pendingReviewMarkerFilename = "review-pending.json"

// PendingReviewMarker is written by `entire review` before spawning the agent.
// The next agent session's UserPromptSubmit hook reads it, tags the session
// kind/review-skills, then clears the marker (so a second review run doesn't
// inherit state from the first).
type PendingReviewMarker struct {
	AgentName   string    `json:"agent_name"`
	Skills      []string  `json:"skills"`
	StartingSHA string    `json:"starting_sha"`
	StartedAt   time.Time `json:"started_at"`
}

func pendingMarkerPath() (string, error) {
	commonDir, err := session.GetGitCommonDir(context.Background())
	if err != nil {
		return "", fmt.Errorf("locate git common dir: %w", err)
	}
	return filepath.Join(commonDir, session.SessionStateDirName, pendingReviewMarkerFilename), nil
}

// WritePendingReviewMarker persists the marker. Overwrites any existing marker
// — callers detect concurrent reviews via ReadPendingReviewMarker before this.
func WritePendingReviewMarker(m PendingReviewMarker) error {
	path, err := pendingMarkerPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal marker: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}
	return nil
}

// ReadPendingReviewMarker returns the marker if one exists.
// ok=false with err=nil indicates "no pending review."
func ReadPendingReviewMarker() (PendingReviewMarker, bool, error) {
	path, err := pendingMarkerPath()
	if err != nil {
		return PendingReviewMarker{}, false, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path derived from git dir
	if errors.Is(err, os.ErrNotExist) {
		return PendingReviewMarker{}, false, nil
	}
	if err != nil {
		return PendingReviewMarker{}, false, fmt.Errorf("read marker: %w", err)
	}
	var m PendingReviewMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return PendingReviewMarker{}, false, fmt.Errorf("parse marker: %w", err)
	}
	return m, true, nil
}

// ClearPendingReviewMarker removes the marker. Missing file is not an error.
func ClearPendingReviewMarker() error {
	path, err := pendingMarkerPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove marker: %w", err)
	}
	return nil
}

// adoptPendingReviewMarkerInto reads any pending review marker and applies it
// to the given session state. Returns (newState, modified, error). If the
// state already has Kind set (e.g., a subsequent turn of a review session),
// the marker is left in place and modified=false — adoption only happens on
// first tag. The marker is cleared on successful first adoption.
func adoptPendingReviewMarkerInto(ctx context.Context, s session.State) (session.State, bool, error) {
	// Already tagged — don't re-apply on subsequent turns.
	if s.Kind != "" {
		return s, false, nil
	}
	m, ok, err := ReadPendingReviewMarker()
	if err != nil {
		return s, false, err
	}
	if !ok {
		return s, false, nil
	}
	s.Kind = session.KindReview
	s.ReviewStatus = session.ReviewStatusInProgress
	s.ReviewSkills = m.Skills
	if err := ClearPendingReviewMarker(); err != nil {
		// Tagging succeeded; leftover marker self-heals on next session start
		// (since Kind is now set, the next turn will return modified=false
		// and the marker will be re-cleared on any next review session).
		logging.Warn(ctx, "failed to clear pending review marker", slog.String("error", err.Error()))
	}
	return s, true, nil
}
