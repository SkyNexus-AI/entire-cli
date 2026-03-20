package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/geminicli"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	cpkg "github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/entireio/cli/cmd/entire/cli/trailers"
	"github.com/entireio/cli/cmd/entire/cli/validation"
	"github.com/entireio/cli/cmd/entire/cli/versioninfo"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

func newAttachCmd() *cobra.Command {
	var (
		force     bool
		agentFlag string
	)
	cmd := &cobra.Command{
		Use:   "attach <session-id>",
		Short: "Attach an existing agent session",
		Long: `Attach an existing agent session that wasn't captured by hooks.

This creates a checkpoint from the session's transcript and links it to the
last commit. Use this when hooks failed to fire or weren't installed when
the session started, or to attach a research session.

If the last commit already has a checkpoint, the session is added to it.
Otherwise a new checkpoint is created.

Supported agents: claude-code, gemini, opencode, cursor, copilot-cli, factoryai-droid`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cmd.Help()
			}
			if checkDisabledGuard(cmd.Context(), cmd.OutOrStdout()) {
				return nil
			}
			agentName := types.AgentName(agentFlag)
			return runAttach(cmd.Context(), cmd.OutOrStdout(), args[0], agentName, force)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation and amend the last commit with the checkpoint trailer")
	cmd.Flags().StringVarP(&agentFlag, "agent", "a", string(agent.DefaultAgentName), "Agent that created the session (claude-code, gemini, opencode, cursor, copilot-cli, factoryai-droid)")
	return cmd
}

func runAttach(ctx context.Context, w io.Writer, sessionID string, agentName types.AgentName, force bool) error {
	logCtx := logging.WithComponent(ctx, "attach")

	existingState, err := validateAttachPreconditions(ctx, sessionID)
	if err != nil {
		return err
	}

	// If session already has a checkpoint, just offer to link it.
	if existingState != nil && !existingState.LastCheckpointID.IsEmpty() {
		cpID := existingState.LastCheckpointID.String()
		fmt.Fprintf(w, "Session %s already has checkpoint %s\n", sessionID, cpID)
		if err := promptAmendCommit(logCtx, w, cpID, force); err != nil {
			logging.Warn(logCtx, "failed to amend commit", "error", err)
			fmt.Fprintf(w, "\nCopy to your commit message to attach:\n\n  Entire-Checkpoint: %s\n", cpID)
		}
		return nil
	}

	// Resolve agent — from existing state or flag + auto-detection.
	var ag agent.Agent
	if existingState != nil {
		ag, err = resolveAgentForState(existingState, agentName)
	} else {
		ag, _, err = resolveAgentAndTranscript(logCtx, w, sessionID, agentName)
	}
	if err != nil {
		return err
	}

	transcriptPath, err := resolveAndValidateTranscript(logCtx, sessionID, ag)
	if err != nil {
		return fmt.Errorf("transcript not found for session %s: %w", sessionID, err)
	}

	transcriptData, err := ag.ReadTranscript(transcriptPath)
	if err != nil {
		return fmt.Errorf("failed to read transcript: %w", err)
	}

	// Normalize Gemini transcripts for storage.
	storedTranscript := transcriptData
	if ag.Type() == agent.AgentTypeGemini {
		if normalized, normErr := geminicli.NormalizeTranscript(transcriptData); normErr == nil {
			storedTranscript = normalized
		} else {
			logging.Warn(logCtx, "failed to normalize Gemini transcript, storing raw", "error", normErr)
		}
	}

	meta := extractTranscriptMetadata(transcriptData)

	// Determine checkpoint ID: reuse from HEAD if one exists, otherwise generate new.
	checkpointID, isExistingCheckpoint, err := resolveCheckpointID(logCtx)
	if err != nil {
		return err
	}

	// Write directly to entire/checkpoints/v1.
	repo, err := openRepository(ctx)
	if err != nil {
		return err
	}
	store := cpkg.NewGitStore(repo)

	author, err := GetGitAuthor(ctx)
	if err != nil {
		return fmt.Errorf("failed to get git author: %w", err)
	}

	var prompts []string
	if meta.FirstPrompt != "" {
		prompts = []string{meta.FirstPrompt}
	}

	var tokenUsage *agent.TokenUsage
	if usage := agent.CalculateTokenUsage(logCtx, ag, transcriptData, 0, ""); usage != nil {
		tokenUsage = usage
	}

	if err := store.WriteCommitted(ctx, cpkg.WriteCommittedOptions{
		CheckpointID: checkpointID,
		SessionID:    sessionID,
		Strategy:     "manual-commit",
		Transcript:   storedTranscript,
		Prompts:      prompts,
		AuthorName:   author.Name,
		AuthorEmail:  author.Email,
		Agent:        ag.Type(),
		Model:        meta.Model,
		TokenUsage:   tokenUsage,
	}); err != nil {
		return fmt.Errorf("failed to write checkpoint: %w", err)
	}

	// Create or update session state.
	if err := saveAttachSessionState(logCtx, sessionID, ag.Type(), transcriptPath, checkpointID, meta, tokenUsage); err != nil {
		logging.Warn(logCtx, "failed to save session state", "error", err)
	}

	fmt.Fprintf(w, "Attached session %s\n", sessionID)
	if isExistingCheckpoint {
		fmt.Fprintf(w, "  Added to existing checkpoint %s\n", checkpointID)
		return nil
	}

	fmt.Fprintf(w, "  Created checkpoint %s\n", checkpointID)
	cpIDStr := checkpointID.String()
	if err := promptAmendCommit(logCtx, w, cpIDStr, force); err != nil {
		logging.Warn(logCtx, "failed to amend commit", "error", err)
		fmt.Fprintf(w, "\nCopy to your commit message to attach:\n\n  Entire-Checkpoint: %s\n", cpIDStr)
	}

	return nil
}

