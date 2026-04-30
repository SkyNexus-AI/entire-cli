//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/review"
	"github.com/entireio/cli/cmd/entire/cli/session"
)

// TestReview_EnvVarAdoptionCondensesReviewMetadataOnNextCommit exercises
// the full adoption pipeline: ENTIRE_REVIEW_* env vars are present when the
// UserPromptSubmit hook fires (as `entire review` sets them on the spawned
// agent process), the session is tagged as a review, and the metadata is
// condensed into the checkpoint on the next git commit.
func TestReview_EnvVarAdoptionCondensesReviewMetadataOnNextCommit(t *testing.T) {
	t.Parallel()

	env := NewFeatureBranchEnv(t)
	enableReviewAgent(t, env, "claude-code")

	skills := []string{"/pr-review-toolkit:review-pr", "/test-auditor"}
	reviewPrompt := composeReviewPromptForTest(skills)
	skillsJSON, err := review.EncodeSkills(skills)
	if err != nil {
		t.Fatalf("encode skills: %v", err)
	}

	// Simulate the env vars that `entire review` sets on the spawned agent
	// process before running the hook.
	reviewEnv := []string{
		review.EnvSession + "=1",
		review.EnvAgent + "=claude-code",
		review.EnvSkills + "=" + skillsJSON,
		review.EnvPrompt + "=" + reviewPrompt,
		review.EnvStartingSHA + "=" + env.GetHeadHash(),
	}

	sess := env.NewSession()
	if err := env.SimulateUserPromptSubmitWithReviewEnvVars(sess.ID, reviewPrompt, reviewEnv); err != nil {
		t.Fatalf("SimulateUserPromptSubmitWithReviewEnvVars failed: %v", err)
	}

	state, err := env.GetSessionState(sess.ID)
	if err != nil {
		t.Fatalf("GetSessionState failed: %v", err)
	}
	if state == nil {
		t.Fatal("expected review session state to be created")
	}
	if state.Kind != session.KindAgentReview {
		t.Fatalf("state.Kind = %q, want %q", state.Kind, session.KindAgentReview)
	}
	if len(state.ReviewSkills) != 2 || state.ReviewSkills[0] != "/pr-review-toolkit:review-pr" || state.ReviewSkills[1] != "/test-auditor" {
		t.Fatalf("state.ReviewSkills = %v", state.ReviewSkills)
	}
	if state.ReviewPrompt != reviewPrompt {
		t.Fatalf("state.ReviewPrompt = %q, want %q", state.ReviewPrompt, reviewPrompt)
	}

	env.WriteFile("review_target.go", "package main\n\nfunc ReviewTarget() string { return \"ok\" }\n")
	sess.CreateTranscript(reviewPrompt, []FileChange{
		{Path: "review_target.go", Content: "package main\n\nfunc ReviewTarget() string { return \"ok\" }\n"},
	})
	if err := env.SimulateStop(sess.ID, sess.TranscriptPath); err != nil {
		t.Fatalf("SimulateStop failed: %v", err)
	}

	env.GitCommitWithShadowHooks("add review target", "review_target.go")

	checkpointID := env.GetCheckpointIDFromCommitMessage(env.GetHeadHash())
	if checkpointID == "" {
		t.Fatal("expected Entire-Checkpoint trailer on HEAD after commit")
	}

	summary := readCheckpointSummary(t, env, checkpointID)
	if !summary.HasReview {
		t.Fatalf("summary.HasReview = false for checkpoint %s", checkpointID)
	}

	metadata := readSessionMetadata(t, env, checkpointID)
	if metadata.SessionID != sess.ID {
		t.Fatalf("metadata.SessionID = %q, want %q", metadata.SessionID, sess.ID)
	}
	if metadata.Kind != string(session.KindAgentReview) {
		t.Fatalf("metadata.Kind = %q, want %q", metadata.Kind, session.KindAgentReview)
	}
	if len(metadata.ReviewSkills) != 2 || metadata.ReviewSkills[0] != "/pr-review-toolkit:review-pr" || metadata.ReviewSkills[1] != "/test-auditor" {
		t.Fatalf("metadata.ReviewSkills = %v", metadata.ReviewSkills)
	}
	if metadata.ReviewPrompt != state.ReviewPrompt {
		t.Fatalf("metadata.ReviewPrompt = %q, want %q", metadata.ReviewPrompt, state.ReviewPrompt)
	}
}

