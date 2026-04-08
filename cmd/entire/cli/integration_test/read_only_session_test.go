//go:build integration

package integration

import (
	"encoding/json"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"
)

// TestReadOnlySession_NotCondensed verifies that a session which never touched
// any files is NOT condensed into a checkpoint when the user commits.
//
// This reproduces the "summarize" bug where tools like steipete/summarize spawn
// many rapid-fire Codex exec sessions that only read/analyze content without
// modifying files. Each session creates a full lifecycle (SessionStart →
// UserPromptSubmit → Stop) but never calls SaveStep and never modifies files.
// When the user later commits, these read-only sessions should be silently
// skipped — not condensed into the checkpoint.
//
// State machine transitions tested:
//   - IDLE + GitCommit → should NOT condense when FilesTouched is empty
//   - Only the session that actually touched files should appear in the checkpoint
func TestReadOnlySession_NotCondensed(t *testing.T) {
	t.Parallel()

	env := NewFeatureBranchEnv(t)

	// ========================================
	// Phase 1: Create a read-only session (no file changes)
	// ========================================
	t.Log("Phase 1: Simulate a read-only session (e.g., codex exec from summarize)")

	readOnlySess := env.NewSession()

	// Start the session — this creates session state
	if err := env.SimulateUserPromptSubmit(readOnlySess.ID); err != nil {
		t.Fatalf("read-only session user-prompt-submit failed: %v", err)
	}

	// Create a transcript with NO file changes — just a question and answer
	readOnlySess.TranscriptBuilder.AddUserMessage("Summarize the README for this project")
	readOnlySess.TranscriptBuilder.AddAssistantMessage("This project is a CLI tool for managing checkpoints.")
	if err := readOnlySess.TranscriptBuilder.WriteToFile(readOnlySess.TranscriptPath); err != nil {
		t.Fatalf("failed to write read-only transcript: %v", err)
	}

	// Stop the session — NO files were touched, NO SaveStep was called
	if err := env.SimulateStop(readOnlySess.ID, readOnlySess.TranscriptPath); err != nil {
		t.Fatalf("read-only session stop failed: %v", err)
	}

	// Verify: session is IDLE with empty FilesTouched
	roState, err := env.GetSessionState(readOnlySess.ID)
	if err != nil {
		t.Fatalf("GetSessionState for read-only session failed: %v", err)
	}
	if roState.Phase != session.PhaseIdle {
		t.Fatalf("Read-only session should be IDLE, got %s", roState.Phase)
	}
	if len(roState.FilesTouched) != 0 {
		t.Fatalf("Read-only session should have empty FilesTouched, got %v", roState.FilesTouched)
	}

	// ========================================
	// Phase 2: Create a normal session that DOES touch files
	// ========================================
	t.Log("Phase 2: Simulate a normal coding session that modifies files")

	codingSess := env.NewSession()

	if err := env.SimulateUserPromptSubmit(codingSess.ID); err != nil {
		t.Fatalf("coding session user-prompt-submit failed: %v", err)
	}

	// Create a file and a transcript that records the change
	env.WriteFile("feature.go", "package main\n\nfunc Feature() {}\n")
	codingSess.CreateTranscript("Create feature function", []FileChange{
		{Path: "feature.go", Content: "package main\n\nfunc Feature() {}\n"},
	})

	if err := env.SimulateStop(codingSess.ID, codingSess.TranscriptPath); err != nil {
		t.Fatalf("coding session stop failed: %v", err)
	}

	// Verify: coding session has files touched
	csState, err := env.GetSessionState(codingSess.ID)
	if err != nil {
		t.Fatalf("GetSessionState for coding session failed: %v", err)
	}
	if len(csState.FilesTouched) == 0 {
		t.Fatal("Coding session should have non-empty FilesTouched")
	}

	// ========================================
	// Phase 3: User commits — only the coding session should be condensed
	// ========================================
	t.Log("Phase 3: User commits; read-only session should NOT be condensed")

	env.GitCommitWithShadowHooks("Add feature", "feature.go")

	// Get the checkpoint ID from the commit
	commitHash := env.GetHeadHash()
	cpID := env.GetCheckpointIDFromCommitMessage(commitHash)
	if cpID == "" {
		t.Fatal("Commit should have an Entire-Checkpoint trailer")
	}

	// ========================================
	// Phase 4: Verify the read-only session was NOT included in the checkpoint
	// ========================================
	t.Log("Phase 4: Verify checkpoint contains only the coding session")

	// Read the checkpoint summary from entire/checkpoints/v1
	summaryPath := CheckpointSummaryPath(cpID)
	summaryContent, found := env.ReadFileFromBranch(paths.MetadataBranchName, summaryPath)
	if !found {
		t.Fatalf("CheckpointSummary not found at %s", summaryPath)
	}

	var summary checkpoint.CheckpointSummary
	if err := json.Unmarshal([]byte(summaryContent), &summary); err != nil {
		t.Fatalf("Failed to parse CheckpointSummary: %v", err)
	}

	// The checkpoint should contain exactly 1 session (the coding session)
	if len(summary.Sessions) != 1 {
		t.Errorf("Checkpoint should contain exactly 1 session (the coding session), got %d sessions", len(summary.Sessions))
		for i, s := range summary.Sessions {
			t.Logf("  Session %d: %s", i, s.Metadata)
		}
	}

	// The read-only session should NOT have been condensed — verify its state
	// is unchanged (StepCount should still be 0, not incremented by condensation)
	roStateAfter, err := env.GetSessionState(readOnlySess.ID)
	if err != nil {
		t.Fatalf("GetSessionState for read-only session after commit failed: %v", err)
	}
	if roStateAfter.StepCount != roState.StepCount {
		t.Errorf("Read-only session StepCount changed from %d to %d — it was incorrectly condensed",
			roState.StepCount, roStateAfter.StepCount)
	}
}

