package strategy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/session"

	"github.com/go-git/go-git/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests in this file use t.Chdir and t.Setenv (process-global state),
// so they cannot use t.Parallel().

func TestCondenseSession_SkipsWhenNoTranscriptAndNoFiles(t *testing.T) {
	dir := setupGitRepo(t)
	t.Chdir(dir)

	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)

	s := &ManualCommitStrategy{}
	checkpointID := id.MustCheckpointID("a1b2c3d4e5f6")

	// Create a session with no transcript path and no files touched
	state := &SessionState{
		SessionID:  "test-skip-session",
		AgentType:  "Codex",
		BaseCommit: getHeadHash(t, repo),
		Phase:      session.PhaseActive,
	}

	result, err := s.CondenseSession(context.Background(), repo, checkpointID, state, nil)
	require.NoError(t, err)
	assert.True(t, result.Skipped, "should skip when no transcript and no files")
	assert.Equal(t, checkpointID, result.CheckpointID)
	assert.Equal(t, "test-skip-session", result.SessionID)
}

func TestCondenseSession_ResolvesTranscriptFromAgentStorage(t *testing.T) {
	dir := setupGitRepo(t)
	t.Chdir(dir)

	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)

	// Create a fake Codex transcript in a temp directory
	codexSessionDir := t.TempDir()
	sessionID := "019daaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	transcriptFile := filepath.Join(codexSessionDir, sessionID+".jsonl")
	transcriptContent := []byte(`{"type":"message","role":"user","content":"hello"}` + "\n")
	require.NoError(t, os.WriteFile(transcriptFile, transcriptContent, 0o644))

	// Set ENTIRE_TEST_CODEX_SESSION_DIR so the Codex agent resolves to our temp dir
	t.Setenv("ENTIRE_TEST_CODEX_SESSION_DIR", codexSessionDir)

	s := &ManualCommitStrategy{}
	checkpointID := id.MustCheckpointID("b2c3d4e5f6a1")

	state := &SessionState{
		SessionID:  sessionID,
		AgentType:  "Codex",
		BaseCommit: getHeadHash(t, repo),
		Phase:      session.PhaseActive,
	}

	result, err := s.CondenseSession(context.Background(), repo, checkpointID, state, nil)
	require.NoError(t, err)
	assert.False(t, result.Skipped, "should not skip when transcript is resolved from agent storage")
	assert.Equal(t, transcriptFile, state.TranscriptPath, "should update TranscriptPath after resolution")
}

func TestCondenseSession_DoesNotSkipWhenFilesTouchedButNoTranscript(t *testing.T) {
	dir := setupGitRepo(t)
	t.Chdir(dir)

	repo, err := git.PlainOpen(dir)
	require.NoError(t, err)

	s := &ManualCommitStrategy{}
	checkpointID := id.MustCheckpointID("c3d4e5f6a1b2")

	// Modify and commit a file so committedFiles has content
	testFile := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("modified"), 0o644))
	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("test.txt")
	require.NoError(t, err)
	commitHash, err := wt.Commit("modify test.txt", &git.CommitOptions{})
	require.NoError(t, err)

	state := &SessionState{
		SessionID:    "test-files-no-transcript",
		AgentType:    "Claude Code",
		BaseCommit:   commitHash.String(),
		Phase:        session.PhaseActive,
		FilesTouched: []string{"test.txt"},
	}

	committedFiles := map[string]struct{}{"test.txt": {}}
	result, err := s.CondenseSession(context.Background(), repo, checkpointID, state, committedFiles)
	require.NoError(t, err)
	assert.False(t, result.Skipped, "should not skip when files are touched even without transcript")
}

// getHeadHash returns the HEAD commit hash as a string.
func getHeadHash(t *testing.T, repo *git.Repository) string {
	t.Helper()
	head, err := repo.Head()
	require.NoError(t, err)
	return head.Hash().String()
}

// --- Unit tests for resolveTranscriptFromAgentStorage ---

// mockPreparerAgent is a mock agent that implements TranscriptPreparer.
// When PrepareTranscript is called, it writes transcriptContent to the resolved path.
type mockPreparerAgent struct {
	sessionDir        string
	transcriptContent []byte
	prepareErr        error
}

