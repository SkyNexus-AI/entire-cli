package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/entireio/cli/cmd/entire/cli/transcript/compact"
	"github.com/entireio/cli/cmd/entire/cli/versioninfo"
	"github.com/entireio/cli/redact"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initMigrateTestRepo creates a repo with an initial commit.
func initMigrateTestRepo(t *testing.T) *git.Repository {
	t.Helper()
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	testutil.WriteFile(t, dir, "README.md", "init")
	testutil.GitAdd(t, dir, "README.md")
	testutil.GitCommit(t, dir, "initial")

	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)

	return repo
}

// writeV1Checkpoint writes a checkpoint to the v1 branch for testing.
func writeV1Checkpoint(t *testing.T, store *checkpoint.GitStore, cpID id.CheckpointID, sessionID string, transcript []byte, prompts []string) {
	t.Helper()
	err := store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    sessionID,
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted(transcript),
		Prompts:      prompts,
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)
}

func newMigrateStores(repo *git.Repository) (*checkpoint.GitStore, *checkpoint.V2GitStore) {
	return checkpoint.NewGitStore(repo), checkpoint.NewV2GitStore(repo, migrateRemoteName)
}

func buildTasksTreeHashWithContent(t *testing.T, repo *git.Repository, toolUseID string, content string) plumbing.Hash {
	t.Helper()

	blobHash, err := checkpoint.CreateBlobFromContent(repo, []byte(content))
	require.NoError(t, err)

	treeHash, err := checkpoint.BuildTreeFromEntries(context.Background(), repo, map[string]object.TreeEntry{
		toolUseID + "/checkpoint.json": {Mode: filemode.Regular, Hash: blobHash},
	})
	require.NoError(t, err)

	return treeHash
}

func addV1SessionTasksTree(t *testing.T, repo *git.Repository, cpID id.CheckpointID, sessionIdx int, toolUseID string) {
	t.Helper()
	addV1SessionTasksTreeWithContent(t, repo, cpID, sessionIdx, toolUseID, `{"tool_use_id":"`+toolUseID+`"}`)
}

func addV1SessionTasksTreeWithContent(t *testing.T, repo *git.Repository, cpID id.CheckpointID, sessionIdx int, toolUseID string, content string) {
	t.Helper()

	tasksTreeHash := buildTasksTreeHashWithContent(t, repo, toolUseID, content)
	tasksTree, err := repo.TreeObject(tasksTreeHash)
	require.NoError(t, err)

	refName := plumbing.NewBranchReferenceName(paths.MetadataBranchName)
	ref, err := repo.Reference(refName, true)
	require.NoError(t, err)

	commit, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)

	newRoot, err := checkpoint.UpdateSubtree(repo, commit.TreeHash,
		[]string{string(cpID[:2]), string(cpID[2:]), strconv.Itoa(sessionIdx), "tasks"},
		tasksTree.Entries,
		checkpoint.UpdateSubtreeOptions{MergeMode: checkpoint.MergeKeepExisting},
	)
	require.NoError(t, err)

	commitHash, err := checkpoint.CreateCommit(context.Background(), repo, newRoot, ref.Hash(),
		"Add test session task metadata\n",
		"Test", "test@test.com")
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(refName, commitHash)))
}

func addV1RootTasksTreeWithContent(t *testing.T, repo *git.Repository, cpID id.CheckpointID, toolUseID string, content string) {
	t.Helper()

	tasksTreeHash := buildTasksTreeHashWithContent(t, repo, toolUseID, content)
	tasksTree, err := repo.TreeObject(tasksTreeHash)
	require.NoError(t, err)

	refName := plumbing.NewBranchReferenceName(paths.MetadataBranchName)
	ref, err := repo.Reference(refName, true)
	require.NoError(t, err)

	commit, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)

	newRoot, err := checkpoint.UpdateSubtree(repo, commit.TreeHash,
		[]string{string(cpID[:2]), string(cpID[2:]), "tasks"},
		tasksTree.Entries,
		checkpoint.UpdateSubtreeOptions{MergeMode: checkpoint.MergeKeepExisting},
	)
	require.NoError(t, err)

	commitHash, err := checkpoint.CreateCommit(context.Background(), repo, newRoot, ref.Hash(),
		"Add test root task metadata\n",
		"Test", "test@test.com")
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(refName, commitHash)))
}

func TestMigrateCheckpointsV2_Basic(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("a1b2c3d4e5f6")
	writeV1Checkpoint(t, v1Store, cpID, "session-001",
		[]byte("{\"type\":\"assistant\",\"message\":\"hello\"}\n"),
		[]string{"test prompt"},
	)

	var stdout bytes.Buffer

	result, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.migrated)
	assert.Equal(t, 0, result.skipped)
	assert.Equal(t, 0, result.failed)

	// Verify checkpoint exists in v2
	summary, err := v2Store.ReadCommitted(context.Background(), cpID)
	require.NoError(t, err)
	require.NotNil(t, summary, "checkpoint should exist in v2 after migration")
	assert.Equal(t, cpID, summary.CheckpointID)
}

func TestMigrateCheckpointsV2_PreservesCreatedAt(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cpID := id.MustCheckpointID("b1c2d3e4f5a6")
	err := v1Store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-created-at",
		CreatedAt:    createdAt,
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted([]byte("{\"type\":\"assistant\",\"message\":\"hello\"}\n")),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	var stdout bytes.Buffer
	result, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.migrated)

	content, err := v2Store.ReadSessionContent(context.Background(), cpID, 0)
	require.NoError(t, err)
	assert.True(t, content.Metadata.CreatedAt.Equal(createdAt))
}

func TestMigrateCheckpointsV2_PacksFullGenerationsOldestFirst(t *testing.T) {
	oldMax := migrateMaxCheckpointsPerGeneration
	migrateMaxCheckpointsPerGeneration = 2
	t.Cleanup(func() {
		migrateMaxCheckpointsPerGeneration = oldMax
	})

	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)
	ctx := context.Background()

	checkpointIDs := []id.CheckpointID{
		id.MustCheckpointID("000000000001"),
		id.MustCheckpointID("000000000002"),
		id.MustCheckpointID("000000000003"),
		id.MustCheckpointID("000000000004"),
		id.MustCheckpointID("000000000005"),
	}
	createdAt := []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
	}

	// Write in non-chronological order to prove migration repacks by checkpoint time,
	// not v1 tree traversal or v1 ListCommitted's newest-first order.
	for _, idx := range []int{3, 1, 4, 0, 2} {
		err := v1Store.WriteCommitted(ctx, checkpoint.WriteCommittedOptions{
			CheckpointID: checkpointIDs[idx],
			SessionID:    "session-pack-" + strconv.Itoa(idx),
			CreatedAt:    createdAt[idx],
			Strategy:     "manual-commit",
			Transcript: redact.AlreadyRedacted([]byte(
				`{"type":"assistant","message":"checkpoint ` + strconv.Itoa(idx) + `"}` + "\n",
			)),
			Prompts:     []string{"prompt " + strconv.Itoa(idx)},
			AuthorName:  "Test",
			AuthorEmail: "test@test.com",
		})
		require.NoError(t, err)
	}

	var stdout bytes.Buffer
	result, err := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 5, result.migrated)
	assert.Equal(t, 0, result.skipped)
	assert.Equal(t, 0, result.failed)

	archived, err := v2Store.ListArchivedGenerations()
	require.NoError(t, err)
	require.Equal(t, []string{"0000000000001", "0000000000002", "0000000000003"}, archived)

	expectedBatches := [][]int{
		{0, 1},
		{2, 3},
		{4},
	}
	for genIdx, batch := range expectedBatches {
		refName := plumbing.ReferenceName(paths.V2FullRefPrefix + archived[genIdx])
		gen, genErr := v2Store.ReadGenerationFromRef(refName)
		require.NoError(t, genErr)
		assert.True(t, gen.OldestCheckpointAt.Equal(createdAt[batch[0]]), "generation %s oldest", archived[genIdx])
		assert.True(t, gen.NewestCheckpointAt.Equal(createdAt[batch[len(batch)-1]]), "generation %s newest", archived[genIdx])

		_, treeHash, refErr := v2Store.GetRefState(refName)
		require.NoError(t, refErr)
		count, countErr := v2Store.CountCheckpointsInTree(treeHash)
		require.NoError(t, countErr)
		assert.Equal(t, len(batch), count)

		tree, treeErr := repo.TreeObject(treeHash)
		require.NoError(t, treeErr)
		for _, idx := range batch {
			_, treeErr = tree.Tree(checkpointIDs[idx].Path())
			require.NoError(t, treeErr, "generation %s should contain checkpoint %s", archived[genIdx], checkpointIDs[idx])
		}
	}

	_, currentTreeHash, err := v2Store.GetRefState(plumbing.ReferenceName(paths.V2FullCurrentRefName))
	require.NoError(t, err)
	currentCount, err := v2Store.CountCheckpointsInTree(currentTreeHash)
	require.NoError(t, err)
	assert.Equal(t, 0, currentCount, "fresh migration should leave /full/current empty for post-migration writes")
}

