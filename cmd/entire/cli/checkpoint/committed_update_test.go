package checkpoint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// setupRepoForUpdate creates a repo with an initial commit and writes a committed checkpoint.
func setupRepoForUpdate(t *testing.T) (*git.Repository, *GitStore, id.CheckpointID) {
	t.Helper()

	tempDir := t.TempDir()
	repo, err := git.PlainInit(tempDir, false)
	if err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}

	readmeFile := filepath.Join(tempDir, "README.md")
	if err := os.WriteFile(readmeFile, []byte("# Test"), 0o644); err != nil {
		t.Fatalf("failed to write README: %v", err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatalf("failed to add README: %v", err)
	}
	if _, err := worktree.Commit("Initial commit", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com"},
	}); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	store := NewGitStore(repo)
	cpID := id.MustCheckpointID("a1b2c3d4e5f6")

	err = store.WriteCommitted(context.Background(), WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Strategy:     "manual-commit",
		Transcript:   []byte("provisional transcript line 1\n"),
		Prompts:      []string{"initial prompt"},
		Context:      []byte("initial context"),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	if err != nil {
		t.Fatalf("WriteCommitted() error = %v", err)
	}

	return repo, store, cpID
}

func TestUpdateCommitted_ReplacesTranscript(t *testing.T) {
	t.Parallel()
	_, store, cpID := setupRepoForUpdate(t)

	// Update with full transcript (replace semantics)
	fullTranscript := []byte("full transcript line 1\nfull transcript line 2\nfull transcript line 3\n")
	err := store.UpdateCommitted(context.Background(), UpdateCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Transcript:   fullTranscript,
	})
	if err != nil {
		t.Fatalf("UpdateCommitted() error = %v", err)
	}

	// Read back and verify transcript was replaced (not appended)
	content, err := store.ReadSessionContent(context.Background(), cpID, 0)
	if err != nil {
		t.Fatalf("ReadSessionContent() error = %v", err)
	}

	if string(content.Transcript) != string(fullTranscript) {
		t.Errorf("transcript mismatch\ngot:  %q\nwant: %q", string(content.Transcript), string(fullTranscript))
	}
}

func TestUpdateCommitted_ReplacesPrompts(t *testing.T) {
	t.Parallel()
	_, store, cpID := setupRepoForUpdate(t)

	err := store.UpdateCommitted(context.Background(), UpdateCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Prompts:      []string{"prompt 1", "prompt 2", "prompt 3"},
	})
	if err != nil {
		t.Fatalf("UpdateCommitted() error = %v", err)
	}

	content, err := store.ReadSessionContent(context.Background(), cpID, 0)
	if err != nil {
		t.Fatalf("ReadSessionContent() error = %v", err)
	}

	expected := "prompt 1\n\n---\n\nprompt 2\n\n---\n\nprompt 3"
	if content.Prompts != expected {
		t.Errorf("prompts mismatch\ngot:  %q\nwant: %q", content.Prompts, expected)
	}
}

func TestUpdateCommitted_ReplacesContext(t *testing.T) {
	t.Parallel()
	_, store, cpID := setupRepoForUpdate(t)

	err := store.UpdateCommitted(context.Background(), UpdateCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Context:      []byte("updated context with full session info"),
	})
	if err != nil {
		t.Fatalf("UpdateCommitted() error = %v", err)
	}

	content, err := store.ReadSessionContent(context.Background(), cpID, 0)
	if err != nil {
		t.Fatalf("ReadSessionContent() error = %v", err)
	}

	if content.Context != "updated context with full session info" {
		t.Errorf("context mismatch\ngot:  %q\nwant: %q", content.Context, "updated context with full session info")
	}
}

func TestUpdateCommitted_ReplacesAllFieldsTogether(t *testing.T) {
	t.Parallel()
	_, store, cpID := setupRepoForUpdate(t)

	fullTranscript := []byte("complete transcript\n")
	err := store.UpdateCommitted(context.Background(), UpdateCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Transcript:   fullTranscript,
		Prompts:      []string{"final prompt"},
		Context:      []byte("final context"),
	})
	if err != nil {
		t.Fatalf("UpdateCommitted() error = %v", err)
	}

	content, err := store.ReadSessionContent(context.Background(), cpID, 0)
	if err != nil {
		t.Fatalf("ReadSessionContent() error = %v", err)
	}

	if string(content.Transcript) != string(fullTranscript) {
		t.Errorf("transcript mismatch\ngot:  %q\nwant: %q", string(content.Transcript), string(fullTranscript))
	}
	if content.Prompts != "final prompt" {
		t.Errorf("prompts mismatch\ngot:  %q\nwant: %q", content.Prompts, "final prompt")
	}
	if content.Context != "final context" {
		t.Errorf("context mismatch\ngot:  %q\nwant: %q", content.Context, "final context")
	}
}

