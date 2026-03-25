package checkpoint

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadGeneration_EmptyTree_ReturnsDefault(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo)

	// Build an empty tree
	emptyTree, err := BuildTreeFromEntries(repo, map[string]object.TreeEntry{})
	require.NoError(t, err)

	gen, err := store.readGeneration(emptyTree)
	require.NoError(t, err)

	assert.Equal(t, 0, gen.Generation)
	assert.Equal(t, 0, gen.CheckpointCount)
	assert.Empty(t, gen.Checkpoints)
	assert.True(t, gen.OldestCheckpointAt.IsZero())
	assert.True(t, gen.NewestCheckpointAt.IsZero())
}

func TestReadGeneration_ParsesJSON(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo)

	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	original := GenerationMetadata{
		Generation:         1,
		CheckpointCount:    2,
		Checkpoints:        []string{"aabbccddeeff", "112233445566"},
		OldestCheckpointAt: now.Add(-1 * time.Hour),
		NewestCheckpointAt: now,
	}

	// Write generation.json into a tree
	entries := make(map[string]object.TreeEntry)
	require.NoError(t, store.writeGeneration(original, entries))

	treeHash, err := BuildTreeFromEntries(repo, entries)
	require.NoError(t, err)

	// Read it back
	gen, err := store.readGeneration(treeHash)
	require.NoError(t, err)

	assert.Equal(t, 1, gen.Generation)
	assert.Equal(t, 2, gen.CheckpointCount)
	assert.Equal(t, []string{"aabbccddeeff", "112233445566"}, gen.Checkpoints)
	assert.True(t, gen.OldestCheckpointAt.Equal(now.Add(-1*time.Hour)))
	assert.True(t, gen.NewestCheckpointAt.Equal(now))
}

func TestWriteGeneration_RoundTrips(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo)

	now := time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC)
	original := GenerationMetadata{
		Generation:         0,
		CheckpointCount:    1,
		Checkpoints:        []string{"aabbccddeeff"},
		OldestCheckpointAt: now,
		NewestCheckpointAt: now,
	}

	entries := make(map[string]object.TreeEntry)
	require.NoError(t, store.writeGeneration(original, entries))

	// Verify the entry was added at the right key
	_, ok := entries[paths.GenerationFileName]
	assert.True(t, ok)

	// Build tree and read back
	treeHash, err := BuildTreeFromEntries(repo, entries)
	require.NoError(t, err)

	gen, err := store.readGeneration(treeHash)
	require.NoError(t, err)

	assert.Equal(t, original.Generation, gen.Generation)
	assert.Equal(t, original.CheckpointCount, gen.CheckpointCount)
	assert.Equal(t, original.Checkpoints, gen.Checkpoints)
}

func TestReadGenerationFromRef(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo)

	// Create a ref with generation.json in its tree
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)
	gen := GenerationMetadata{
		Generation:         0,
		CheckpointCount:    1,
		Checkpoints:        []string{"aabbccddeeff"},
		OldestCheckpointAt: now,
		NewestCheckpointAt: now,
	}

	entries := make(map[string]object.TreeEntry)
	require.NoError(t, store.writeGeneration(gen, entries))
	treeHash, err := BuildTreeFromEntries(repo, entries)
	require.NoError(t, err)

	refName := plumbing.ReferenceName(paths.V2FullCurrentRefName)
	authorName, authorEmail := GetGitAuthorFromRepo(repo)
	commitHash, err := CreateCommit(repo, treeHash, plumbing.ZeroHash, "test", authorName, authorEmail)
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(refName, commitHash)))

	// Read back via ref
	result, err := store.readGenerationFromRef(refName)
	require.NoError(t, err)

	assert.Equal(t, 0, result.Generation)
	assert.Equal(t, 1, result.CheckpointCount)
	assert.Equal(t, []string{"aabbccddeeff"}, result.Checkpoints)
}

func TestAddGenerationToRootTree(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo)

	// Start with a root tree that has a shard directory entry (simulating checkpoint data)
	shardEntries := map[string]object.TreeEntry{}
	shardEntries["aa/bbccddeeff/0/full.jsonl"] = object.TreeEntry{
		Name: "full.jsonl",
		Mode: 0o100644,
		Hash: plumbing.ZeroHash, // dummy
	}
	rootTreeHash, err := BuildTreeFromEntries(repo, shardEntries)
	require.NoError(t, err)

	gen := GenerationMetadata{
		Generation:      0,
		CheckpointCount: 1,
		Checkpoints:     []string{"aabbccddeeff"},
	}

	// Add generation.json to the root tree
	newRootHash, err := store.addGenerationToRootTree(rootTreeHash, gen)
	require.NoError(t, err)
	assert.NotEqual(t, rootTreeHash, newRootHash)

	// Verify generation.json is present and shard dir is preserved
	readGen, err := store.readGeneration(newRootHash)
	require.NoError(t, err)
	assert.Equal(t, 1, readGen.CheckpointCount)

	// Verify the shard directory still exists in the tree
	tree, err := repo.TreeObject(newRootHash)
	require.NoError(t, err)
	foundShard := false
	for _, e := range tree.Entries {
		if e.Name == "aa" {
			foundShard = true
		}
	}
	assert.True(t, foundShard, "shard directory should be preserved")
}