// resolveCheckpointID returns the checkpoint ID to use for the attach.
// If HEAD already has an Entire-Checkpoint trailer, reuses that ID (the session
// gets added as an additional session in the existing checkpoint).
// Otherwise generates a new ID.
func resolveCheckpointID(ctx context.Context) (id.CheckpointID, bool, error) {
	repo, err := openRepository(ctx)
	if err != nil {
		return id.EmptyCheckpointID, false, err
	}
	headRef, err := repo.Head()
	if err != nil {
		return id.EmptyCheckpointID, false, fmt.Errorf("failed to get HEAD: %w", err)
	}
	headCommit, err := repo.CommitObject(headRef.Hash())
	if err != nil {
		return id.EmptyCheckpointID, false, fmt.Errorf("failed to get HEAD commit: %w", err)
	}

	existing := trailers.ParseAllCheckpoints(headCommit.Message)
	if len(existing) > 0 {
		// Use the last checkpoint ID on HEAD.
		return existing[len(existing)-1], true, nil
	}

	cpID, err := id.Generate()
	if err != nil {
		return id.EmptyCheckpointID, false, fmt.Errorf("failed to generate checkpoint ID: %w", err)
	}
	return cpID, false, nil
}

// saveAttachSessionState creates or updates the session state file for the attached session.
func saveAttachSessionState(ctx context.Context, sessionID string, agentType types.AgentType, transcriptPath string, checkpointID id.CheckpointID, meta transcriptMetadata, tokenUsage *agent.TokenUsage) error {
	stateStore, err := session.NewStateStore(ctx)
	if err != nil {
		return fmt.Errorf("failed to open session store: %w", err)
	}

	state, err := stateStore.Load(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to load session state: %w", err)
	}

	now := time.Now()
	if state == nil {
		state = &session.State{
			SessionID: sessionID,
			StartedAt: now,
		}
	}

	state.CLIVersion = versioninfo.Version
	state.AgentType = agentType
	state.TranscriptPath = transcriptPath
	state.LastCheckpointID = checkpointID
	state.Phase = session.PhaseEnded
	state.LastInteractionTime = &now
	if meta.TurnCount > 0 {
		state.SessionTurnCount = meta.TurnCount
	}
	if meta.Model != "" {
		state.ModelName = meta.Model
	}
	if meta.FirstPrompt != "" {
		state.LastPrompt = meta.FirstPrompt
	}
	if tokenUsage != nil {
		state.TokenUsage = tokenUsage
	}
	// Note: session duration is not estimated here because we don't have the
	// raw transcript data. The token usage and turn count are sufficient metadata.

	if err := stateStore.Save(ctx, state); err != nil {
		return fmt.Errorf("failed to save session state: %w", err)
	}
	return nil
}

// validateAttachPreconditions checks session ID format and git repo state.
// Returns the existing session state if the session is already tracked (nil if new).
func validateAttachPreconditions(ctx context.Context, sessionID string) (*session.State, error) {
	if err := validation.ValidateSessionID(sessionID); err != nil {
		return nil, fmt.Errorf("invalid session ID: %w", err)
	}

	repo, repoErr := strategy.OpenRepository(ctx)
	if repoErr != nil {
		return nil, fmt.Errorf("failed to open repository: %w", repoErr)
	}
	if strategy.IsEmptyRepository(repo) {
		return nil, errors.New("repository has no commits yet — make an initial commit before running attach")
	}

	store, err := session.NewStateStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open session store: %w", err)
	}
	existing, err := store.Load(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing session: %w", err)
	}

	return existing, nil
}

// resolveAgentAndTranscript resolves the agent and transcript path, with auto-detection fallback.
func resolveAgentAndTranscript(ctx context.Context, w io.Writer, sessionID string, agentName types.AgentName) (agent.Agent, string, error) {
	ag, err := agent.Get(agentName)
	if err != nil {
		return nil, "", fmt.Errorf("agent %q not available: %w", agentName, err)
	}

	transcriptPath, err := resolveAndValidateTranscript(ctx, sessionID, ag)
	if err != nil {
		detectedAg, detectedPath, detectErr := detectAgentByTranscript(ctx, sessionID, agentName)
		if detectErr != nil {
			return nil, "", fmt.Errorf("%w (also tried auto-detecting other agents: %w)", err, detectErr)
		}
		ag = detectedAg
		transcriptPath = detectedPath
		logging.Info(ctx, "auto-detected agent from transcript", "agent", ag.Name())
		fmt.Fprintf(w, "Auto-detected agent: %s\n", ag.Name())
	}

	return ag, transcriptPath, nil
}