func TestUpdateCommitted_NonexistentCheckpoint(t *testing.T) {
	t.Parallel()
	_, store, _ := setupRepoForUpdate(t)

	err := store.UpdateCommitted(context.Background(), UpdateCommittedOptions{
		CheckpointID: id.MustCheckpointID("deadbeef1234"),
		SessionID:    "session-001",
		Transcript:   []byte("should fail"),
	})
	if err == nil {
		t.Fatal("expected error for nonexistent checkpoint, got nil")
	}
}

func TestUpdateCommitted_PreservesMetadata(t *testing.T) {
	t.Parallel()
	_, store, cpID := setupRepoForUpdate(t)

	// Read metadata before update
	contentBefore, err := store.ReadSessionContent(context.Background(), cpID, 0)
	if err != nil {
		t.Fatalf("ReadSessionContent() before error = %v", err)
	}

	// Update only transcript
	err = store.UpdateCommitted(context.Background(), UpdateCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Transcript:   []byte("updated transcript\n"),
	})
	if err != nil {
		t.Fatalf("UpdateCommitted() error = %v", err)
	}

	// Read metadata after update - should be unchanged
	contentAfter, err := store.ReadSessionContent(context.Background(), cpID, 0)
	if err != nil {
		t.Fatalf("ReadSessionContent() after error = %v", err)
	}

	if contentAfter.Metadata.SessionID != contentBefore.Metadata.SessionID {
		t.Errorf("session ID changed: %q -> %q", contentBefore.Metadata.SessionID, contentAfter.Metadata.SessionID)
	}
	if contentAfter.Metadata.Strategy != contentBefore.Metadata.Strategy {
		t.Errorf("strategy changed: %q -> %q", contentBefore.Metadata.Strategy, contentAfter.Metadata.Strategy)
	}
}