func TestMigrateCheckpointsV2_PacksFullGenerationMetadataFromRawTranscriptTimestamps(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("101112131415")
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rawOldest := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	rawNewest := time.Date(2026, 3, 10, 9, 5, 0, 0, time.UTC)
	transcript := []byte(
		`{"type":"user","timestamp":"` + rawOldest.Format(time.RFC3339Nano) + `"}` + "\n" +
			`{"type":"assistant","timestamp":"` + rawNewest.Format(time.RFC3339Nano) + `"}` + "\n",
	)

	err := v1Store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-raw-timestamps",
		CreatedAt:    createdAt,
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted(transcript),
		Prompts:      []string{"raw timestamp prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	var stdout bytes.Buffer
	result, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.migrated)

	archived, err := v2Store.ListArchivedGenerations()
	require.NoError(t, err)
	require.Equal(t, []string{"0000000000001"}, archived)

	gen, err := v2Store.ReadGenerationFromRef(plumbing.ReferenceName(paths.V2FullRefPrefix + archived[0]))
	require.NoError(t, err)
	assert.True(t, gen.OldestCheckpointAt.Equal(rawOldest))
	assert.True(t, gen.NewestCheckpointAt.Equal(rawNewest))
	assert.False(t, gen.OldestCheckpointAt.Equal(createdAt), "raw transcript timestamps should take precedence over checkpoint metadata")
}

// TestMigrateCheckpointsV2_RerunResumesInterruptedMigration verifies that
// when a previous migration was interrupted between writing /main and
// flushing the generation packer, a rerun without --force picks up where it
// left off and packs the missing /full artifacts.
func TestMigrateCheckpointsV2_RerunResumesInterruptedMigration(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("000000000011")
	writeV1Checkpoint(t, v1Store, cpID, "session-interrupt",
		[]byte(`{"type":"assistant","message":"hi"}`+"\n"),
		[]string{"prompt"},
	)

	// Simulate an interrupted prior migration: /main is written but the raw
	// transcript never reached /full/* (we drop the fullCheckpoint that
	// would otherwise have been fed to the packer).
	v1List, err := v1Store.ListCommitted(ctx)
	require.NoError(t, err)
	require.Len(t, v1List, 1)
	fullCheckpoint, _, migrateErr := migrateOneCheckpoint(ctx, repo, v1Store, v2Store, v1List[0], false)
	require.NoError(t, migrateErr)
	require.NotNil(t, fullCheckpoint)

	hasFullBefore, err := v2Store.HasFullSessionArtifacts(cpID, 0)
	require.NoError(t, err)
	require.False(t, hasFullBefore, "precondition: full artifacts should be missing")

	// Rerun without --force: must pick up the interrupted checkpoint and
	// finish packing it. Counted as migrated, not skipped.
	var rerun bytes.Buffer
	result, err := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &rerun, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.migrated)
	assert.Equal(t, 0, result.skipped)
	assert.Equal(t, 0, result.failed)

	hasFullAfter, err := v2Store.HasFullSessionArtifacts(cpID, 0)
	require.NoError(t, err)
	assert.True(t, hasFullAfter, "rerun should pack the missing raw transcript")

	// A second rerun once everything is packed must skip (no further work).
	var second bytes.Buffer
	result2, err := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &second, false)
	require.NoError(t, err)
	assert.Equal(t, 0, result2.migrated)
	assert.Equal(t, 1, result2.skipped)
}

// TestMigrateCheckpointsV2_RenamesLegacyArchivedTranscriptFiles verifies
// that an archived /full/<n> ref written under pre-rename filenames
// (full.jsonl / content_hash.txt) is rewritten in place so each session
// subtree carries the current names (raw_transcript /
// raw_transcript_hash.txt). The blob hashes — and therefore the file
// contents — must be unchanged.
func TestMigrateCheckpointsV2_RenamesLegacyArchivedTranscriptFiles(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("eeff22334455")
	writeV1Checkpoint(t, v1Store, cpID, "session-rename",
		[]byte(`{"type":"assistant","message":"keep the bytes"}`+"\n"),
		[]string{"rename prompt"},
	)

	var initial bytes.Buffer
	r1, err := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &initial, false)
	require.NoError(t, err)
	require.Equal(t, 1, r1.migrated)

	archived, err := v2Store.ListArchivedGenerations()
	require.NoError(t, err)
	require.Len(t, archived, 1)
	archivedRef := plumbing.ReferenceName(paths.V2FullRefPrefix + archived[0])

	// Capture the raw_transcript / raw_transcript_hash blob hashes BEFORE
	// rewriting to legacy names — the rename must preserve them.
	rawHashBefore, hashHashBefore := readRawTranscriptBlobHashesForTest(t, repo, v2Store, archivedRef, cpID, 0)

	// Mutate the archived gen to use legacy names.
	renameRawTranscriptArtifactsToLegacyNames(t, repo, v2Store, archivedRef, cpID, 0)
	require.False(t, sessionHasNewNamingForTest(t, repo, v2Store, archivedRef, cpID, 0),
		"precondition: legacy naming should be in place before rerun")

	// Rerun. Migration must rename the legacy entries back to current
	// names without changing the blob hashes.
	var rerun bytes.Buffer
	_, err = migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &rerun, false)
	require.NoError(t, err)

	rawHashAfter, hashHashAfter := readRawTranscriptBlobHashesForTest(t, repo, v2Store, archivedRef, cpID, 0)
	assert.Equal(t, rawHashBefore, rawHashAfter, "raw transcript blob hash must survive the rename unchanged")
	assert.Equal(t, hashHashBefore, hashHashAfter, "raw transcript hash blob must survive the rename unchanged")
	assert.True(t, sessionHasNewNamingForTest(t, repo, v2Store, archivedRef, cpID, 0),
		"session should be on current naming after rerun")

	// One more rerun should be a clean no-op — nothing left to rename.
	var second bytes.Buffer
	_, err = migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &second, false)
	require.NoError(t, err)
	assert.NotContains(t, second.String(), "Renamed legacy transcript filenames",
		"second rerun should produce no rename output")
}

// TestMigrateCheckpointsV2_RerunSkipsLegacyArchivedFullJsonl pins the
// regression for the 46→86 generation duplication: archived /full/<n> refs
// written under the pre-rename names (full.jsonl, content_hash.txt) must
// count as valid full artifacts so a rerun does not repack them into new
// generations.
func TestMigrateCheckpointsV2_RerunSkipsLegacyArchivedFullJsonl(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("ddee11223344")
	writeV1Checkpoint(t, v1Store, cpID, "session-legacy-archive",
		[]byte(`{"type":"assistant","message":"legacy"}`+"\n"),
		[]string{"legacy prompt"},
	)

	// Initial migration produces an archived /full/<n> with current naming.
	var initial bytes.Buffer
	r1, err := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &initial, false)
	require.NoError(t, err)
	require.Equal(t, 1, r1.migrated)

	// Rewrite the archived generation so its raw transcript files are
	// stored under the pre-rename names (full.jsonl / content_hash.txt) —
	// the state on disk for any repo migrated before commit a3cd77122.
	archived, err := v2Store.ListArchivedGenerations()
	require.NoError(t, err)
	require.Len(t, archived, 1)
	renameRawTranscriptArtifactsToLegacyNames(t, repo, v2Store, plumbing.ReferenceName(paths.V2FullRefPrefix+archived[0]), cpID, 0)

	archivedBefore, err := v2Store.ListArchivedGenerations()
	require.NoError(t, err)

	// Rerun must recognize the legacy names and skip.
	var rerun bytes.Buffer
	r2, err := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &rerun, false)
	require.NoError(t, err)
	assert.Equal(t, 0, r2.migrated)
	assert.Equal(t, 1, r2.skipped)

	archivedAfter, err := v2Store.ListArchivedGenerations()
	require.NoError(t, err)
	assert.Equal(t, archivedBefore, archivedAfter, "rerun must not create new archived generations for legacy-named artifacts")
}

