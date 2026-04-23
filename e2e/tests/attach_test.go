//go:build e2e

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/entireio/cli/e2e/agents"
	"github.com/entireio/cli/e2e/entire"
	"github.com/entireio/cli/e2e/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttachSessionCreatesCheckpoint(t *testing.T) {
	testutil.ForEachAgent(t, 2*time.Minute, func(t *testing.T, s *testutil.RepoState, ctx context.Context) {
		if s.Agent.Name() != "vogon" {
			t.Skip("attach E2E coverage currently uses the deterministic vogon session storage")
		}

		homeDir := t.TempDir()
		extraEnv := []string{"HOME=" + homeDir}
		vogon := requireVogonAgent(t, s)

		mainBranch := testutil.GitOutput(t, s.Dir, "branch", "--show-current")
		s.Git(t, "add", ".")
		s.Git(t, "commit", "-m", "Enable entire")
		s.Git(t, "checkout", "-b", "feature")

		sessionID := "attach-vogon-session"
		_, writeErr := vogon.WriteSessionTranscript(ctx, s.Dir, extraEnv, sessionID, "explain the feature module", "The feature module organizes related work.")
		require.NoError(t, writeErr, "prepare vogon session")

		out, err := entire.AttachWithEnv(s.Dir, extraEnv, sessionID, s.Agent.EntireAgent())
		require.NoError(t, err, "entire attach failed: %s", out)

		assert.Contains(t, out, "Attached session "+sessionID)
		assert.Contains(t, out, "Created checkpoint")
		checkpointID := testutil.AssertHasCheckpointTrailer(t, s.Dir, "HEAD")

		resumeOut, resumeErr := entire.Resume(s.Dir, "feature")
		require.NoError(t, resumeErr, "entire resume failed after attach: %s", resumeOut)
		assert.Contains(t, resumeOut, sessionID, "resume output should reference the attached session")
		assert.Contains(t, resumeOut, "To continue", "resume output should show follow-up instructions")

		s.Git(t, "checkout", mainBranch)
		explainOut := entire.Explain(t, s.Dir, checkpointID)
		assert.Contains(t, explainOut, "Checkpoint: "+checkpointID)
	})
}

func TestAttachSessionAddsToExistingCheckpoint(t *testing.T) {
	testutil.ForEachAgent(t, 3*time.Minute, func(t *testing.T, s *testutil.RepoState, ctx context.Context) {
		if s.Agent.Name() != "vogon" {
			t.Skip("attach E2E coverage currently uses the deterministic vogon session storage")
		}

		homeDir := t.TempDir()
		extraEnv := []string{"HOME=" + homeDir}
		vogon := requireVogonAgent(t, s)

		s.Git(t, "add", ".")
		s.Git(t, "commit", "-m", "Enable entire")

		_, err := s.RunPrompt(t, ctx,
			"create a file at docs/existing.md with a short paragraph about existing checkpoints. Do not ask for confirmation or approval, just make the change.")
		require.NoError(t, err, "agent failed")

		checkpointBefore := ""
		if _, refErr := testutil.GitOutputErr(s.Dir, "rev-parse", "--verify", testutil.CheckpointMetadataRef()); refErr == nil {
			checkpointBefore = testutil.CurrentCheckpointRef(t, s.Dir)
		}

		s.Git(t, "add", ".")
		s.Git(t, "commit", "-m", "Add existing checkpoint doc")
		testutil.WaitForCheckpointAdvanceFrom(t, s.Dir, checkpointBefore, 30*time.Second)

		checkpointID := testutil.AssertHasCheckpointTrailer(t, s.Dir, "HEAD")

		sessionID := "attach-second-vogon-session"
		_, writeErr := vogon.WriteSessionTranscript(ctx, s.Dir, extraEnv, sessionID, "summarize the checkpoint flow", "The checkpoint flow stores the session on the existing checkpoint.")
		require.NoError(t, writeErr, "prepare vogon session")

		out, attachErr := entire.AttachWithEnv(s.Dir, extraEnv, sessionID, s.Agent.EntireAgent())
		require.NoError(t, attachErr, "entire attach failed: %s", out)

		assert.Contains(t, out, "Attached session "+sessionID)
		assert.Contains(t, out, "Added to existing checkpoint "+checkpointID)
		assert.Equal(t, checkpointID, testutil.AssertHasCheckpointTrailer(t, s.Dir, "HEAD"),
			"attach should reuse the existing checkpoint trailer")
	})
}

func requireVogonAgent(t *testing.T, s *testutil.RepoState) *agents.Vogon {
	t.Helper()

	vogon, ok := s.Agent.(*agents.Vogon)
	require.True(t, ok, "expected Vogon agent")
	return vogon
}