// TestReadOnlySession_ActiveDuringCommit_NotCondensed verifies that a session
// which is still ACTIVE (between UserPromptSubmit and Stop) but has not touched
// any files is NOT condensed when a commit happens mid-turn.
//
// This is the more realistic scenario for the summarize bug: the read-only session
// fires UserPromptSubmit and the commit happens before Stop. The session is ACTIVE
// with recent interaction, which normally bypasses the overlap check. But since
// no files were touched, it should still not be condensed.
func TestReadOnlySession_ActiveDuringCommit_NotCondensed(t *testing.T) {
	t.Parallel()

	env := NewFeatureBranchEnv(t)

	// ========================================
	// Phase 1: Create a coding session, do work, stop
	// ========================================
	t.Log("Phase 1: Create a coding session with file changes")

	codingSess := env.NewSession()

	if err := env.SimulateUserPromptSubmitWithTranscriptPath(codingSess.ID, codingSess.TranscriptPath); err != nil {
		t.Fatalf("coding session user-prompt-submit failed: %v", err)
	}

	env.WriteFile("feature.go", "package main\n\nfunc Feature() {}\n")
	codingSess.CreateTranscript("Create feature function", []FileChange{
		{Path: "feature.go", Content: "package main\n\nfunc Feature() {}\n"},
	})

	if err := env.SimulateStop(codingSess.ID, codingSess.TranscriptPath); err != nil {
		t.Fatalf("coding session stop failed: %v", err)
	}

	// ========================================
	// Phase 2: Start a read-only session — leave it ACTIVE (no Stop)
	// ========================================
	t.Log("Phase 2: Start read-only session, leave it ACTIVE")

	readOnlySess := env.NewSession()

	if err := env.SimulateUserPromptSubmitWithTranscriptPath(readOnlySess.ID, readOnlySess.TranscriptPath); err != nil {
		t.Fatalf("read-only session user-prompt-submit failed: %v", err)
	}

	// Write a transcript with NO file changes
	readOnlySess.TranscriptBuilder.AddUserMessage("Explain what this codebase does")
	readOnlySess.TranscriptBuilder.AddAssistantMessage("This codebase implements a CLI tool.")
	if err := readOnlySess.TranscriptBuilder.WriteToFile(readOnlySess.TranscriptPath); err != nil {
		t.Fatalf("failed to write read-only transcript: %v", err)
	}

	// Verify it's ACTIVE
	roState, err := env.GetSessionState(readOnlySess.ID)
	if err != nil {
		t.Fatalf("GetSessionState failed: %v", err)
	}
	if roState.Phase != session.PhaseActive {
		t.Fatalf("Read-only session should be ACTIVE, got %s", roState.Phase)
	}
	if len(roState.FilesTouched) != 0 {
		t.Fatalf("Read-only session should have empty FilesTouched, got %v", roState.FilesTouched)
	}

	// ========================================
	// Phase 3: User commits while read-only session is still ACTIVE
	// ========================================
	t.Log("Phase 3: User commits while read-only session is ACTIVE")

	env.GitCommitWithShadowHooks("Add feature", "feature.go")

	commitHash := env.GetHeadHash()
	cpID := env.GetCheckpointIDFromCommitMessage(commitHash)
	if cpID == "" {
		t.Fatal("Commit should have an Entire-Checkpoint trailer")
	}

	// ========================================
	// Phase 4: Verify read-only ACTIVE session was NOT included
	// ========================================
	t.Log("Phase 4: Verify checkpoint contains only the coding session")

	summaryPath := CheckpointSummaryPath(cpID)
	summaryContent, found := env.ReadFileFromBranch(paths.MetadataBranchName, summaryPath)
	if !found {
		t.Fatalf("CheckpointSummary not found at %s", summaryPath)
	}

	var summary checkpoint.CheckpointSummary
	if err := json.Unmarshal([]byte(summaryContent), &summary); err != nil {
		t.Fatalf("Failed to parse CheckpointSummary: %v", err)
	}

	// The checkpoint should contain exactly 1 session (the coding session).
	// The ACTIVE read-only session should NOT be condensed even though it has
	// recent interaction — it touched no files.
	if len(summary.Sessions) != 1 {
		t.Errorf("Checkpoint should contain exactly 1 session (the coding session), got %d sessions", len(summary.Sessions))
		for i, s := range summary.Sessions {
			t.Logf("  Session %d: %s", i, s.Metadata)
		}
	}

	// Verify the checkpoint doesn't falsely attribute committed files to the read-only session
	for _, f := range summary.FilesTouched {
		if f != "feature.go" {
			t.Errorf("Unexpected file in checkpoint files_touched: %q", f)
		}
	}
}