func TestMigrateCheckpointsV2_Idempotent(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("c3d4e5f6a1b2")
	writeV1Checkpoint(t, v1Store, cpID, "session-idem",
		[]byte("{\"type\":\"assistant\",\"message\":\"idempotent test\"}\n"),
		[]string{"idem prompt"},
	)

	var stdout bytes.Buffer

	// First run: should migrate
	result1, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result1.migrated)
	assert.Equal(t, 0, result1.skipped)

	// Second run: should skip (no agent type means backfill also can't produce compact transcript)
	stdout.Reset()
	result2, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 0, result2.migrated)
	assert.Equal(t, 1, result2.skipped)
}

func TestMigrateCheckpointsV2_ForceOverwritesExisting(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("f0f1f2f3f4f5")
	writeV1Checkpoint(t, v1Store, cpID, "session-force",
		[]byte("{\"type\":\"assistant\",\"message\":\"original\"}\n"),
		[]string{"original prompt"},
	)

	var stdout bytes.Buffer

	// First run: normal migration
	result1, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result1.migrated)

	// Second run without force: should skip
	stdout.Reset()
	result2, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 0, result2.migrated)
	assert.Equal(t, 1, result2.skipped)

	// Third run with force: should re-migrate
	stdout.Reset()
	result3, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, true)
	require.NoError(t, err)
	assert.Equal(t, 1, result3.migrated)
	assert.Equal(t, 0, result3.skipped)
	assert.Empty(t, stdout.String())

	// Verify checkpoint still readable in v2
	summary, readErr := v2Store.ReadCommitted(context.Background(), cpID)
	require.NoError(t, readErr)
	require.NotNil(t, summary)
	assert.Equal(t, cpID, summary.CheckpointID)

	archived, err := v2Store.ListArchivedGenerations()
	require.NoError(t, err)
	require.Equal(t, []string{"0000000000001"}, archived, "force migration should replace archived raw transcripts instead of duplicating them into a later generation")

	_, currentTreeHash, err := v2Store.GetRefState(plumbing.ReferenceName(paths.V2FullCurrentRefName))
	require.NoError(t, err)
	currentCount, err := v2Store.CountCheckpointsInTree(currentTreeHash)
	require.NoError(t, err)
	assert.Equal(t, 0, currentCount, "force migration should leave /full/current empty for post-migration writes")
}

func TestMigrateCheckpointsV2_ForceMultipleCheckpoints(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID1 := id.MustCheckpointID("a0a1a2a3a4a5")
	cpID2 := id.MustCheckpointID("b0b1b2b3b4b5")
	writeV1Checkpoint(t, v1Store, cpID1, "session-force-1",
		[]byte("{\"type\":\"assistant\",\"message\":\"first\"}\n"),
		[]string{"prompt 1"},
	)
	writeV1Checkpoint(t, v1Store, cpID2, "session-force-2",
		[]byte("{\"type\":\"assistant\",\"message\":\"second\"}\n"),
		[]string{"prompt 2"},
	)

	// First run: migrates both
	var discard bytes.Buffer
	result1, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &discard, false)
	require.NoError(t, err)
	assert.Equal(t, 2, result1.migrated)

	// Force re-migrate: should re-migrate both (0 skipped)
	var stdout bytes.Buffer
	result2, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, true)
	require.NoError(t, err)
	assert.Equal(t, 2, result2.migrated)
	assert.Equal(t, 0, result2.skipped)
}

func TestPruneV2CheckpointForForce_RecomputesPartialArchivedGeneration(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)
	ctx := context.Background()

	cpID1 := id.MustCheckpointID("101010101010")
	cpID2 := id.MustCheckpointID("202020202020")
	cp1CreatedAt := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	cp2CreatedAt := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	for _, cp := range []struct {
		id        id.CheckpointID
		sessionID string
		createdAt time.Time
	}{
		{cpID1, "session-force-prune-1", cp1CreatedAt},
		{cpID2, "session-force-prune-2", cp2CreatedAt},
	} {
		err := v1Store.WriteCommitted(ctx, checkpoint.WriteCommittedOptions{
			CheckpointID: cp.id,
			SessionID:    cp.sessionID,
			CreatedAt:    cp.createdAt,
			Strategy:     "manual-commit",
			Transcript:   redact.AlreadyRedacted([]byte("{\"type\":\"assistant\",\"message\":\"force prune\"}\n")),
			AuthorName:   "Test",
			AuthorEmail:  "test@test.com",
		})
		require.NoError(t, err)
	}

	var stdout bytes.Buffer
	result, err := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 2, result.migrated)

	require.NoError(t, pruneV2CheckpointForForce(ctx, repo, v2Store, cpID1))

	archived, err := v2Store.ListArchivedGenerations()
	require.NoError(t, err)
	require.Equal(t, []string{"0000000000001"}, archived)

	refName := plumbing.ReferenceName(paths.V2FullRefPrefix + archived[0])
	_, treeHash, err := v2Store.GetRefState(refName)
	require.NoError(t, err)
	count, err := v2Store.CountCheckpointsInTree(treeHash)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	rootTree, err := repo.TreeObject(treeHash)
	require.NoError(t, err)
	_, err = rootTree.Tree(cpID1.Path())
	require.Error(t, err, "force prune should remove the target checkpoint from archived generations")
	_, err = rootTree.Tree(cpID2.Path())
	require.NoError(t, err, "force prune should preserve other checkpoints in the archived generation")

	gen, err := v2Store.ReadGenerationFromRef(refName)
	require.NoError(t, err)
	assert.True(t, gen.OldestCheckpointAt.Equal(cp2CreatedAt))
	assert.True(t, gen.NewestCheckpointAt.Equal(cp2CreatedAt))
}

func TestMigrateCmd_ForceFlag(t *testing.T) {
	t.Parallel()
	cmd := newMigrateCmd()

	// Verify --force flag exists
	flag := cmd.Flags().Lookup("force")
	require.NotNil(t, flag, "--force flag should be registered")
	assert.Equal(t, "false", flag.DefValue)
}

func TestMigrateCmd_RepairsArchivedGenerationMetadata(t *testing.T) {
	repo := initMigrateTestRepo(t)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	t.Chdir(wt.Filesystem.Root())
	paths.ClearWorktreeRootCache()

	cpID := id.MustCheckpointID("123456789abc")
	rawOldest := time.Date(2025, 12, 20, 8, 0, 0, 0, time.UTC)
	rawNewest := time.Date(2025, 12, 20, 8, 5, 0, 0, time.UTC)
	createArchivedGenerationRefWithRawTranscript(t, repo, "0000000000007", cpID,
		time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 7, 1, 0, 0, 0, time.UTC),
		rawOldest, rawNewest)

	cmd := newMigrateCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--checkpoints", "v2"})

	require.NoError(t, cmd.Execute())
	assert.Contains(t, stdout.String(), "Archived generation metadata repair: 1 repaired")
	assert.Empty(t, stderr.String())

	v2Store := checkpoint.NewV2GitStore(repo, migrateRemoteName)
	gen, genErr := v2Store.ReadGenerationFromRef(plumbing.ReferenceName(paths.V2FullRefPrefix + "0000000000007"))
	require.NoError(t, genErr)
	assert.True(t, gen.OldestCheckpointAt.Equal(rawOldest))
	assert.True(t, gen.NewestCheckpointAt.Equal(rawNewest))
}

