package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/settings"
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

// curatedSkill represents a known review skill/command surfaced by the
// first-run picker. Users can add custom skills by editing
// .entire/settings.json directly.
type curatedSkill struct { //nolint:unused // wired up in a subsequent chunk
	Name string
	Desc string
}

// curatedReviewSkills groups known review skills by agent name (as a string
// matching types.AgentName values). Agents not listed here still work via
// the picker — users just see an empty list and should edit settings.json
// manually to add skills.
var curatedReviewSkills = map[string][]curatedSkill{ //nolint:unused // wired up in a subsequent chunk
	"claude-code": {
		{Name: "/pr-review-toolkit:review-pr", Desc: "Full PR review"},
		{Name: "/pr-review-toolkit:code-reviewer", Desc: "Code review for standards"},
		{Name: "/test-auditor", Desc: "Test coverage audit"},
		{Name: "/verification-before-completion", Desc: "Verify before marking done"},
		{Name: "/requesting-code-review", Desc: "Prepare code for review"},
		{Name: "/pr-review-toolkit:silent-failure-hunter", Desc: "Find suppressed errors"},
	},
	"codex": {
		{Name: "/codex:review", Desc: "Codex review"},
		{Name: "/codex:adversarial-review", Desc: "Adversarial review — red-team"},
	},
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

// runReviewConfigPicker presents a huh multi-select for each installed agent
// that has curated review skills, and saves the selection to
// .entire/settings.json. Returns the picked configuration so callers can
// proceed immediately without re-reading from disk.
//
//nolint:unused // wired up in a subsequent chunk
func runReviewConfigPicker(ctx context.Context, out io.Writer) (map[string][]string, error) {
	installed := GetAgentsWithHooksInstalled(ctx)
	if len(installed) == 0 {
		return nil, errors.New("no agents installed; run 'entire enable' first")
	}
	selected := map[string][]string{}
	for _, agentName := range installed {
		curated, ok := curatedReviewSkills[string(agentName)]
		if !ok || len(curated) == 0 {
			// Agent has no curated list; user must edit JSON manually.
			continue
		}
		var picks []string
		options := make([]huh.Option[string], 0, len(curated))
		for _, s := range curated {
			options = append(options, huh.NewOption(fmt.Sprintf("%s — %s", s.Name, s.Desc), s.Name))
		}
		form := NewAccessibleForm(huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(fmt.Sprintf("Pick review skills for %s", agentName)).
				Options(options...).
				Value(&picks),
		))
		if err := form.Run(); err != nil {
			return nil, fmt.Errorf("picker for %s: %w", agentName, err)
		}
		if len(picks) > 0 {
			selected[string(agentName)] = picks
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("no review skills selected")
	}
	if err := saveReviewConfig(ctx, selected); err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "Saved review config to .entire/settings.json. Edit directly or run `entire review --edit`.")
	return selected, nil
}

// saveReviewConfig persists the review map into .entire/settings.json while
// preserving all other settings.
func saveReviewConfig(ctx context.Context, review map[string][]string) error {
	s, err := settings.Load(ctx)
	if err != nil || s == nil {
		s = &settings.EntireSettings{}
	}
	s.Review = review
	if err := settings.Save(ctx, s); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}
