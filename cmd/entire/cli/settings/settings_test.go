package settings

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_RejectsUnknownKeys(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()

	// Create .entire directory
	entireDir := filepath.Join(tmpDir, ".entire")
	if err := os.MkdirAll(entireDir, 0755); err != nil {
		t.Fatalf("failed to create .entire directory: %v", err)
	}

	// Create settings.json with an unknown key
	settingsFile := filepath.Join(entireDir, "settings.json")
	settingsContent := `{"enabled": true, "unknown_key": "value"}`
	if err := os.WriteFile(settingsFile, []byte(settingsContent), 0644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	// Initialize a git repo (required by paths.AbsPath)
	if err := os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}

	// Change to the temp directory
	t.Chdir(tmpDir)

	// Try to load settings - should fail due to unknown key
	_, err := Load(context.Background())
	if err == nil {
		t.Error("expected error for unknown key, got nil")
	} else if !containsUnknownField(err.Error()) {
		t.Errorf("expected unknown field error, got: %v", err)
	}
}

func TestLoad_AcceptsValidKeys(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()

	// Create .entire directory
	entireDir := filepath.Join(tmpDir, ".entire")
	if err := os.MkdirAll(entireDir, 0755); err != nil {
		t.Fatalf("failed to create .entire directory: %v", err)
	}

	// Create settings.json with all valid keys
	settingsFile := filepath.Join(entireDir, "settings.json")
	settingsContent := `{
		"enabled": true,
		"local_dev": false,
		"log_level": "debug",
		"strategy_options": {"key": "value"},
		"telemetry": true
	}`
	if err := os.WriteFile(settingsFile, []byte(settingsContent), 0644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	// Initialize a git repo (required by paths.AbsPath)
	if err := os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}

	// Change to the temp directory
	t.Chdir(tmpDir)

	// Load settings - should succeed
	settings, err := Load(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify values
	if !settings.Enabled {
		t.Error("expected enabled to be true")
	}
	if settings.LogLevel != "debug" {
		t.Errorf("expected log_level 'debug', got %q", settings.LogLevel)
	}
	if settings.Telemetry == nil || !*settings.Telemetry {
		t.Error("expected telemetry to be true")
	}
}

func TestLoad_LocalSettingsRejectsUnknownKeys(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()

	// Create .entire directory
	entireDir := filepath.Join(tmpDir, ".entire")
	if err := os.MkdirAll(entireDir, 0755); err != nil {
		t.Fatalf("failed to create .entire directory: %v", err)
	}

	// Create valid settings.json
	settingsFile := filepath.Join(entireDir, "settings.json")
	settingsContent := `{"enabled": true}`
	if err := os.WriteFile(settingsFile, []byte(settingsContent), 0644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	// Create settings.local.json with an unknown key
	localSettingsFile := filepath.Join(entireDir, "settings.local.json")
	localSettingsContent := `{"bad_key": true}`
	if err := os.WriteFile(localSettingsFile, []byte(localSettingsContent), 0644); err != nil {
		t.Fatalf("failed to write local settings file: %v", err)
	}

	// Initialize a git repo (required by paths.AbsPath)
	if err := os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}

	// Change to the temp directory
	t.Chdir(tmpDir)

	// Try to load settings - should fail due to unknown key in local settings
	_, err := Load(context.Background())
	if err == nil {
		t.Error("expected error for unknown key in local settings, got nil")
	} else if !containsUnknownField(err.Error()) {
		t.Errorf("expected unknown field error, got: %v", err)
	}
}

func TestLoad_AcceptsDeprecatedStrategyField(t *testing.T) {
	tmpDir := t.TempDir()

	entireDir := filepath.Join(tmpDir, ".entire")
	if err := os.MkdirAll(entireDir, 0o755); err != nil {
		t.Fatalf("failed to create .entire directory: %v", err)
	}

	settingsFile := filepath.Join(entireDir, "settings.json")
	if err := os.WriteFile(settingsFile, []byte(`{"enabled": true, "strategy": "auto-commit"}`), 0o644); err != nil {
		t.Fatalf("failed to write settings file: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(tmpDir, ".git"), 0o755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}

	t.Chdir(tmpDir)

	s, err := Load(context.Background())
	if err != nil {
		t.Fatalf("expected no error for deprecated strategy field, got: %v", err)
	}
	if s.Strategy != "auto-commit" {
		t.Errorf("expected strategy 'auto-commit', got %q", s.Strategy)
	}
}

func TestFilesWithDeprecatedStrategy(t *testing.T) {
	tmpDir := t.TempDir()

	entireDir := filepath.Join(tmpDir, ".entire")
	if err := os.MkdirAll(entireDir, 0o755); err != nil {
		t.Fatalf("failed to create .entire directory: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(tmpDir, ".git"), 0o755); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}

	t.Chdir(tmpDir)

	// No strategy field → empty result
	if err := os.WriteFile(filepath.Join(entireDir, "settings.json"), []byte(`{"enabled": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if files := FilesWithDeprecatedStrategy(context.Background()); len(files) != 0 {
		t.Errorf("expected no deprecated files, got %v", files)
	}

	// Add strategy to project settings
	if err := os.WriteFile(filepath.Join(entireDir, "settings.json"), []byte(`{"enabled": true, "strategy": "auto-commit"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	files := FilesWithDeprecatedStrategy(context.Background())
	if len(files) != 1 || files[0] != EntireSettingsFile {
		t.Errorf("expected [%s], got %v", EntireSettingsFile, files)
	}

	// Also add strategy to local settings
	if err := os.WriteFile(filepath.Join(entireDir, "settings.local.json"), []byte(`{"strategy": "manual-commit"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	files = FilesWithDeprecatedStrategy(context.Background())
	if len(files) != 2 {
		t.Errorf("expected 2 deprecated files, got %v", files)
	}
}

// containsUnknownField checks if the error message indicates an unknown field
func containsUnknownField(msg string) bool {
	// Go's json package reports unknown fields with this message format
	return strings.Contains(msg, "unknown field")
}