// resolveAgentForState resolves the agent from session state's AgentType,
// falling back to the --agent flag if the state has no type.
func resolveAgentForState(state *session.State, agentName types.AgentName) (agent.Agent, error) {
	if state.AgentType != "" {
		for _, name := range agent.List() {
			ag, err := agent.Get(name)
			if err != nil {
				continue
			}
			if ag.Type() == state.AgentType {
				return ag, nil
			}
		}
	}
	ag, err := agent.Get(agentName)
	if err != nil {
		return nil, fmt.Errorf("agent %q not available: %w", agentName, err)
	}
	return ag, nil
}

// resolveAndValidateTranscript finds the transcript file for a session, searching alternative
// project directories if needed.
func resolveAndValidateTranscript(ctx context.Context, sessionID string, ag agent.Agent) (string, error) {
	transcriptPath, err := resolveTranscriptPath(ctx, sessionID, ag)
	if err != nil {
		return "", fmt.Errorf("failed to resolve transcript path: %w", err)
	}
	if preparer, ok := agent.AsTranscriptPreparer(ag); ok {
		if prepErr := preparer.PrepareTranscript(ctx, transcriptPath); prepErr != nil {
			logging.Debug(ctx, "PrepareTranscript failed (best-effort)", "error", prepErr)
		}
	}
	if _, statErr := os.Stat(transcriptPath); statErr == nil {
		return transcriptPath, nil
	}
	found, searchErr := searchTranscriptInProjectDirs(sessionID, ag)
	if searchErr == nil {
		logging.Info(ctx, "found transcript in alternative project directory", "path", found)
		return found, nil
	}
	logging.Debug(ctx, "fallback transcript search failed", "error", searchErr)
	return "", fmt.Errorf("transcript not found for agent %q with session %s; is the session ID correct?", ag.Name(), sessionID)
}

// detectAgentByTranscript tries all registered agents (except skip) to find one whose
// transcript resolution succeeds for the given session ID.
func detectAgentByTranscript(ctx context.Context, sessionID string, skip types.AgentName) (agent.Agent, string, error) {
	for _, name := range agent.List() {
		if name == skip {
			continue
		}
		ag, err := agent.Get(name)
		if err != nil {
			continue
		}
		path, resolveErr := resolveAndValidateTranscript(ctx, sessionID, ag)
		if resolveErr != nil {
			logging.Debug(ctx, "auto-detect: agent did not match", "agent", string(name), "error", resolveErr)
			continue
		}
		return ag, path, nil
	}
	return nil, "", errors.New("transcript not found for any registered agent")
}

// promptAmendCommit shows the last commit and asks whether to amend it with the checkpoint trailer.
// When force is true, it amends without prompting.
func promptAmendCommit(ctx context.Context, w io.Writer, checkpointIDStr string, force bool) error {
	repo, err := openRepository(ctx)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}
	headRef, err := repo.Head()
	if err != nil {
		return fmt.Errorf("failed to get HEAD: %w", err)
	}
	headCommit, err := repo.CommitObject(headRef.Hash())
	if err != nil {
		return fmt.Errorf("failed to get HEAD commit: %w", err)
	}

	shortHash := headRef.Hash().String()[:7]
	subject := strings.SplitN(headCommit.Message, "\n", 2)[0]

	// Skip amending if this exact checkpoint ID is already in the commit.
	for _, existing := range trailers.ParseAllCheckpoints(headCommit.Message) {
		if existing.String() == checkpointIDStr {
			fmt.Fprintf(w, "Commit %s already has Entire-Checkpoint: %s\n", shortHash, checkpointIDStr)
			return nil
		}
	}

	fmt.Fprintf(w, "\nLast commit: %s %s\n", shortHash, subject)

	amend := true
	if !force {
		form := NewAccessibleForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Amend the last commit in this branch?").
					Affirmative("Y").
					Negative("n").
					Value(&amend),
			),
		)
		if err := form.Run(); err != nil {
			return fmt.Errorf("prompt failed: %w", err)
		}
	}

	if !amend {
		fmt.Fprintf(w, "\nCopy to your commit message to attach:\n\n  Entire-Checkpoint: %s\n", checkpointIDStr)
		return nil
	}

	newMessage := trailers.AppendCheckpointTrailer(headCommit.Message, checkpointIDStr)

	cmd := exec.CommandContext(ctx, "git", "commit", "--amend", "--only", "-m", newMessage)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to amend commit: %w\n%s", err, output)
	}

	fmt.Fprintf(w, "Amended commit %s with Entire-Checkpoint: %s\n", shortHash, checkpointIDStr)
	return nil
}
