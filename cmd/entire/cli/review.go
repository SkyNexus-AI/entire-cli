package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/trailers"
	"github.com/spf13/cobra"
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
type curatedSkill struct {
	Name string
	Desc string
}

// curatedReviewSkills groups known review skills by agent name (as a string
// matching types.AgentName values). Agents not listed here still work via
// the picker — users just see an empty list and should edit settings.json
// manually to add skills.
var curatedReviewSkills = map[string][]curatedSkill{
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

func newReviewCmd() *cobra.Command {
	var edit bool
	var trackOnly bool
	var postreview bool
	var finalize string
	var sessionID string

	cmd := &cobra.Command{
		Use:   "review",
		Short: "Run configured review skills against the current branch",
		Long: `Run the review skills configured in .entire/settings.json against
the current branch. On first run, an interactive picker writes the config.

After the review session, choose to Fix findings in-session, Close with an
empty review-marker commit, or Skip without committing.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			switch {
			case postreview:
				return runPostReview(ctx, cmd, sessionID)
			case finalize != "":
				return runFinalize(ctx, cmd, finalize, sessionID)
			case edit:
				_, err := runReviewConfigPicker(ctx, cmd.OutOrStdout())
				return err
			default:
				return runReview(ctx, cmd, trackOnly)
			}
		},
	}
	cmd.Flags().BoolVar(&edit, "edit", false, "re-open the review config picker")
	cmd.Flags().BoolVar(&trackOnly, "track-only", false, "write pending marker without spawning agent")
	cmd.Flags().BoolVar(&postreview, "postreview", false, "hidden: print post-review options for finish skill")
	cmd.Flags().StringVar(&finalize, "finalize", "", "hidden: finalize decision (fix|close|skip)")
	cmd.Flags().StringVar(&sessionID, "session", "", "hidden: session id (used with --postreview/--finalize)")
	if err := cmd.Flags().MarkHidden("postreview"); err != nil {
		panic(err) // flag name is a compile-time constant; panic is appropriate
	}
	if err := cmd.Flags().MarkHidden("finalize"); err != nil {
		panic(err)
	}
	if err := cmd.Flags().MarkHidden("session"); err != nil {
		panic(err)
	}
	return cmd
}

func runReview(ctx context.Context, cmd *cobra.Command, trackOnly bool) error {
	out := cmd.OutOrStdout()

	// 1. Pre-flight: must be in a git repo.
	if _, err := paths.WorktreeRoot(ctx); err != nil {
		cmd.SilenceUsage = true
		fmt.Fprintln(cmd.ErrOrStderr(), "Not a git repository. Run `entire enable` first.")
		return NewSilentError(errors.New("not a git repository"))
	}

	// 2. Load config; trigger first-run picker if missing.
	s, err := settings.Load(ctx)
	if err != nil || s == nil || len(s.Review) == 0 {
		picked, pickErr := runReviewConfigPicker(ctx, out)
		if pickErr != nil {
			return pickErr
		}
		s = &settings.EntireSettings{Review: picked}
	}

	// 3. Pick agent.
	agentName, skills, err := selectReviewAgent(s.Review)
	if err != nil {
		return err
	}

	// 4. Re-run guard. (Real implementation lands in Chunk 8.)
	if reviewed, meta := branchReviewedAtHead(ctx); reviewed {
		if !confirmReRun(out, meta) {
			fmt.Fprintln(out, "Review cancelled.")
			return nil
		}
	}

	// 5. Resolve HEAD for the pending marker.
	headSHA, err := currentHeadSHA(ctx)
	if err != nil {
		return fmt.Errorf("resolve HEAD: %w", err)
	}

	// 6. Write pending marker (agent hook will adopt it).
	if err := WritePendingReviewMarker(PendingReviewMarker{
		AgentName:   agentName,
		Skills:      skills,
		StartingSHA: headSHA,
		StartedAt:   time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("write pending marker: %w", err)
	}

	if trackOnly {
		fmt.Fprintln(out, "Pending review marker written.")
		fmt.Fprintf(out, "Start %s and run these skills manually: %s\n", agentName, strings.Join(skills, ", "))
		fmt.Fprintln(out, "When done, run `/entire-review:finish` in the agent.")
		return nil
	}

	// 7. Spawn agent with the composed initial prompt.
	launcher, ok := agent.LauncherFor(types.AgentName(agentName))
	if !ok {
		fmt.Fprintf(out, "%s does not support subprocess launch yet. Falling back to --track-only.\n", agentName)
		fmt.Fprintf(out, "Start %s manually and run: %s, then /entire-review:finish\n", agentName, strings.Join(skills, ", "))
		return nil
	}
	prompt := composeReviewPrompt(skills)
	execCmd, err := launcher.LaunchCmd(ctx, prompt)
	if err != nil {
		return fmt.Errorf("launch %s: %w", agentName, err)
	}
	if err := execCmd.Run(); err != nil {
		return fmt.Errorf("agent exited: %w", err)
	}
	return nil
}

// selectReviewAgent picks an agent from the configured review map. v1: single
// agent. If multiple are configured, returns the one that sorts first by name
// (deterministic default). Returns an error if the map is empty.
func selectReviewAgent(review map[string][]string) (string, []string, error) {
	if len(review) == 0 {
		return "", nil, errors.New("no review skills configured")
	}
	// Deterministic pick: alphabetical by agent name.
	var names []string
	for name, skills := range review {
		if len(skills) > 0 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", nil, errors.New("no review skills configured")
	}
	sort.Strings(names)
	pick := names[0]
	return pick, review[pick], nil
}

// composeReviewPrompt builds the initial prompt the agent receives.
func composeReviewPrompt(skills []string) string {
	var sb strings.Builder
	sb.WriteString("Please run these review skills in order:\n")
	for i, skill := range skills {
		fmt.Fprintf(&sb, "  %d. %s\n", i+1, skill)
	}
	sb.WriteString("\nWhen all skills have completed, run /entire-review:finish to present options to the user.\n")
	return sb.String()
}

// currentHeadSHA returns the current HEAD commit hash as a 40-char hex string.
func currentHeadSHA(ctx context.Context) (string, error) {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return "", fmt.Errorf("locate repo root: %w", err)
	}
	execCmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "HEAD")
	output, err := execCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// branchReviewedAtHead checks if the current branch already has a review
// commit at HEAD. STUB: real implementation lands in Chunk 8.
// TODO(chunk-8): scan commit history via trailers.ParseReviewMetadata.
func branchReviewedAtHead(ctx context.Context) (bool, string) {
	_ = ctx
	return false, ""
}

// confirmReRun prompts the user to confirm re-running review when the branch
// was already reviewed at HEAD. Returns true if user wants to proceed.
func confirmReRun(out io.Writer, meta string) bool {
	var proceed bool
	form := NewAccessibleForm(huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Already reviewed: %s. Proceed anyway?", meta)).
			Value(&proceed),
	))
	if err := form.Run(); err != nil {
		fmt.Fprintln(out, "prompt cancelled")
		return false
	}
	return proceed
}

func runPostReview(ctx context.Context, cmd *cobra.Command, sessionID string) error {
	if sessionID == "" {
		return errors.New("--session required with --postreview")
	}
	store, err := session.NewStateStore(ctx)
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	state, err := store.Load(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	if state == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	if state.Kind != session.KindReview {
		return fmt.Errorf("session %s is not a review session", sessionID)
	}
	out := cmd.OutOrStdout()
	checkpointID := mostRecentCheckpointID(ctx, sessionID)
	if checkpointID != "" {
		fmt.Fprintf(out, "Review complete. Transcript: entire explain %s\n\n", checkpointID)
	} else {
		fmt.Fprintln(out, "Review complete.")
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "Ask the user to choose:")
	fmt.Fprintln(out, "  - Fix    → run: entire review --finalize fix --session "+sessionID)
	fmt.Fprintln(out, "  - Close  → run: entire review --finalize close --session "+sessionID)
	fmt.Fprintln(out, "  - Skip   → run: entire review --finalize skip --session "+sessionID)
	return nil
}

// mostRecentCheckpointID returns the most recent checkpoint ID for the given
// session, or "" if unavailable.
// TODO(chunk-8): resolve actual checkpoint ID from strategy.
func mostRecentCheckpointID(_ context.Context, _ string) string {
	return ""
}

func runFinalize(ctx context.Context, cmd *cobra.Command, decision, sessionID string) error {
	if sessionID == "" {
		return errors.New("--session required with --finalize")
	}
	switch decision {
	case "fix":
		return finalizeFix(ctx, cmd, sessionID)
	case "close":
		return finalizeClose(ctx, cmd, sessionID)
	case "skip":
		return finalizeSkip(ctx, cmd, sessionID)
	default:
		return fmt.Errorf("unknown finalize decision: %s (expected fix|close|skip)", decision)
	}
}

func finalizeFix(ctx context.Context, cmd *cobra.Command, sessionID string) error {
	state, err := loadReviewSession(ctx, sessionID)
	if err != nil {
		return err
	}
	_ = state // no state change on fix
	fmt.Fprintln(cmd.OutOrStdout(),
		"Continue addressing the findings above. Run `entire review` again after your fixes to verify.")
	return nil
}

// loadReviewSession is a small helper shared by the finalize handlers.
func loadReviewSession(ctx context.Context, sessionID string) (*session.State, error) {
	store, err := session.NewStateStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("open session store: %w", err)
	}
	state, err := store.Load(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	if state == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	if state.Kind != session.KindReview {
		return nil, fmt.Errorf("session %s is not a review session", sessionID)
	}
	return state, nil
}

func finalizeClose(ctx context.Context, cmd *cobra.Command, sessionID string) error {
	state, err := loadReviewSession(ctx, sessionID)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	checkpointID := mostRecentCheckpointID(ctx, sessionID)
	// Best-effort: author email used as review-by; omit if unavailable.
	by := ""
	if author, authErr := GetGitAuthor(ctx); authErr == nil && author != nil {
		by = author.Email
	}
	// TODO(v2): detect clean reviews; for now default to closed.
	status := trailers.ReviewStatusClosed
	md := trailers.ReviewMetadata{
		By:         by,
		Agent:      pickAgentNameFromSession(state),
		Skills:     state.ReviewSkills,
		Session:    sessionID,
		Checkpoint: checkpointID,
		Status:     status,
	}
	hash, err := createReviewCommit(ctx, md)
	if err != nil {
		return err
	}
	// Record close in session state.
	state.ReviewStatus = session.ReviewStatus(status)
	store, err := session.NewStateStore(ctx)
	if err != nil {
		return fmt.Errorf("reopen session store: %w", err)
	}
	if err := store.Save(ctx, state); err != nil {
		return fmt.Errorf("save session state: %w", err)
	}
	short := hash.String()
	if len(short) > 12 {
		short = short[:12]
	}
	fmt.Fprintf(out, "Review committed as %s. You may exit.\n", short)
	return nil
}

// createReviewCommit writes an empty "Review" commit on the current branch
// with review trailers. Reviewed-Up-To is set to HEAD (before the commit)
// so it doesn't point at the new commit itself.
func createReviewCommit(ctx context.Context, md trailers.ReviewMetadata) (plumbing.Hash, error) {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("locate repo root: %w", err)
	}
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("open repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("resolve HEAD: %w", err)
	}
	if !head.Name().IsBranch() {
		return plumbing.ZeroHash, errors.New("cannot create review commit: not on a branch (detached HEAD)")
	}
	parent, err := repo.CommitObject(head.Hash())
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("load parent commit: %w", err)
	}
	md.ReviewedUpTo = head.Hash().String()
	message := trailers.AppendReviewTrailers("Review\n", md)

	author, err := GetGitAuthor(ctx)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("resolve git author: %w", err)
	}
	now := time.Now()
	commit := &object.Commit{
		Author:       object.Signature{Name: author.Name, Email: author.Email, When: now},
		Committer:    object.Signature{Name: author.Name, Email: author.Email, When: now},
		Message:      message,
		TreeHash:     parent.TreeHash,
		ParentHashes: []plumbing.Hash{parent.Hash},
	}
	obj := repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("encode commit: %w", err)
	}
	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("store commit: %w", err)
	}
	// Advance the current branch to point at the new commit.
	ref := plumbing.NewHashReference(head.Name(), hash)
	if err := repo.Storer.SetReference(ref); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("update branch ref: %w", err)
	}
	return hash, nil
}

// pickAgentNameFromSession extracts an agent name hint from session state.
// v1: returns empty; agent name was recorded in the pending marker which is
// cleared at adoption time. Future work: persist agent name on the session.
func pickAgentNameFromSession(_ *session.State) string {
	return ""
}

func finalizeSkip(ctx context.Context, cmd *cobra.Command, sessionID string) error {
	state, err := loadReviewSession(ctx, sessionID)
	if err != nil {
		return err
	}
	state.ReviewStatus = session.ReviewStatusSkipped
	store, err := session.NewStateStore(ctx)
	if err != nil {
		return fmt.Errorf("reopen session store: %w", err)
	}
	if err := store.Save(ctx, state); err != nil {
		return fmt.Errorf("save session state: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Review ended without commit. You may exit.")
	return nil
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