func TestMigrateCheckpointsV2_MultiSession(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("d4e5f6a1b2c3")

	// Write first session
	writeV1Checkpoint(t, v1Store, cpID, "session-multi-1",
		[]byte("{\"type\":\"assistant\",\"message\":\"session 1\"}\n"),
		[]string{"prompt 1"},
	)

	// Write second session to same checkpoint
	writeV1Checkpoint(t, v1Store, cpID, "session-multi-2",
		[]byte("{\"type\":\"assistant\",\"message\":\"session 2\"}\n"),
		[]string{"prompt 2"},
	)

	var stdout bytes.Buffer

	result, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.migrated)

	// Verify both sessions are in v2
	summary, readErr := v2Store.ReadCommitted(context.Background(), cpID)
	require.NoError(t, readErr)
	require.NotNil(t, summary)
	assert.GreaterOrEqual(t, len(summary.Sessions), 2, "should have at least 2 sessions")
}

func TestMigrateCheckpointsV2_SkipsV1SessionWithoutTranscript(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("445566778899")

	writeV1Checkpoint(t, v1Store, cpID, "session-real",
		[]byte("{\"type\":\"assistant\",\"message\":\"real session\"}\n"),
		[]string{"real prompt"},
	)

	err := v1Store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-without-transcript",
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted(nil),
		Prompts:      []string{"metadata-only prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	var stdout bytes.Buffer
	result, migrateErr := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, migrateErr)
	assert.Equal(t, 1, result.migrated)
	assert.Equal(t, 0, result.skipped)
	assert.Equal(t, 0, result.failed)
	assert.Equal(t, 1, result.missingSessions)

	output := stdout.String()
	assert.NotContains(t, output, "warning: skipping v1 session 1")
	assert.NotContains(t, output, "skipped 1 session(s) with missing transcript/session content")

	summary, readErr := v2Store.ReadCommitted(context.Background(), cpID)
	require.NoError(t, readErr)
	require.NotNil(t, summary)
	require.Len(t, summary.Sessions, 1)
	assert.Equal(t, "/"+cpID.Path()+"/0/metadata.json", summary.Sessions[0].Metadata)
}

func TestMigrateCheckpointsV2_SkipsV1SessionWithMissingDirectory(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("4455667788aa")
	writeV1Checkpoint(t, v1Store, cpID, "session-real",
		[]byte("{\"type\":\"assistant\",\"message\":\"real session\"}\n"),
		[]string{"real prompt"},
	)
	appendMissingV1SessionReference(t, repo, v1Store, cpID)

	var stdout bytes.Buffer
	result, migrateErr := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, migrateErr)
	assert.Equal(t, 1, result.migrated)
	assert.Equal(t, 0, result.skipped)
	assert.Equal(t, 0, result.failed)
	assert.Equal(t, 1, result.missingSessions)

	output := stdout.String()
	assert.NotContains(t, output, "warning: skipping v1 session 1")
	assert.NotContains(t, output, "skipped 1 session(s) with missing transcript/session content")

	summary, readErr := v2Store.ReadCommitted(context.Background(), cpID)
	require.NoError(t, readErr)
	require.NotNil(t, summary)
	require.Len(t, summary.Sessions, 1)
	assert.Equal(t, "/"+cpID.Path()+"/0/metadata.json", summary.Sessions[0].Metadata)
}

func TestMigrateCheckpointsV2_TaskMetadataUsesMigratedSessionIndexAfterSkip(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("66778899aabb")

	writeV1Checkpoint(t, v1Store, cpID, "session-real",
		[]byte("{\"type\":\"assistant\",\"message\":\"real session\"}\n"),
		[]string{"real prompt"},
	)

	err := v1Store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-without-transcript",
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted(nil),
		Prompts:      []string{"metadata-only prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	err = v1Store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-task",
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted([]byte("{\"type\":\"assistant\",\"message\":\"task session\"}\n")),
		Prompts:      []string{"task prompt"},
		IsTask:       true,
		ToolUseID:    "toolu_root_shifted",
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)
	addV1SessionTasksTree(t, repo, cpID, 2, "toolu_session_shifted")

	var stdout bytes.Buffer
	result, migrateErr := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, migrateErr)
	assert.Equal(t, 1, result.migrated)

	summary, readErr := v2Store.ReadCommitted(context.Background(), cpID)
	require.NoError(t, readErr)
	require.NotNil(t, summary)
	require.Len(t, summary.Sessions, 2)
	assert.Equal(t, "/"+cpID.Path()+"/1/metadata.json", summary.Sessions[1].Metadata)

	rootTree := v2FullTreeForCheckpoint(t, repo, v2Store, cpID)

	_, err = rootTree.File(cpID.Path() + "/1/tasks/toolu_root_shifted/checkpoint.json")
	require.NoError(t, err, "root task metadata should follow the shifted v2 session index")
	_, err = rootTree.File(cpID.Path() + "/1/tasks/toolu_session_shifted/checkpoint.json")
	require.NoError(t, err, "session task metadata should follow the shifted v2 session index")
	_, err = rootTree.File(cpID.Path() + "/2/tasks/toolu_root_shifted/checkpoint.json")
	require.Error(t, err, "task metadata must not be written under a non-existent v2 session")
}

func TestMigrateCheckpointsV2_TaskMetadataKeepsFirstConflictingTaskTree(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("8899aabbccdd")
	toolUseID := "toolu_conflict"
	err := v1Store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-conflict",
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted([]byte("{\"type\":\"assistant\",\"message\":\"conflict\"}\n")),
		Prompts:      []string{"conflict prompt"},
		IsTask:       true,
		ToolUseID:    toolUseID,
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)
	addV1RootTasksTreeWithContent(t, repo, cpID, toolUseID, `{"source":"root"}`)
	addV1SessionTasksTreeWithContent(t, repo, cpID, 0, toolUseID, `{"source":"session"}`)

	var stdout bytes.Buffer
	result, migrateErr := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, migrateErr)
	assert.Equal(t, 1, result.migrated)

	rootTree := v2FullTreeForCheckpoint(t, repo, v2Store, cpID)
	file, err := rootTree.File(cpID.Path() + "/0/tasks/" + toolUseID + "/checkpoint.json")
	require.NoError(t, err)
	content, err := file.Contents()
	require.NoError(t, err)
	assert.JSONEq(t, `{"source":"root"}`, content)
}

func TestMigrateCheckpointsV2_SkipsCheckpointWhenAllV1SessionsMissingTranscript(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("5566778899bb")
	err := v1Store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "metadata-only-session",
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted(nil),
		Prompts:      []string{"metadata-only prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	var stdout bytes.Buffer
	result, migrateErr := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, migrateErr)
	assert.Equal(t, 0, result.migrated)
	assert.Equal(t, 1, result.skipped)
	assert.Equal(t, 0, result.failed)
	assert.Equal(t, 1, result.missingSessions)

	output := stdout.String()
	assert.NotContains(t, output, "warning: skipping v1 session 0")
	assert.NotContains(t, output, "skipped (no migratable v1 sessions")

	summary, readErr := v2Store.ReadCommitted(context.Background(), cpID)
	require.NoError(t, readErr)
	assert.Nil(t, summary)
}

