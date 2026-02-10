package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"
)

func TestInitHookLogging(t *testing.T) {
	// Create a temporary directory to simulate a git repo
	tmpDir := t.TempDir()

	// Change to temp dir (automatically restored after test)
	t.Chdir(tmpDir)

	// Initialize git repo (required for session state store to find .git common dir)
	gitInit := exec.CommandContext(context.Background(), "git", "init")
	gitInit.Dir = tmpDir
	if err := gitInit.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	t.Run("returns cleanup func when no session state exists", func(t *testing.T) {
		cleanup := initHookLogging()
		if cleanup == nil {
			t.Fatal("expected cleanup function, got nil")
		}
		cleanup() // Should not panic
	})

	t.Run("initializes logging when session state exists", func(t *testing.T) {
		// Create .entire directory
		entireDir := filepath.Join(tmpDir, paths.EntireDir)
		if err := os.MkdirAll(entireDir, 0o755); err != nil {
			t.Fatalf("failed to create .entire directory: %v", err)
		}

		// Create session state file in .git/entire-sessions/
		sessionID := "test-session-12345"
		stateDir := filepath.Join(tmpDir, ".git", session.SessionStateDirName)
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatalf("failed to create session state directory: %v", err)
		}

		now := time.Now()
		state := session.State{
			SessionID:           sessionID,
			StartedAt:           now,
			LastInteractionTime: &now,
			Phase:               session.PhaseActive,
		}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("failed to marshal state: %v", err)
		}
		stateFile := filepath.Join(stateDir, sessionID+".json")
		if err := os.WriteFile(stateFile, data, 0o600); err != nil {
			t.Fatalf("failed to write session state file: %v", err)
		}
		defer os.Remove(stateFile)

		// Create logs directory (logging.Init will try to create the log file)
		logsDir := filepath.Join(entireDir, "logs")
		if err := os.MkdirAll(logsDir, 0o755); err != nil {
			t.Fatalf("failed to create logs directory: %v", err)
		}

		cleanup := initHookLogging()
		if cleanup == nil {
			t.Fatal("expected cleanup function, got nil")
		}
		defer cleanup()

		// Verify log file was created
		logFile := filepath.Join(logsDir, "entire.log")
		if _, err := os.Stat(logFile); os.IsNotExist(err) {
			t.Errorf("expected log file to be created at %s", logFile)
		}
	})
}
