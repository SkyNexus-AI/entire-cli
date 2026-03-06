package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testOpPostCommit = "post-commit"

func TestParsePerfEntry(t *testing.T) {
	t.Parallel()

	t.Run("valid perf entry", func(t *testing.T) {
		t.Parallel()

		line := `{"time":"2026-01-15T10:30:00.000Z","level":"DEBUG","msg":"perf","component":"perf","op":"post-commit","duration_ms":150,"error":true,"steps.load_session_ms":50,"steps.save_checkpoint_ms":80,"steps.save_checkpoint_err":true}`

		entry := parsePerfEntry(line)
		if entry == nil {
			t.Fatal("parsePerfEntry returned nil for valid perf entry")
		}

		if entry.Op != testOpPostCommit {
			t.Errorf("Op = %q, want %q", entry.Op, testOpPostCommit)
		}
		if entry.DurationMs != 150 {
			t.Errorf("DurationMs = %d, want %d", entry.DurationMs, 150)
		}
		if !entry.Error {
			t.Error("Error = false, want true")
		}

		expectedTime, err := time.Parse(time.RFC3339, "2026-01-15T10:30:00.000Z")
		if err != nil {
			t.Fatalf("failed to parse expected time: %v", err)
		}
		if !entry.Time.Equal(expectedTime) {
			t.Errorf("Time = %v, want %v", entry.Time, expectedTime)
		}

		if len(entry.Steps) != 2 {
			t.Fatalf("len(Steps) = %d, want 2", len(entry.Steps))
		}

		// Steps are sorted alphabetically by name
		if entry.Steps[0].Name != "load_session" {
			t.Errorf("Steps[0].Name = %q, want %q", entry.Steps[0].Name, "load_session")
		}
		if entry.Steps[0].DurationMs != 50 {
			t.Errorf("Steps[0].DurationMs = %d, want %d", entry.Steps[0].DurationMs, 50)
		}
		if entry.Steps[0].Error {
			t.Error("Steps[0].Error = true, want false")
		}

		if entry.Steps[1].Name != "save_checkpoint" {
			t.Errorf("Steps[1].Name = %q, want %q", entry.Steps[1].Name, "save_checkpoint")
		}
		if entry.Steps[1].DurationMs != 80 {
			t.Errorf("Steps[1].DurationMs = %d, want %d", entry.Steps[1].DurationMs, 80)
		}
		if !entry.Steps[1].Error {
			t.Error("Steps[1].Error = false, want true")
		}
	})

	t.Run("non-perf entry returns nil", func(t *testing.T) {
		t.Parallel()

		line := `{"time":"2026-01-15T10:30:00.000Z","level":"INFO","msg":"hook invoked","component":"lifecycle","hook":"post-commit"}`

		entry := parsePerfEntry(line)
		if entry != nil {
			t.Errorf("parsePerfEntry returned %+v for non-perf entry, want nil", entry)
		}
	})

	t.Run("invalid JSON returns nil", func(t *testing.T) {
		t.Parallel()

		entry := parsePerfEntry("this is not json at all{{{")
		if entry != nil {
			t.Errorf("parsePerfEntry returned %+v for invalid JSON, want nil", entry)
		}
	})
}

func TestCollectPerfEntries(t *testing.T) {
	t.Parallel()

	// Fixture: 4 lines — 2 prepare-commit-msg, 1 non-perf, 1 post-commit
	fixtureLines := []string{
		`{"time":"2026-01-15T10:00:00Z","level":"DEBUG","msg":"perf","op":"prepare-commit-msg","duration_ms":100}`,
		`{"time":"2026-01-15T10:01:00Z","level":"DEBUG","msg":"perf","op":"prepare-commit-msg","duration_ms":120}`,
		`{"time":"2026-01-15T10:02:00Z","level":"INFO","msg":"hook invoked","component":"lifecycle","hook":"post-commit"}`,
		`{"time":"2026-01-15T10:03:00Z","level":"DEBUG","msg":"perf","op":"post-commit","duration_ms":200}`,
	}
	fixtureContent := strings.Join(fixtureLines, "\n") + "\n"

	writeFixture := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		p := filepath.Join(dir, "perf.jsonl")
		if err := os.WriteFile(p, []byte(fixtureContent), 0o644); err != nil {
			t.Fatalf("failed to write fixture: %v", err)
		}
		return p
	}

	t.Run("last 2 entries", func(t *testing.T) {
		t.Parallel()
		logFile := writeFixture(t)

		entries, err := collectPerfEntries(logFile, 2, "")
		if err != nil {
			t.Fatalf("collectPerfEntries returned error: %v", err)
		}

		if len(entries) != 2 {
			t.Fatalf("got %d entries, want 2", len(entries))
		}

		// Newest first: post-commit (line 4), then prepare-commit-msg (line 2)
		if entries[0].Op != testOpPostCommit {
			t.Errorf("entries[0].Op = %q, want %q", entries[0].Op, testOpPostCommit)
		}
		if entries[0].DurationMs != 200 {
			t.Errorf("entries[0].DurationMs = %d, want %d", entries[0].DurationMs, 200)
		}
		if entries[1].Op != "prepare-commit-msg" {
			t.Errorf("entries[1].Op = %q, want %q", entries[1].Op, "prepare-commit-msg")
		}
		if entries[1].DurationMs != 120 {
			t.Errorf("entries[1].DurationMs = %d, want %d", entries[1].DurationMs, 120)
		}
	})

	t.Run("filter by hook type", func(t *testing.T) {
		t.Parallel()
		logFile := writeFixture(t)

		entries, err := collectPerfEntries(logFile, 10, testOpPostCommit)
		if err != nil {
			t.Fatalf("collectPerfEntries returned error: %v", err)
		}

		if len(entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(entries))
		}
		if entries[0].Op != testOpPostCommit {
			t.Errorf("entries[0].Op = %q, want %q", entries[0].Op, testOpPostCommit)
		}
	})

	t.Run("file not found", func(t *testing.T) {
		t.Parallel()

		_, err := collectPerfEntries("/nonexistent/path/perf.jsonl", 10, "")
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
	})
}