// v2FullGeneration reads generation.json from the /full/current ref.
func v2FullGeneration(t *testing.T, repo *git.Repository) GenerationMetadata {
	t.Helper()
	store := NewV2GitStore(repo)
	gen, err := store.readGenerationFromRef(plumbing.ReferenceName(paths.V2FullCurrentRefName))
	require.NoError(t, err)
	return gen
}

func TestWriteCommittedFull_UpdatesGenerationJSON(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("d1e2f3a4b5c6")
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-gen-001",
		Strategy:     "manual-commit",
		Agent:        agent.AgentTypeClaudeCode,
		Transcript:   []byte(`{"type":"assistant","message":"hello"}`),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	gen := v2FullGeneration(t, repo)
	assert.Equal(t, 0, gen.Generation, "active generation should be 0")
	assert.Equal(t, 1, gen.CheckpointCount)
	assert.Equal(t, []string{cpID.String()}, gen.Checkpoints)
	assert.False(t, gen.OldestCheckpointAt.IsZero())
	assert.False(t, gen.NewestCheckpointAt.IsZero())
}

func TestWriteCommittedFull_AccumulatesInGenerationJSON(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo)
	ctx := context.Background()

	cpA := id.MustCheckpointID("e2f3a4b5c6d1")
	cpB := id.MustCheckpointID("f3a4b5c6d1e2")

	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpA,
		SessionID:    "session-acc-A",
		Strategy:     "manual-commit",
		Agent:        agent.AgentTypeClaudeCode,
		Transcript:   []byte(`{"from":"A"}`),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	err = store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpB,
		SessionID:    "session-acc-B",
		Strategy:     "manual-commit",
		Agent:        agent.AgentTypeClaudeCode,
		Transcript:   []byte(`{"from":"B"}`),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	gen := v2FullGeneration(t, repo)
	assert.Equal(t, 2, gen.CheckpointCount)
	assert.Equal(t, []string{cpA.String(), cpB.String()}, gen.Checkpoints)
	assert.True(t, gen.NewestCheckpointAt.After(gen.OldestCheckpointAt) || gen.NewestCheckpointAt.Equal(gen.OldestCheckpointAt))
}

func TestUpdateCommitted_DoesNotUpdateGenerationJSON(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("a4b5c6d1e2f3")

	// Initial write
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-noupdate-gen",
		Strategy:     "manual-commit",
		Agent:        agent.AgentTypeClaudeCode,
		Transcript:   []byte(`{"type":"assistant","message":"initial"}`),
		Prompts:      []string{"first"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	genBefore := v2FullGeneration(t, repo)
	require.Equal(t, 1, genBefore.CheckpointCount)

	// Update (stop-time finalization) — should NOT change generation.json
	err = store.UpdateCommitted(ctx, UpdateCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-noupdate-gen",
		Transcript:   []byte(`{"type":"assistant","message":"finalized"}`),
		Prompts:      []string{"first", "second"},
		Agent:        agent.AgentTypeClaudeCode,
	})
	require.NoError(t, err)

	genAfter := v2FullGeneration(t, repo)
	assert.Equal(t, 1, genAfter.CheckpointCount, "UpdateCommitted should not change checkpoint count")
	assert.Equal(t, genBefore.Checkpoints, genAfter.Checkpoints)

	// Verify the transcript was actually updated (sanity check)
	fullTree := v2FullTree(t, repo)
	content := v2ReadFile(t, fullTree, cpID.Path()+"/0/"+paths.TranscriptFileName)
	assert.Contains(t, content, "finalized")
}

func TestWriteCommittedFull_GenerationJSON_SameCheckpointIdNotDuplicated(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("b5c6d1e2f3a4")

	// Write same checkpoint twice (e.g., two sessions for the same commit)
	for _, sessID := range []string{"session-dup-1", "session-dup-2"} {
		err := store.WriteCommitted(ctx, WriteCommittedOptions{
			CheckpointID: cpID,
			SessionID:    sessID,
			Strategy:     "manual-commit",
			Agent:        agent.AgentTypeClaudeCode,
			Transcript:   []byte(`{"from":"` + sessID + `"}`),
			AuthorName:   "Test",
			AuthorEmail:  "test@test.com",
		})
		require.NoError(t, err)
	}

	gen := v2FullGeneration(t, repo)
	// Same checkpoint ID written twice should only appear once in the array
	assert.Equal(t, 1, gen.CheckpointCount)
	assert.Equal(t, []string{cpID.String()}, gen.Checkpoints)
}

func TestWriteCommittedFull_GenerationJSON_PreservedInTree(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2GitStore(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("c6d1e2f3a4b5")
	err := store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-tree-check",
		Strategy:     "manual-commit",
		Agent:        agent.AgentTypeClaudeCode,
		Transcript:   []byte(`{"check":"tree"}`),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	// Read the /full/current tree and verify generation.json is at root
	fullTree := v2FullTree(t, repo)
	genContent := v2ReadFile(t, fullTree, paths.GenerationFileName)
	var gen GenerationMetadata
	require.NoError(t, json.Unmarshal([]byte(genContent), &gen))
	assert.Equal(t, 1, gen.CheckpointCount)

	// Verify checkpoint data is also present
	content := v2ReadFile(t, fullTree, cpID.Path()+"/0/"+paths.TranscriptFileName)
	assert.Contains(t, content, `"check":"tree"`)
}