// TestMultipleReadOnlySessions_NoneCondensed simulates the summarize scenario
// where many rapid-fire read-only sessions are created, then a user commits.
// None of the read-only sessions should appear in the checkpoint.
func TestMultipleReadOnlySessions_NoneCondensed(t *testing.T) {
	t.Parallel()

	env := NewFeatureBranchEnv(t)

	// ========================================
	// Phase 1: Create multiple read-only sessions (simulating summarize batch)
	// ========================================
	t.Log("Phase 1: Create 5 read-only sessions (simulating summarize batch runs)")

	for i := range 5 {
		sess := env.NewSession()

		if err := env.SimulateUserPromptSubmit(sess.ID); err != nil {
			t.Fatalf("read-only session %d user-prompt-submit failed: %v", i, err)
		}

		// Write a transcript with no file changes
		sess.TranscriptBuilder.AddUserMessage("Summarize this repository")
		sess.TranscriptBuilder.AddAssistantMessage("This is a summary.")
		if err := sess.TranscriptBuilder.WriteToFile(sess.TranscriptPath); err != nil {
			t.Fatalf("failed to write transcript for session %d: %v", i, err)
		}

		if err := env.SimulateStop(sess.ID, sess.TranscriptPath); err != nil {
			t.Fatalf("read-only session %d stop failed: %v", i, err)
		}
	}

	// ========================================
	// Phase 2: Create one real coding session
	// ========================================
	t.Log("Phase 2: Create a real coding session that modifies files")

	codingSess := env.NewSession()
	if err := env.SimulateUserPromptSubmit(codingSess.ID); err != nil {
		t.Fatalf("coding session user-prompt-submit failed: %v", err)
	}

	env.WriteFile("main.go", "package main\n\nfunc main() {}\n")
	codingSess.CreateTranscript("Create main function", []FileChange{
		{Path: "main.go", Content: "package main\n\nfunc main() {}\n"},
	})

	if err := env.SimulateStop(codingSess.ID, codingSess.TranscriptPath); err != nil {
		t.Fatalf("coding session stop failed: %v", err)
	}

	// ========================================
	// Phase 3: User commits
	// ========================================
	t.Log("Phase 3: User commits")

	env.GitCommitWithShadowHooks("Add main function", "main.go")

	commitHash := env.GetHeadHash()
	cpID := env.GetCheckpointIDFromCommitMessage(commitHash)
	if cpID == "" {
		t.Fatal("Commit should have an Entire-Checkpoint trailer")
	}

	// ========================================
	// Phase 4: Verify only the coding session was condensed
	// ========================================
	t.Log("Phase 4: Verify checkpoint contains exactly 1 session")

	summaryPath := CheckpointSummaryPath(cpID)
	summaryContent, found := env.ReadFileFromBranch(paths.MetadataBranchName, summaryPath)
	if !found {
		t.Fatalf("CheckpointSummary not found at %s", summaryPath)
	}

	var summary checkpoint.CheckpointSummary
	if err := json.Unmarshal([]byte(summaryContent), &summary); err != nil {
		t.Fatalf("Failed to parse CheckpointSummary: %v", err)
	}

	// Should have exactly 1 session — the coding session.
	// The 5 read-only sessions should all have been skipped.
	if len(summary.Sessions) != 1 {
		t.Errorf("Checkpoint should contain exactly 1 session, got %d sessions (read-only sessions were incorrectly condensed)", len(summary.Sessions))
	}

	// Verify the checkpoint's files_touched only contains the coding session's files
	expectedFiles := map[string]bool{"main.go": true}
	for _, f := range summary.FilesTouched {
		if !expectedFiles[f] {
			t.Errorf("Unexpected file in checkpoint files_touched: %q (likely from read-only session fallback)", f)
		}
	}
}