func TestMigrateCheckpointsV2_ForcePrunesSkippedV2Sessions(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("778899aabbcc")
	writeV1Checkpoint(t, v1Store, cpID, "session-keep",
		[]byte("{\"type\":\"assistant\",\"message\":\"keep\"}\n"),
		[]string{"keep prompt"},
	)
	writeV1Checkpoint(t, v1Store, cpID, "session-stale",
		[]byte("{\"type\":\"assistant\",\"message\":\"stale\"}\n"),
		[]string{"stale prompt"},
	)

	var initialRun bytes.Buffer
	result1, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &initialRun, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result1.migrated)

	initialSummary, readErr := v2Store.ReadCommitted(context.Background(), cpID)
	require.NoError(t, readErr)
	require.NotNil(t, initialSummary)
	require.Len(t, initialSummary.Sessions, 2)

	err = v1Store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-stale",
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted(nil),
		Prompts:      []string{"metadata-only stale prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	var stdout bytes.Buffer
	result2, rerunErr := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, true)
	require.NoError(t, rerunErr)
	assert.Equal(t, 1, result2.migrated)
	assert.Equal(t, 0, result2.skipped)
	assert.Equal(t, 1, result2.missingSessions)
	assert.NotContains(t, stdout.String(), "warning: skipping v1 session 1")

	summary, readErr := v2Store.ReadCommitted(context.Background(), cpID)
	require.NoError(t, readErr)
	require.NotNil(t, summary)
	require.Len(t, summary.Sessions, 1)
	assert.Equal(t, "/"+cpID.Path()+"/0/metadata.json", summary.Sessions[0].Metadata)

	_, rootTreeHash, refErr := v2Store.GetRefState(plumbing.ReferenceName(paths.V2FullCurrentRefName))
	require.NoError(t, refErr)
	rootTree, treeErr := repo.TreeObject(rootTreeHash)
	require.NoError(t, treeErr)
	_, err = rootTree.File(cpID.Path() + "/1/" + paths.V2RawTranscriptHashFileName)
	require.Error(t, err, "force migration should remove stale full transcript data for skipped sessions")
}

func TestMigrateCheckpointsV2_ForcePruneRemovesEmptyShardWhenAllSessionsSkipped(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("8899aabbccdd")
	writeV1Checkpoint(t, v1Store, cpID, "session-stale-only",
		[]byte("{\"type\":\"assistant\",\"message\":\"stale only\"}\n"),
		[]string{"stale prompt"},
	)

	var initialRun bytes.Buffer
	result1, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &initialRun, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result1.migrated)

	err = v1Store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-stale-only",
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted(nil),
		Prompts:      []string{"metadata-only stale prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	var stdout bytes.Buffer
	result2, rerunErr := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, true)
	require.NoError(t, rerunErr)
	assert.Equal(t, 0, result2.migrated)
	assert.Equal(t, 1, result2.skipped)
	assert.Equal(t, 1, result2.missingSessions)
	assert.NotContains(t, stdout.String(), "no migratable v1 sessions")

	summary, readErr := v2Store.ReadCommitted(context.Background(), cpID)
	require.NoError(t, readErr)
	assert.Nil(t, summary)

	assertNoV2ShardPrefix(t, repo, v2Store, plumbing.ReferenceName(paths.V2MainRefName), cpID)
	assertNoV2ShardPrefix(t, repo, v2Store, plumbing.ReferenceName(paths.V2FullCurrentRefName), cpID)
}

func assertNoV2ShardPrefix(t *testing.T, repo *git.Repository, v2Store *checkpoint.V2GitStore, refName plumbing.ReferenceName, cpID id.CheckpointID) {
	t.Helper()

	_, rootTreeHash, err := v2Store.GetRefState(refName)
	require.NoError(t, err)

	rootTree, err := repo.TreeObject(rootTreeHash)
	require.NoError(t, err)

	_, err = rootTree.Tree(string(cpID[:2]))
	require.Error(t, err, "force prune should remove an empty shard prefix from %s", refName)
}

func appendMissingV1SessionReference(t *testing.T, repo *git.Repository, v1Store *checkpoint.GitStore, cpID id.CheckpointID) {
	t.Helper()

	ctx := context.Background()
	summary, err := v1Store.ReadCommitted(ctx, cpID)
	require.NoError(t, err)
	require.NotNil(t, summary)

	missingIndex := len(summary.Sessions)
	missingBase := "/" + cpID.Path() + "/" + strconv.Itoa(missingIndex) + "/"
	summary.Sessions = append(summary.Sessions, checkpoint.SessionFilePaths{
		Metadata:    missingBase + paths.MetadataFileName,
		Transcript:  missingBase + paths.TranscriptFileName,
		ContentHash: missingBase + paths.ContentHashFileName,
		Prompt:      missingBase + paths.PromptFileName,
	})

	metadataJSON, err := json.MarshalIndent(summary, "", "  ")
	require.NoError(t, err)
	metadataJSON = append(metadataJSON, '\n')

	metadataHash, err := checkpoint.CreateBlobFromContent(repo, metadataJSON)
	require.NoError(t, err)

	refName := plumbing.NewBranchReferenceName(paths.MetadataBranchName)
	ref, err := repo.Reference(refName, true)
	require.NoError(t, err)
	commit, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)

	newTreeHash, err := checkpoint.UpdateSubtree(
		repo,
		commit.TreeHash,
		[]string{string(cpID[:2]), string(cpID[2:])},
		[]object.TreeEntry{{
			Name: paths.MetadataFileName,
			Mode: filemode.Regular,
			Hash: metadataHash,
		}},
		checkpoint.UpdateSubtreeOptions{MergeMode: checkpoint.MergeKeepExisting},
	)
	require.NoError(t, err)

	newCommitHash, err := checkpoint.CreateCommit(ctx, repo, newTreeHash, ref.Hash(), "test: stale v1 session reference\n", "Test", "test@test.com")
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(refName, newCommitHash)))
}

func TestMigrateCheckpointsV2_NoV1Branch(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)
	var stdout bytes.Buffer

	// No v1 data written — ListCommitted returns empty
	result, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 0, result.migrated)
	assert.Empty(t, stdout.String())
}

func TestMigrateCmd_InvalidFlag(t *testing.T) {
	t.Parallel()
	cmd := newMigrateCmd()
	cmd.SetArgs([]string{"--checkpoints", "v3"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported checkpoints version")
}

func TestMigrateCheckpointsV2_CompactionSkipped(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("e5f6a1b2c3d4")
	// Write checkpoint with no agent type — compaction will be skipped
	err := v1Store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-noagent",
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted([]byte("{\"type\":\"assistant\",\"message\":\"no agent\"}\n")),
		Prompts:      []string{"compact fail prompt"},
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	var stdout bytes.Buffer

	result, migrateErr := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, migrateErr)
	assert.Equal(t, 1, result.migrated)
	assert.Equal(t, 1, result.compactTranscriptSkipped)
	assert.Empty(t, stdout.String())
}

func TestMigrateCheckpointsV2_TaskCheckpoint(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("b2c3d4e5f6a1")
	err := v1Store.WriteCommitted(context.Background(), checkpoint.WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-task-001",
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted([]byte("{\"type\":\"assistant\",\"message\":\"task work\"}\n")),
		Prompts:      []string{"task prompt"},
		IsTask:       true,
		ToolUseID:    "toolu_01ABC",
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	})
	require.NoError(t, err)

	var stdout bytes.Buffer

	result, migrateErr := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, migrateErr)
	assert.Equal(t, 1, result.migrated)

	// Verify task checkpoint exists in v2
	summary, readErr := v2Store.ReadCommitted(context.Background(), cpID)
	require.NoError(t, readErr)
	require.NotNil(t, summary)

	// Verify task metadata tree was copied into the migrated v2 /full/* generation.
	rootTree := v2FullTreeForCheckpoint(t, repo, v2Store, cpID)
	_, taskFileErr := rootTree.File(cpID.Path() + "/0/tasks/toolu_01ABC/checkpoint.json")
	require.NoError(t, taskFileErr, "expected migrated task checkpoint metadata in /full/*")
}