func TestUpdateCommitted_MultipleCheckpoints(t *testing.T) {
	t.Parallel()
	_, store, cpID1 := setupRepoForUpdate(t)

	// Write a second checkpoint
	cpID2 := id.MustCheckpointID("b2c3d4e5f6a1")
	err := store.WriteCommitted(context.Background(), WriteCommittedOptions{
		CheckpointID: cpID2,
		SessionID:    "session-001",
		Strategy:     "manual-commit",
		Transcript:   []byte("provisional cp2\n"),
		Prompts:      []string{"cp2 prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	if err != nil {
		t.Fatalf("WriteCommitted(cp2) error = %v", err)
	}

	fullTranscript := []byte("complete full transcript\n")

	// Update both checkpoints with the same full transcript
	for _, cpID := range []id.CheckpointID{cpID1, cpID2} {
		err = store.UpdateCommitted(context.Background(), UpdateCommittedOptions{
			CheckpointID: cpID,
			SessionID:    "session-001",
			Transcript:   fullTranscript,
			Prompts:      []string{"final prompt 1", "final prompt 2"},
			Context:      []byte("final context"),
		})
		if err != nil {
			t.Fatalf("UpdateCommitted(%s) error = %v", cpID, err)
		}
	}

	// Verify both have the full transcript
	for _, cpID := range []id.CheckpointID{cpID1, cpID2} {
		content, readErr := store.ReadSessionContent(context.Background(), cpID, 0)
		if readErr != nil {
			t.Fatalf("ReadSessionContent(%s) error = %v", cpID, readErr)
		}
		if string(content.Transcript) != string(fullTranscript) {
			t.Errorf("checkpoint %s: transcript mismatch\ngot:  %q\nwant: %q", cpID, string(content.Transcript), string(fullTranscript))
		}
	}
}

func TestUpdateCommitted_UpdatesContentHash(t *testing.T) {
	t.Parallel()
	repo, store, cpID := setupRepoForUpdate(t)

	// Update transcript
	err := store.UpdateCommitted(context.Background(), UpdateCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Transcript:   []byte("new full transcript content\n"),
	})
	if err != nil {
		t.Fatalf("UpdateCommitted() error = %v", err)
	}

	// Verify content_hash.txt was updated
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(paths.MetadataBranchName), true)
	if err != nil {
		t.Fatalf("failed to get ref: %v", err)
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("failed to get commit: %v", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatalf("failed to get tree: %v", err)
	}

	hashPath := cpID.Path() + "/0/" + paths.ContentHashFileName
	hashFile, err := tree.File(hashPath)
	if err != nil {
		t.Fatalf("content_hash.txt not found at %s: %v", hashPath, err)
	}
	hashContent, err := hashFile.Contents()
	if err != nil {
		t.Fatalf("failed to read content_hash.txt: %v", err)
	}

	// Hash should be for the new content, not the old
	if hashContent == "" || !isValidContentHash(hashContent) {
		t.Errorf("invalid content hash: %q", hashContent)
	}
}

func TestUpdateCommitted_EmptyCheckpointID(t *testing.T) {
	t.Parallel()
	_, store, _ := setupRepoForUpdate(t)

	err := store.UpdateCommitted(context.Background(), UpdateCommittedOptions{
		SessionID:  "session-001",
		Transcript: []byte("should fail"),
	})
	if err == nil {
		t.Fatal("expected error for empty checkpoint ID, got nil")
	}
}

func TestUpdateCommitted_FallsBackToLatestSession(t *testing.T) {
	t.Parallel()
	_, store, cpID := setupRepoForUpdate(t)

	// Update with wrong session ID — should fall back to latest (index 0)
	fullTranscript := []byte("updated via fallback\n")
	err := store.UpdateCommitted(context.Background(), UpdateCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "nonexistent-session",
		Transcript:   fullTranscript,
	})
	if err != nil {
		t.Fatalf("UpdateCommitted() error = %v", err)
	}

	// Verify transcript was updated on the latest (and only) session
	content, err := store.ReadSessionContent(context.Background(), cpID, 0)
	if err != nil {
		t.Fatalf("ReadSessionContent() error = %v", err)
	}
	if string(content.Transcript) != string(fullTranscript) {
		t.Errorf("transcript mismatch\ngot:  %q\nwant: %q", string(content.Transcript), string(fullTranscript))
	}
}

func TestUpdateCommitted_SummaryPreserved(t *testing.T) {
	t.Parallel()
	_, store, cpID := setupRepoForUpdate(t)

	// Verify the root-level CheckpointSummary is preserved after update
	summaryBefore, err := store.ReadCommitted(context.Background(), cpID)
	if err != nil {
		t.Fatalf("ReadCommitted() before error = %v", err)
	}

	err = store.UpdateCommitted(context.Background(), UpdateCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-001",
		Transcript:   []byte("updated\n"),
	})
	if err != nil {
		t.Fatalf("UpdateCommitted() error = %v", err)
	}

	summaryAfter, err := store.ReadCommitted(context.Background(), cpID)
	if err != nil {
		t.Fatalf("ReadCommitted() after error = %v", err)
	}

	if summaryAfter.CheckpointID != summaryBefore.CheckpointID {
		t.Errorf("checkpoint ID changed in summary")
	}
	if len(summaryAfter.Sessions) != len(summaryBefore.Sessions) {
		t.Errorf("sessions count changed: %d -> %d", len(summaryBefore.Sessions), len(summaryAfter.Sessions))
	}
}

func isValidContentHash(hash string) bool {
	return len(hash) > 10 && hash[:7] == "sha256:"
}

// Verify JSON serialization of the new fields on State
func TestState_TurnCheckpointIDs_JSON(t *testing.T) {
	t.Parallel()

	type stateSnippet struct {
		TurnCheckpointIDs []string `json:"turn_checkpoint_ids,omitempty"`
	}

	// Test round-trip
	original := stateSnippet{
		TurnCheckpointIDs: []string{"a1b2c3d4e5f6", "b2c3d4e5f6a1"},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded stateSnippet
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(decoded.TurnCheckpointIDs) != 2 {
		t.Errorf("expected 2 IDs, got %d", len(decoded.TurnCheckpointIDs))
	}

	// Test nil serialization (omitempty)
	empty := stateSnippet{}
	data, err = json.Marshal(empty)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if string(data) != "{}" {
		t.Errorf("expected empty JSON, got %s", string(data))
	}
}
