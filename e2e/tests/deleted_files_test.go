//go:build e2e

package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/e2e/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeletedFilesCommitDeletion tests that deleting a file that was tracked
// in the session gets handled properly when committed via git rm.
func TestDeletedFilesCommitDeletion(t *testing.T) {
	testutil.ForEachAgent(t, 3*time.Minute, func(t *testing.T, s *testutil.RepoState, ctx context.Context) {
		require.NoError(t, os.WriteFile(
			filepath.Join(s.Dir, "to_delete.go"),
			[]byte("package main\n\nfunc ToDelete() {}\n"), 0o644))
		s.Git(t, "add", "to_delete.go")
		s.Git(t, "commit", "--no-verify", "-m", "Add to_delete.go")

		_, err := s.RunPrompt(t, ctx,
			"Do two things: (1) Delete the file to_delete.go using rm. "+
				"(2) Create a new file replacement.go with content 'package main; func Replacement() {}'. "+
				"Do both tasks. Do not commit. "+
				"Do not ask for confirmation, just make the changes.")
		if err != nil {
			t.Fatalf("agent failed: %v", err)
		}

		assert.NoFileExists(t, filepath.Join(s.Dir, "to_delete.go"))
		testutil.AssertFileExists(t, s.Dir, "replacement.go")

		s.Git(t, "add", "replacement.go")
		s.Git(t, "commit", "-m", "Add replacement")
		testutil.WaitForCheckpoint(t, s, 15*time.Second)
		cpID1 := testutil.AssertHasCheckpointTrailer(t, s.Dir, "HEAD")
		testutil.AssertCheckpointExists(t, s.Dir, cpID1)

		cpBranchAfterFirst := testutil.GitOutput(t, s.Dir, "rev-parse", "entire/checkpoints/v1")

		s.Git(t, "rm", "to_delete.go")
		s.Git(t, "commit", "-m", "Remove to_delete.go")

		time.Sleep(5 * time.Second)
		cpBranchAfterDeletion := testutil.GitOutput(t, s.Dir, "rev-parse", "entire/checkpoints/v1")
		if cpBranchAfterDeletion != cpBranchAfterFirst {
			cpID2 := testutil.AssertHasCheckpointTrailer(t, s.Dir, "HEAD")
			assert.NotEqual(t, cpID1, cpID2, "checkpoint IDs should be distinct")
			t.Logf("deletion commit got checkpoint %s (carry-forward)", cpID2)
		} else {
			t.Log("deletion commit has no checkpoint (deleted files may not carry forward)")
		}
		testutil.AssertNoShadowBranches(t, s.Dir)
	})
}