func TestReviewAttach_TagsAttachedSessionAsReview(t *testing.T) {
	t.Parallel()

	env := NewFeatureBranchEnv(t)

	sessionID := "review-attach-session"
	tb := NewTranscriptBuilder()
	tb.AddUserMessage("review the branch for security regressions")
	tb.AddAssistantMessage("I found a few areas to check.")
	transcriptPath := filepath.Join(env.ClaudeProjectDir, sessionID+".jsonl")
	if err := tb.WriteToFile(transcriptPath); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	output := env.RunCLI("review", "attach", sessionID, "--force", "--agent", "claude-code", "--skills", "/pr-review-toolkit:review-pr")
	if !strings.Contains(output, "Attached session") {
		t.Fatalf("expected attached session output, got:\n%s", output)
	}

	checkpointID := env.GetCheckpointIDFromCommitMessage(env.GetHeadHash())
	if checkpointID == "" {
		t.Fatal("expected Entire-Checkpoint trailer on HEAD after review attach")
	}

	state, err := env.GetSessionState(sessionID)
	if err != nil {
		t.Fatalf("GetSessionState failed: %v", err)
	}
	if state == nil {
		t.Fatal("expected attached session state to be created")
	}
	if state.Kind != session.KindAgentReview {
		t.Fatalf("state.Kind = %q, want %q", state.Kind, session.KindAgentReview)
	}
	if len(state.ReviewSkills) != 1 || state.ReviewSkills[0] != "/pr-review-toolkit:review-pr" {
		t.Fatalf("state.ReviewSkills = %v", state.ReviewSkills)
	}
	if state.ReviewPrompt != "review the branch for security regressions" {
		t.Fatalf("state.ReviewPrompt = %q", state.ReviewPrompt)
	}

	summary := readCheckpointSummary(t, env, checkpointID)
	if !summary.HasReview {
		t.Fatalf("summary.HasReview = false for checkpoint %s", checkpointID)
	}

	metadata := readSessionMetadata(t, env, checkpointID)
	if metadata.Kind != string(session.KindAgentReview) {
		t.Fatalf("metadata.Kind = %q, want %q", metadata.Kind, session.KindAgentReview)
	}
	if len(metadata.ReviewSkills) != 1 || metadata.ReviewSkills[0] != "/pr-review-toolkit:review-pr" {
		t.Fatalf("metadata.ReviewSkills = %v", metadata.ReviewSkills)
	}
	if metadata.ReviewPrompt != "review the branch for security regressions" {
		t.Fatalf("metadata.ReviewPrompt = %q", metadata.ReviewPrompt)
	}
}

// TestReview_MissingSkillAtSpawn_ErrorsCleanly pins the runtime verification
// guard: a settings file naming a nonexistent skill must cause entire review
// to exit non-zero with a clear stderr message and leave no pending marker.
func TestReview_MissingSkillAtSpawn_ErrorsCleanly(t *testing.T) {
	t.Parallel()

	env := NewFeatureBranchEnv(t)
	enableReviewAgent(t, env, "claude-code")

	env.WriteSettings(map[string]any{
		"review": map[string]any{
			"claude-code": map[string]any{
				"skills": []string{"/nonexistent:skill-xyz"},
			},
		},
	})

	output, exitErr := env.RunCLIWithError("review")
	if exitErr == nil {
		t.Fatalf("expected non-zero exit; output:\n%s", output)
	}
	if !strings.Contains(output, "not installed") {
		t.Errorf("stderr should mention skill not installed; got:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(env.RepoDir, ".git", "entire-sessions", "review-pending.json")); !os.IsNotExist(err) {
		t.Errorf("pending marker should not exist; stat err=%v", err)
	}
}

func enableReviewAgent(t *testing.T, env *TestEnv, name string) {
	t.Helper()
	env.RunCLI("enable", "--agent", name, "--telemetry=false")
}

func readCheckpointSummary(t *testing.T, env *TestEnv, checkpointID string) checkpoint.CheckpointSummary {
	t.Helper()

	content, found := env.ReadFileFromBranch(paths.MetadataBranchName, CheckpointSummaryPath(checkpointID))
	if !found {
		t.Fatalf("checkpoint summary not found for %s", checkpointID)
	}

	var summary checkpoint.CheckpointSummary
	if err := json.Unmarshal([]byte(content), &summary); err != nil {
		t.Fatalf("failed to parse checkpoint summary: %v\n%s", err, content)
	}
	return summary
}

func readSessionMetadata(t *testing.T, env *TestEnv, checkpointID string) checkpoint.CommittedMetadata {
	t.Helper()

	content, found := env.ReadFileFromBranch(paths.MetadataBranchName, SessionMetadataPath(checkpointID))
	if !found {
		t.Fatalf("session metadata not found for %s", checkpointID)
	}

	var metadata checkpoint.CommittedMetadata
	if err := json.Unmarshal([]byte(content), &metadata); err != nil {
		t.Fatalf("failed to parse session metadata: %v\n%s", err, content)
	}
	return metadata
}