// TestMigrateCheckpointsV2_RerunPicksUpNewV1Checkpoints verifies that v1
// checkpoints added after a prior migration are migrated on rerun, while
// already-migrated checkpoints stay skipped.
func TestMigrateCheckpointsV2_RerunPicksUpNewV1Checkpoints(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)
	ctx := context.Background()

	cpExisting := id.MustCheckpointID("aaa111222333")
	writeV1Checkpoint(t, v1Store, cpExisting, "session-existing",
		[]byte(`{"type":"assistant","message":"existing"}`+"\n"),
		[]string{"existing prompt"},
	)

	var firstRun bytes.Buffer
	r1, err := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &firstRun, false)
	require.NoError(t, err)
	require.Equal(t, 1, r1.migrated)

	// Add a new v1 checkpoint after the initial migration completed.
	cpNew := id.MustCheckpointID("bbb444555666")
	writeV1Checkpoint(t, v1Store, cpNew, "session-new",
		[]byte(`{"type":"assistant","message":"new"}`+"\n"),
		[]string{"new prompt"},
	)

	// Rerun: existing must be skipped, new one must be migrated.
	var rerun bytes.Buffer
	r2, err := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &rerun, false)
	require.NoError(t, err)
	assert.Equal(t, 1, r2.migrated, "new v1 checkpoint should be migrated on rerun")
	assert.Equal(t, 1, r2.skipped, "already-migrated v1 checkpoint should be skipped")

	for _, cp := range []id.CheckpointID{cpExisting, cpNew} {
		hasFull, err := v2Store.HasFullSessionArtifacts(cp, 0)
		require.NoError(t, err)
		assert.True(t, hasFull, "checkpoint %s should have full artifacts after rerun", cp)
	}
}

func TestMigrateCheckpointsV2_AllSkippedOnRerun(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID1 := id.MustCheckpointID("f6a1b2c3d4e5")
	cpID2 := id.MustCheckpointID("a1b2c3d4e5f7")

	writeV1Checkpoint(t, v1Store, cpID1, "session-p1",
		[]byte("{\"type\":\"assistant\",\"message\":\"first\"}\n"),
		[]string{"prompt 1"},
	)
	writeV1Checkpoint(t, v1Store, cpID2, "session-p2",
		[]byte("{\"type\":\"assistant\",\"message\":\"second\"}\n"),
		[]string{"prompt 2"},
	)

	// First run: migrates both
	var discard bytes.Buffer
	result1, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &discard, false)
	require.NoError(t, err)
	assert.Equal(t, 2, result1.migrated)

	// Second run: skips both
	var stdout bytes.Buffer
	result2, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 0, result2.migrated)
	assert.Equal(t, 2, result2.skipped)
}

func TestMigrateCheckpointsV2_UsesComputedCompactTranscriptStart(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("5566778899aa")
	transcript := []byte(
		"{\"type\":\"human\",\"message\":{\"content\":\"prompt 1\"}}\n" +
			"{\"type\":\"assistant\",\"message\":{\"content\":\"reply 1\"}}\n" +
			"{\"type\":\"human\",\"message\":{\"content\":\"prompt 2\"}}\n" +
			"{\"type\":\"assistant\",\"message\":{\"content\":\"reply 2\"}}\n",
	)
	err := v1Store.WriteCommitted(ctx, checkpoint.WriteCommittedOptions{
		CheckpointID:              cpID,
		SessionID:                 "session-compact-start-migrate",
		Strategy:                  "manual-commit",
		Transcript:                redact.AlreadyRedacted(transcript),
		Prompts:                   []string{"prompt 2"},
		Agent:                     agent.AgentTypeClaudeCode,
		CheckpointTranscriptStart: 2, // full transcript line domain
		AuthorName:                "Test",
		AuthorEmail:               "test@test.com",
	})
	require.NoError(t, err)

	v1Content, err := v1Store.ReadSessionContent(ctx, cpID, 0)
	require.NoError(t, err)
	fullCompacted := tryCompactTranscript(ctx, v1Content.Transcript, v1Content.Metadata)
	require.NotNil(t, fullCompacted)
	scopedCompacted, err := compact.Compact(redact.AlreadyRedacted(v1Content.Transcript), compact.MetadataFields{
		Agent:      string(v1Content.Metadata.Agent),
		CLIVersion: versioninfo.Version,
		StartLine:  v1Content.Metadata.GetTranscriptStart(),
	})
	require.NoError(t, err)
	require.NotNil(t, scopedCompacted)
	require.Greater(t, bytes.Count(fullCompacted, []byte{'\n'}), bytes.Count(scopedCompacted, []byte{'\n'}))
	expectedOffset := computeCompactOffset(ctx, v1Content.Transcript, fullCompacted, v1Content.Metadata)
	require.Positive(t, expectedOffset, "expected non-zero compact transcript start")

	var stdout bytes.Buffer
	result, migrateErr := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, migrateErr)
	assert.Equal(t, 1, result.migrated)

	v2MainRef, err := repo.Reference(plumbing.ReferenceName(paths.V2MainRefName), true)
	require.NoError(t, err)
	v2MainCommit, err := repo.CommitObject(v2MainRef.Hash())
	require.NoError(t, err)
	v2MainTree, err := v2MainCommit.Tree()
	require.NoError(t, err)

	metadataFile, err := v2MainTree.File(cpID.Path() + "/0/" + paths.MetadataFileName)
	require.NoError(t, err)
	metadataContent, err := metadataFile.Contents()
	require.NoError(t, err)

	var metadata checkpoint.CommittedMetadata
	require.NoError(t, json.Unmarshal([]byte(metadataContent), &metadata))
	assert.Equal(t, expectedOffset, metadata.CheckpointTranscriptStart)

	storedCompact, err := v2Store.ReadSessionCompactTranscript(ctx, cpID, 0)
	require.NoError(t, err)
	assert.Equal(t, fullCompacted, storedCompact, "migration should persist cumulative compact transcript")
}

func TestMigrateCheckpointsV2_SkipsRepairWhenArchivedFullExists(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)

	cpID := id.MustCheckpointID("334455ddeeff")
	writeV1Checkpoint(t, v1Store, cpID, "session-repair-archive-001",
		[]byte("{\"type\":\"assistant\",\"message\":\"repair from archive fallback\"}\n"),
		[]string{"repair archive prompt"},
	)

	// Initial migration to seed v2.
	var initialRun bytes.Buffer
	result1, err := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &initialRun, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result1.migrated)

	// Fresh migration packs raw transcripts into an archived generation and
	// leaves /full/current empty.
	archivedRead, archivedReadErr := v2Store.ReadSessionContent(context.Background(), cpID, 0)
	require.NoError(t, archivedReadErr)
	assert.NotEmpty(t, archivedRead.Transcript)

	// Re-run migration: archived /full/* artifacts are sufficient, so it should
	// not rehydrate old raw transcripts into /full/current.
	var rerun bytes.Buffer
	result2, rerunErr := migrateCheckpointsV2(context.Background(), repo, v1Store, v2Store, &rerun, false)
	require.NoError(t, rerunErr)
	assert.Equal(t, 0, result2.migrated)
	assert.Equal(t, 1, result2.skipped)
	assert.NotContains(t, rerun.String(), "repaired partial v2 checkpoint state")

	ok, checkErr := v2Store.HasFullSessionArtifacts(cpID, 0)
	require.NoError(t, checkErr)
	assert.True(t, ok, "expected archived /full/* artifacts to count as present")
	assert.False(t, hasCurrentFullSessionArtifactsForTest(t, repo, v2Store, cpID, 0),
		"migration rerun must not copy archived artifacts back into /full/current")
}

func v2FullTreeForCheckpoint(t *testing.T, repo *git.Repository, v2Store *checkpoint.V2GitStore, cpID id.CheckpointID) *object.Tree {
	t.Helper()

	for _, refName := range v2FullRefSearchOrderForTest(t, v2Store) {
		_, rootTreeHash, err := v2Store.GetRefState(refName)
		if err != nil {
			continue
		}
		rootTree, err := repo.TreeObject(rootTreeHash)
		require.NoError(t, err)
		if _, treeErr := rootTree.Tree(cpID.Path()); treeErr == nil {
			return rootTree
		}
	}

	t.Fatalf("checkpoint %s not found in any v2 /full/* ref", cpID)
	return nil
}

func v2FullRefSearchOrderForTest(t *testing.T, v2Store *checkpoint.V2GitStore) []plumbing.ReferenceName {
	t.Helper()

	refNames := []plumbing.ReferenceName{plumbing.ReferenceName(paths.V2FullCurrentRefName)}
	archived, err := v2Store.ListArchivedGenerations()
	require.NoError(t, err)
	for i := len(archived) - 1; i >= 0; i-- {
		refNames = append(refNames, plumbing.ReferenceName(paths.V2FullRefPrefix+archived[i]))
	}
	return refNames
}

