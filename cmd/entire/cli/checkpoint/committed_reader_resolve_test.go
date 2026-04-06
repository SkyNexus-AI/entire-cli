package checkpoint

import (
	"context"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/stretchr/testify/require"
)

func TestResolveCommittedReaderForCheckpoint_UsesV2WhenFound(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	v1Store := NewGitStore(repo)
	v2Store := NewV2GitStore(repo, "origin")
	ctx := context.Background()
	cpID := id.MustCheckpointID("111111111111")

	require.NoError(t, v2Store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-v2",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"user","message":{"content":[{"type":"text","text":"hello"}]}}` + "\n"),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	}))

	reader, summary, err := ResolveCommittedReaderForCheckpoint(ctx, cpID, v1Store, v2Store, true)
	require.NoError(t, err)
	require.NotNil(t, summary)
	require.IsType(t, &V2GitStore{}, reader)
}

func TestResolveCommittedReaderForCheckpoint_FallsBackToV1WhenMissingInV2(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	v1Store := NewGitStore(repo)
	v2Store := NewV2GitStore(repo, "origin")
	ctx := context.Background()
	cpID := id.MustCheckpointID("222222222222")

	require.NoError(t, v1Store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-v1",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"user","message":{"content":[{"type":"text","text":"hello"}]}}` + "\n"),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	}))

	reader, summary, err := ResolveCommittedReaderForCheckpoint(ctx, cpID, v1Store, v2Store, true)
	require.NoError(t, err)
	require.NotNil(t, summary)
	require.IsType(t, &GitStore{}, reader)
}

func TestResolveCommittedReaderForCheckpoint_PrefersV1WhenV2Disabled(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	v1Store := NewGitStore(repo)
	v2Store := NewV2GitStore(repo, "origin")
	ctx := context.Background()
	cpID := id.MustCheckpointID("333333333333")

	require.NoError(t, v2Store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-v2",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"user","message":{"content":[{"type":"text","text":"hello"}]}}` + "\n"),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	}))

	require.NoError(t, v1Store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-v1",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"user","message":{"content":[{"type":"text","text":"hello"}]}}` + "\n"),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	}))

	reader, summary, err := ResolveCommittedReaderForCheckpoint(ctx, cpID, v1Store, v2Store, false)
	require.NoError(t, err)
	require.NotNil(t, summary)
	require.IsType(t, &GitStore{}, reader)
}

func TestResolveRawSessionLogForCheckpoint_UsesV2WhenFound(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	v1Store := NewGitStore(repo)
	v2Store := NewV2GitStore(repo, "origin")
	ctx := context.Background()
	cpID := id.MustCheckpointID("444444444444")

	require.NoError(t, v2Store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-v2",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"user","message":{"content":[{"type":"text","text":"from-v2"}]}}` + "\n"),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	}))

	logContent, sessionID, err := ResolveRawSessionLogForCheckpoint(ctx, cpID, v1Store, v2Store, true)
	require.NoError(t, err)
	require.Equal(t, "session-v2", sessionID)
	require.Contains(t, string(logContent), "from-v2")
}

func TestResolveRawSessionLogForCheckpoint_FallsBackToV1WhenMissingInV2(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	v1Store := NewGitStore(repo)
	v2Store := NewV2GitStore(repo, "origin")
	ctx := context.Background()
	cpID := id.MustCheckpointID("555555555555")

	require.NoError(t, v1Store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-v1",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"user","message":{"content":[{"type":"text","text":"from-v1"}]}}` + "\n"),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	}))

	logContent, sessionID, err := ResolveRawSessionLogForCheckpoint(ctx, cpID, v1Store, v2Store, true)
	require.NoError(t, err)
	require.Equal(t, "session-v1", sessionID)
	require.Contains(t, string(logContent), "from-v1")
}

func TestResolveRawSessionLogForCheckpoint_PrefersV1WhenV2Disabled(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	v1Store := NewGitStore(repo)
	v2Store := NewV2GitStore(repo, "origin")
	ctx := context.Background()
	cpID := id.MustCheckpointID("666666666666")

	require.NoError(t, v2Store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-v2",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"user","message":{"content":[{"type":"text","text":"from-v2"}]}}` + "\n"),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	}))

	require.NoError(t, v1Store.WriteCommitted(ctx, WriteCommittedOptions{
		CheckpointID: cpID,
		SessionID:    "session-v1",
		Strategy:     "manual-commit",
		Transcript:   []byte(`{"type":"user","message":{"content":[{"type":"text","text":"from-v1"}]}}` + "\n"),
		AuthorName:   "Test",
		AuthorEmail:  "test@test.com",
	}))

	logContent, sessionID, err := ResolveRawSessionLogForCheckpoint(ctx, cpID, v1Store, v2Store, false)
	require.NoError(t, err)
	require.Equal(t, "session-v1", sessionID)
	require.Contains(t, string(logContent), "from-v1")
}