func (m *mockPreparerAgent) Name() types.AgentName                        { return "mock" }
func (m *mockPreparerAgent) Type() types.AgentType                        { return "Mock" }
func (m *mockPreparerAgent) Description() string                          { return "" }
func (m *mockPreparerAgent) IsPreview() bool                              { return false }
func (m *mockPreparerAgent) DetectPresence(context.Context) (bool, error) { return false, nil }
func (m *mockPreparerAgent) GetSessionID(*agent.HookInput) string         { return "" }
func (m *mockPreparerAgent) ReadSession(*agent.HookInput) (*agent.AgentSession, error) {
	return nil, errors.New("not implemented") //nolint:goerr113 // test mock
}
func (m *mockPreparerAgent) WriteSession(context.Context, *agent.AgentSession) error { return nil }
func (m *mockPreparerAgent) FormatResumeCommand(string) string                       { return "" }
func (m *mockPreparerAgent) ProtectedDirs() []string                                 { return nil }
func (m *mockPreparerAgent) ReadTranscript(string) ([]byte, error)                   { return nil, nil }
func (m *mockPreparerAgent) ChunkTranscript(context.Context, []byte, int) ([][]byte, error) {
	return nil, nil
}
func (m *mockPreparerAgent) ReassembleTranscript([][]byte) ([]byte, error) { return nil, nil }

func (m *mockPreparerAgent) GetSessionDir(string) (string, error) {
	return m.sessionDir, nil
}

func (m *mockPreparerAgent) ResolveSessionFile(sessionDir, agentSessionID string) string {
	return filepath.Join(sessionDir, agentSessionID+".jsonl")
}

func (m *mockPreparerAgent) PrepareTranscript(_ context.Context, sessionRef string) error {
	if m.prepareErr != nil {
		return m.prepareErr
	}
	// Simulate the agent producing the transcript file on disk
	return os.WriteFile(sessionRef, m.transcriptContent, 0o644)
}

func TestResolveTranscriptFromAgentStorage_CallsPrepareTranscript(t *testing.T) {
	dir := setupGitRepo(t)
	t.Chdir(dir)

	sessionDir := t.TempDir()
	transcriptContent := []byte(`{"type":"message","role":"user","content":"hello"}` + "\n")

	ag := &mockPreparerAgent{
		sessionDir:        sessionDir,
		transcriptContent: transcriptContent,
	}

	state := &SessionState{
		SessionID: "test-prepare-session",
		AgentType: "Mock",
	}

	data := resolveTranscriptFromAgentStorage(context.Background(), ag, state)
	assert.Equal(t, transcriptContent, data, "should return transcript produced by PrepareTranscript")
	assert.Equal(t, filepath.Join(sessionDir, "test-prepare-session.jsonl"), state.TranscriptPath,
		"should update TranscriptPath")
}

func TestResolveTranscriptFromAgentStorage_PrepareFailsButFileExists(t *testing.T) {
	dir := setupGitRepo(t)
	t.Chdir(dir)

	sessionDir := t.TempDir()
	transcriptContent := []byte(`{"type":"message","role":"user","content":"cached"}` + "\n")

	// Pre-create the file (simulating a previous export)
	transcriptPath := filepath.Join(sessionDir, "test-cached-session.jsonl")
	require.NoError(t, os.WriteFile(transcriptPath, transcriptContent, 0o644))

	ag := &mockPreparerAgent{
		sessionDir: sessionDir,
		prepareErr: errors.New("export command not found"),
	}

	state := &SessionState{
		SessionID: "test-cached-session",
		AgentType: "Mock",
	}

	data := resolveTranscriptFromAgentStorage(context.Background(), ag, state)
	assert.Equal(t, transcriptContent, data, "should read existing file even when PrepareTranscript fails")
}

func TestResolveTranscriptFromAgentStorage_PrepareFailsNoFile(t *testing.T) {
	dir := setupGitRepo(t)
	t.Chdir(dir)

	sessionDir := t.TempDir()

	ag := &mockPreparerAgent{
		sessionDir: sessionDir,
		prepareErr: errors.New("export command not found"),
	}

	state := &SessionState{
		SessionID: "test-no-file-session",
		AgentType: "Mock",
	}

	data := resolveTranscriptFromAgentStorage(context.Background(), ag, state)
	assert.Nil(t, data, "should return nil when PrepareTranscript fails and no file exists")
}