// renameRawTranscriptArtifactsToLegacyNames rewrites a single session inside
// a /full/* ref so its raw_transcript[/.NNN] and raw_transcript_hash.txt
// entries are renamed to the pre-rename filenames (full.jsonl[/.NNN] /
// content_hash.txt). Used to simulate archived generations written before
// commit a3cd77122.
// readRawTranscriptBlobHashesForTest returns the blob hashes of the
// raw_transcript and raw_transcript_hash.txt files for one session in one
// /full/* ref. Fails the test if either file is missing.
func readRawTranscriptBlobHashesForTest(t *testing.T, repo *git.Repository, v2Store *checkpoint.V2GitStore, refName plumbing.ReferenceName, cpID id.CheckpointID, sessionIdx int) (transcript, hash plumbing.Hash) {
	t.Helper()

	_, rootTreeHash, err := v2Store.GetRefState(refName)
	require.NoError(t, err)
	rootTree, err := repo.TreeObject(rootTreeHash)
	require.NoError(t, err)
	sessionTree, err := rootTree.Tree(cpID.Path() + "/" + strconv.Itoa(sessionIdx))
	require.NoError(t, err)

	for _, entry := range sessionTree.Entries {
		switch entry.Name {
		case paths.V2RawTranscriptFileName:
			transcript = entry.Hash
		case paths.V2RawTranscriptHashFileName:
			hash = entry.Hash
		}
	}
	require.NotEqual(t, plumbing.ZeroHash, transcript, "raw_transcript should exist for session %d in %s", sessionIdx, refName)
	require.NotEqual(t, plumbing.ZeroHash, hash, "raw_transcript_hash.txt should exist for session %d in %s", sessionIdx, refName)
	return transcript, hash
}

// sessionHasNewNamingForTest reports whether one session subtree carries
// the current naming convention (raw_transcript + raw_transcript_hash.txt)
// and not the pre-rename names (full.jsonl / content_hash.txt).
func sessionHasNewNamingForTest(t *testing.T, repo *git.Repository, v2Store *checkpoint.V2GitStore, refName plumbing.ReferenceName, cpID id.CheckpointID, sessionIdx int) bool {
	t.Helper()

	_, rootTreeHash, err := v2Store.GetRefState(refName)
	require.NoError(t, err)
	rootTree, err := repo.TreeObject(rootTreeHash)
	require.NoError(t, err)
	sessionTree, err := rootTree.Tree(cpID.Path() + "/" + strconv.Itoa(sessionIdx))
	require.NoError(t, err)

	hasNewTranscript := false
	hasNewHash := false
	hasLegacyTranscript := false
	hasLegacyHash := false
	for _, entry := range sessionTree.Entries {
		switch {
		case entry.Name == paths.V2RawTranscriptFileName,
			strings.HasPrefix(entry.Name, paths.V2RawTranscriptFileName+"."):
			hasNewTranscript = true
		case entry.Name == paths.V2RawTranscriptHashFileName:
			hasNewHash = true
		case entry.Name == paths.TranscriptFileName,
			strings.HasPrefix(entry.Name, paths.TranscriptFileName+"."):
			hasLegacyTranscript = true
		case entry.Name == paths.ContentHashFileName:
			hasLegacyHash = true
		}
	}
	return hasNewTranscript && hasNewHash && !hasLegacyTranscript && !hasLegacyHash
}

func renameRawTranscriptArtifactsToLegacyNames(t *testing.T, repo *git.Repository, v2Store *checkpoint.V2GitStore, refName plumbing.ReferenceName, cpID id.CheckpointID, sessionIdx int) {
	t.Helper()

	parentHash, rootTreeHash, err := v2Store.GetRefState(refName)
	require.NoError(t, err)

	rootTree, err := repo.TreeObject(rootTreeHash)
	require.NoError(t, err)

	sessionPath := cpID.Path() + "/" + strconv.Itoa(sessionIdx)
	sessionTree, err := rootTree.Tree(sessionPath)
	require.NoError(t, err)

	var renamedEntries []object.TreeEntry
	var deleteNames []string
	for _, entry := range sessionTree.Entries {
		switch {
		case entry.Name == paths.V2RawTranscriptFileName:
			renamedEntries = append(renamedEntries, object.TreeEntry{
				Name: paths.TranscriptFileName,
				Mode: entry.Mode,
				Hash: entry.Hash,
			})
			deleteNames = append(deleteNames, entry.Name)
		case strings.HasPrefix(entry.Name, paths.V2RawTranscriptFileName+"."):
			suffix := strings.TrimPrefix(entry.Name, paths.V2RawTranscriptFileName)
			renamedEntries = append(renamedEntries, object.TreeEntry{
				Name: paths.TranscriptFileName + suffix,
				Mode: entry.Mode,
				Hash: entry.Hash,
			})
			deleteNames = append(deleteNames, entry.Name)
		case entry.Name == paths.V2RawTranscriptHashFileName:
			renamedEntries = append(renamedEntries, object.TreeEntry{
				Name: paths.ContentHashFileName,
				Mode: entry.Mode,
				Hash: entry.Hash,
			})
			deleteNames = append(deleteNames, entry.Name)
		}
	}
	require.NotEmpty(t, renamedEntries, "session must have raw_transcript artifacts to rename")

	newRootHash, err := checkpoint.UpdateSubtree(
		repo,
		rootTreeHash,
		[]string{string(cpID[:2]), string(cpID[2:]), strconv.Itoa(sessionIdx)},
		renamedEntries,
		checkpoint.UpdateSubtreeOptions{
			MergeMode:   checkpoint.MergeKeepExisting,
			DeleteNames: deleteNames,
		},
	)
	require.NoError(t, err)

	commitHash, err := checkpoint.CreateCommit(context.Background(), repo, newRootHash, parentHash, "test: rename to legacy names\n", "Test", "test@test.com")
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(refName, commitHash)))
}

func hasCurrentFullSessionArtifactsForTest(t *testing.T, repo *git.Repository, v2Store *checkpoint.V2GitStore, cpID id.CheckpointID, sessionIdx int) bool {
	t.Helper()

	_, rootTreeHash, err := v2Store.GetRefState(plumbing.ReferenceName(paths.V2FullCurrentRefName))
	require.NoError(t, err)

	rootTree, err := repo.TreeObject(rootTreeHash)
	require.NoError(t, err)

	sessionPath := cpID.Path() + "/" + strconv.Itoa(sessionIdx)
	sessionTree, err := rootTree.Tree(sessionPath)
	if err != nil {
		return false
	}

	hasTranscript := false
	for _, entry := range sessionTree.Entries {
		if entry.Name == paths.V2RawTranscriptFileName || strings.HasPrefix(entry.Name, paths.V2RawTranscriptFileName+".") {
			hasTranscript = true
			break
		}
	}
	if !hasTranscript {
		return false
	}

	_, err = sessionTree.File(paths.V2RawTranscriptHashFileName)
	return err == nil
}

func TestBuildMigrateWriteOpts_PromptSeparatorRoundTrip(t *testing.T) {
	t.Parallel()

	cpID := id.MustCheckpointID("123456abcdef")
	rawPrompts := strings.Join([]string{
		"first line\nwith newline",
		"second prompt",
	}, checkpoint.PromptSeparator)

	opts := buildMigrateWriteOpts(&checkpoint.SessionContent{
		Metadata: checkpoint.CommittedMetadata{
			SessionID: "session-prompts-001",
			Strategy:  "manual-commit",
		},
		Prompts: rawPrompts,
	}, checkpoint.CommittedInfo{
		CheckpointID: cpID,
	}, nil)

	require.Len(t, opts.Prompts, 2)
	assert.Equal(t, "first line\nwith newline", opts.Prompts[0])
	assert.Equal(t, "second prompt", opts.Prompts[1])
}

func TestLatestMigratedV2SessionIndex_Empty(t *testing.T) {
	t.Parallel()

	latest, ok := latestMigratedV2SessionIndex(nil)
	assert.Equal(t, -1, latest)
	assert.False(t, ok)
}

