//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/go-git/go-git/v5/plumbing"
)

// TestWorktreeCommitPersistence verifies that commits made via go-git
// in a linked worktree are actually persisted and visible to git CLI.
//
// This is a regression test for the EnableDotGitCommonDir fix.
// Without that fix, go-git commits silently fail in worktrees.
//
// NOTE: This test uses os.Chdir() so it cannot use t.Parallel().
func TestWorktreeCommitPersistence(t *testing.T) {
	// Only test auto-commit strategy - it creates commits on the working branch
	worktreeStrategies := []string{
		strategy.StrategyNameAutoCommit,
	}

	RunForStrategiesSequential(t, worktreeStrategies, func(t *testing.T, strat string) {
		env := NewTestEnv(t)
		env.InitRepo()
		env.InitEntire(strat)

		env.WriteFile("README.md", "# Main Repo")
		env.GitAdd("README.md")
		env.GitCommit("Initial commit")

		// Create a worktree
		worktreeDir := filepath.Join(t.TempDir(), "worktree")
		if resolved, err := filepath.EvalSymlinks(filepath.Dir(worktreeDir)); err == nil {
			worktreeDir = filepath.Join(resolved, "worktree")
		}

		cmd := exec.Command("git", "worktree", "add", worktreeDir, "-b", "worktree-branch")
		cmd.Dir = env.RepoDir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed to create worktree: %v\nOutput: %s", err, output)
		}

		// Initialize .entire in worktree
		worktreeEntireDir := filepath.Join(worktreeDir, ".entire")
		if err := os.MkdirAll(worktreeEntireDir, 0o755); err != nil {
			t.Fatalf("failed to create .entire in worktree: %v", err)
		}
		settingsSrc := filepath.Join(env.RepoDir, ".entire", paths.SettingsFileName)
		settingsDst := filepath.Join(worktreeEntireDir, paths.SettingsFileName)
		settingsData, err := os.ReadFile(settingsSrc)
		if err != nil {
			t.Fatalf("failed to read settings: %v", err)
		}
		if err := os.WriteFile(settingsDst, settingsData, 0o644); err != nil {
			t.Fatalf("failed to write settings to worktree: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(worktreeEntireDir, "tmp"), 0o755); err != nil {
			t.Fatalf("failed to create tmp dir: %v", err)
		}

		// Change to worktree directory
		originalWd, _ := os.Getwd()
		if err := os.Chdir(worktreeDir); err != nil {
			t.Fatalf("failed to chdir to worktree: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Chdir(originalWd)
		})

		// Create a file in the worktree
		testFile := filepath.Join(worktreeDir, "worktree-file.txt")
		if err := os.WriteFile(testFile, []byte("worktree content"), 0o644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		// Create a HookRunner pointing to the worktree
		runner := NewHookRunner(worktreeDir, env.ClaudeProjectDir, t)

		// Simulate a session that creates a commit
		sessionID := "worktree-test-session"
		transcriptPath := filepath.Join(worktreeEntireDir, "tmp", sessionID+".jsonl")

		builder := NewTranscriptBuilder()
		builder.AddUserMessage("Add worktree file")
		builder.AddAssistantMessage("I'll add the file.")
		toolID := builder.AddToolUse("mcp__acp__Write", "worktree-file.txt", "worktree content")
		builder.AddToolResult(toolID)
		builder.AddAssistantMessage("Done!")
		if err := builder.WriteToFile(transcriptPath); err != nil {
			t.Fatalf("failed to write transcript: %v", err)
		}

		if err := runner.SimulateUserPromptSubmit(sessionID); err != nil {
			t.Fatalf("SimulateUserPromptSubmit failed: %v", err)
		}

		if err := runner.SimulateStop(sessionID, transcriptPath); err != nil {
			t.Fatalf("SimulateStop failed: %v", err)
		}

		// CRITICAL: Verify commit persisted using git CLI (not go-git)
		gitLogCmd := exec.Command("git", "log", "--oneline", "-5")
		gitLogCmd.Dir = worktreeDir
		logOutput, err := gitLogCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git log failed: %v\nOutput: %s", err, logOutput)
		}

		logLines := strings.Split(strings.TrimSpace(string(logOutput)), "\n")
		if len(logLines) < 2 {
			t.Errorf("expected at least 2 commits (initial + session), got %d:\n%s",
				len(logLines), logOutput)
		}

		// Verify git status shows clean working tree
		gitStatusCmd := exec.Command("git", "status", "--porcelain")
		gitStatusCmd.Dir = worktreeDir
		statusOutput, err := gitStatusCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git status failed: %v\nOutput: %s", err, statusOutput)
		}

		if strings.Contains(string(statusOutput), "worktree-file.txt") {
			t.Errorf("worktree-file.txt still appears in git status (commit didn't persist):\n%s",
				statusOutput)
		}

		t.Logf("Worktree commit test passed for strategy %s", strat)
		t.Logf("Git log:\n%s", logOutput)
	})
}

// TestWorktreeOpenRepository verifies that OpenRepository() works correctly
// in a worktree context by checking it can read HEAD and refs.
//
// NOTE: This test uses os.Chdir() so it cannot use t.Parallel().
func TestWorktreeOpenRepository(t *testing.T) {
	env := NewTestEnv(t)
	env.InitRepo()

	env.WriteFile("README.md", "# Test")
	env.GitAdd("README.md")
	env.GitCommit("Initial commit")

	worktreeDir := filepath.Join(t.TempDir(), "worktree")
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(worktreeDir)); err == nil {
		worktreeDir = filepath.Join(resolved, "worktree")
	}

	cmd := exec.Command("git", "worktree", "add", worktreeDir, "-b", "test-branch")
	cmd.Dir = env.RepoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create worktree: %v\nOutput: %s", err, output)
	}

	originalWd, _ := os.Getwd()
	if err := os.Chdir(worktreeDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWd)
	})

	repo, err := strategy.OpenRepository()
	if err != nil {
		t.Fatalf("OpenRepository() failed in worktree: %v", err)
	}

	head, err := repo.Head()
	if err != nil {
		t.Fatalf("repo.Head() failed: %v", err)
	}

	if head.Name().Short() != "test-branch" {
		t.Errorf("expected HEAD to be test-branch, got %s", head.Name().Short())
	}

	refs, err := repo.References()
	if err != nil {
		t.Fatalf("repo.References() failed: %v", err)
	}

	refCount := 0
	_ = refs.ForEach(func(ref *plumbing.Reference) error {
		refCount++
		return nil
	})

	if refCount == 0 {
		t.Error("expected to find refs, but found none")
	}

	t.Logf("Successfully opened worktree repo, HEAD=%s, found %d refs",
		head.Name().Short(), refCount)
}