func TestMigrateCheckpointsV2_PreservesPromptAttributions(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("aabb22334455")
	promptAttrs := json.RawMessage(`[{"prompt_index":0,"user_lines":["main.go:10"]}]`)

	err := v1Store.WriteCommitted(ctx, checkpoint.WriteCommittedOptions{
		CheckpointID:           cpID,
		SessionID:              "session-pa-001",
		Strategy:               "manual-commit",
		Transcript:             redact.AlreadyRedacted([]byte("{\"type\":\"assistant\",\"message\":\"pa test\"}\n")),
		Prompts:                []string{"test prompt"},
		PromptAttributionsJSON: promptAttrs,
		AuthorName:             "Test",
		AuthorEmail:            "test@test.com",
	})
	require.NoError(t, err)

	// Verify v1 has prompt_attributions
	v1Content, err := v1Store.ReadSessionContent(ctx, cpID, 0)
	require.NoError(t, err)
	require.NotNil(t, v1Content.Metadata.PromptAttributions, "v1 should have prompt_attributions")

	// Migrate
	var stdout bytes.Buffer
	result, err := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.migrated)

	// Read v2 session metadata from /main ref and verify prompt_attributions preserved
	v2MainRef, err := repo.Reference(plumbing.ReferenceName(paths.V2MainRefName), true)
	require.NoError(t, err)
	v2MainCommit, err := repo.CommitObject(v2MainRef.Hash())
	require.NoError(t, err)
	v2MainTree, err := v2MainCommit.Tree()
	require.NoError(t, err)

	metadataFile, err := v2MainTree.File(cpID.Path() + "/0/" + paths.MetadataFileName)
	require.NoError(t, err)
	metadataContent, err := metadataFile.Contents()
	require.NoError(t, err)

	var metadata checkpoint.CommittedMetadata
	require.NoError(t, json.Unmarshal([]byte(metadataContent), &metadata))
	assert.JSONEq(t, string(promptAttrs), string(metadata.PromptAttributions),
		"v2 session metadata should preserve prompt_attributions from v1")
}

func TestMigrateCheckpointsV2_PreservesCombinedAttribution(t *testing.T) {
	t.Parallel()
	repo := initMigrateTestRepo(t)
	v1Store, v2Store := newMigrateStores(repo)
	ctx := context.Background()

	cpID := id.MustCheckpointID("ccdd55667788")

	// Write two sessions so combined attribution is meaningful
	writeV1Checkpoint(t, v1Store, cpID, "session-ca-001",
		[]byte("{\"type\":\"assistant\",\"message\":\"session 1\"}\n"),
		[]string{"prompt 1"},
	)
	writeV1Checkpoint(t, v1Store, cpID, "session-ca-002",
		[]byte("{\"type\":\"assistant\",\"message\":\"session 2\"}\n"),
		[]string{"prompt 2"},
	)

	// Inject CombinedAttribution into v1 root summary
	combined := &checkpoint.InitialAttribution{
		CalculatedAt:      time.Date(2026, 4, 15, 0, 18, 47, 0, time.UTC),
		AgentLines:        119,
		AgentRemoved:      94,
		HumanAdded:        3,
		HumanModified:     0,
		HumanRemoved:      1,
		TotalCommitted:    122,
		TotalLinesChanged: 217,
		AgentPercentage:   98.15668202764977,
		MetricVersion:     2,
	}
	err := v1Store.UpdateCheckpointSummary(ctx, cpID, combined)
	require.NoError(t, err)

	// Verify v1 root summary has CombinedAttribution
	v1Summary, err := v1Store.ReadCommitted(ctx, cpID)
	require.NoError(t, err)
	require.NotNil(t, v1Summary.CombinedAttribution, "v1 should have combined_attribution")

	// Migrate
	var stdout bytes.Buffer
	result, err := migrateCheckpointsV2(ctx, repo, v1Store, v2Store, &stdout, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.migrated)

	// Read v2 root summary and verify CombinedAttribution preserved
	v2Summary, err := v2Store.ReadCommitted(ctx, cpID)
	require.NoError(t, err)
	require.NotNil(t, v2Summary)
	require.NotNil(t, v2Summary.CombinedAttribution,
		"v2 root summary should preserve combined_attribution from v1")
	assert.Equal(t, combined.CalculatedAt, v2Summary.CombinedAttribution.CalculatedAt)
	assert.Equal(t, combined.AgentLines, v2Summary.CombinedAttribution.AgentLines)
	assert.Equal(t, combined.AgentRemoved, v2Summary.CombinedAttribution.AgentRemoved)
	assert.Equal(t, combined.HumanAdded, v2Summary.CombinedAttribution.HumanAdded)
	assert.Equal(t, combined.HumanModified, v2Summary.CombinedAttribution.HumanModified)
	assert.Equal(t, combined.HumanRemoved, v2Summary.CombinedAttribution.HumanRemoved)
	assert.Equal(t, combined.TotalCommitted, v2Summary.CombinedAttribution.TotalCommitted)
	assert.Equal(t, combined.TotalLinesChanged, v2Summary.CombinedAttribution.TotalLinesChanged)
	assert.InDelta(t, combined.AgentPercentage, v2Summary.CombinedAttribution.AgentPercentage, 0.001)
	assert.Equal(t, combined.MetricVersion, v2Summary.CombinedAttribution.MetricVersion)
}

func TestSortMigratableCheckpoints(t *testing.T) {
	t.Parallel()

	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		input []checkpoint.CommittedInfo
		want  []id.CheckpointID
	}{
		{
			name: "chronological order",
			input: []checkpoint.CommittedInfo{
				{CheckpointID: id.MustCheckpointID("000000000003"), CreatedAt: t3},
				{CheckpointID: id.MustCheckpointID("000000000001"), CreatedAt: t1},
				{CheckpointID: id.MustCheckpointID("000000000002"), CreatedAt: t2},
			},
			want: []id.CheckpointID{
				id.MustCheckpointID("000000000001"),
				id.MustCheckpointID("000000000002"),
				id.MustCheckpointID("000000000003"),
			},
		},
		{
			name: "ties on CreatedAt break by checkpoint ID",
			input: []checkpoint.CommittedInfo{
				{CheckpointID: id.MustCheckpointID("0000000000bb"), CreatedAt: t1},
				{CheckpointID: id.MustCheckpointID("0000000000aa"), CreatedAt: t1},
				{CheckpointID: id.MustCheckpointID("0000000000cc"), CreatedAt: t1},
			},
			want: []id.CheckpointID{
				id.MustCheckpointID("0000000000aa"),
				id.MustCheckpointID("0000000000bb"),
				id.MustCheckpointID("0000000000cc"),
			},
		},
		{
			name: "zero CreatedAt sorts after non-zero, ties by ID",
			input: []checkpoint.CommittedInfo{
				{CheckpointID: id.MustCheckpointID("0000000000aa")},
				{CheckpointID: id.MustCheckpointID("000000000002"), CreatedAt: t2},
				{CheckpointID: id.MustCheckpointID("0000000000bb")},
				{CheckpointID: id.MustCheckpointID("000000000001"), CreatedAt: t1},
			},
			want: []id.CheckpointID{
				id.MustCheckpointID("000000000001"),
				id.MustCheckpointID("000000000002"),
				id.MustCheckpointID("0000000000aa"),
				id.MustCheckpointID("0000000000bb"),
			},
		},
		{
			name: "all-zero CreatedAt sorts by ID",
			input: []checkpoint.CommittedInfo{
				{CheckpointID: id.MustCheckpointID("0000000000cc")},
				{CheckpointID: id.MustCheckpointID("0000000000aa")},
				{CheckpointID: id.MustCheckpointID("0000000000bb")},
			},
			want: []id.CheckpointID{
				id.MustCheckpointID("0000000000aa"),
				id.MustCheckpointID("0000000000bb"),
				id.MustCheckpointID("0000000000cc"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := make([]checkpoint.CommittedInfo, len(tt.input))
			copy(input, tt.input)
			sortMigratableCheckpoints(input)
			got := make([]id.CheckpointID, len(input))
			for i, c := range input {
				got[i] = c.CheckpointID
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
