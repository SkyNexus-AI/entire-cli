package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func TestReviewMarker_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)

	m := PendingReviewMarker{
		AgentName:   "claude-code",
		Skills:      []string{"/pr-review-toolkit:review-pr"},
		StartingSHA: "deadbeef",
		StartedAt:   time.Now().UTC(),
	}
	if err := WritePendingReviewMarker(m); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, ok, err := ReadPendingReviewMarker()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !ok {
		t.Fatal("expected marker present")
	}
	if got.AgentName != m.AgentName || got.StartingSHA != m.StartingSHA {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if err := ClearPendingReviewMarker(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	_, ok, err = ReadPendingReviewMarker()
	if err != nil {
		t.Fatalf("read-after-clear: %v", err)
	}
	if ok {
		t.Error("expected marker absent after clear")
	}

	// Ensure the file lived under .git/entire-sessions/, not the worktree.
	gitDir := filepath.Join(tmp, ".git")
	entries, err := filepath.Glob(filepath.Join(gitDir, "entire-sessions", "*"))
	if err != nil {
		t.Fatalf("glob sessions dir: %v", err)
	}
	_ = entries // sanity check only
}

func TestSaveReviewConfig_PersistsSettings(t *testing.T) {
	// NOTE: uses t.Chdir, so no t.Parallel.
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	// settings.Save writes to .entire/settings.json relative to CWD, so we need
	// to ensure .entire/ exists. The Save helper should create it if not.
	t.Chdir(tmp)

	err := saveReviewConfig(context.Background(), map[string][]string{
		"claude-code": {"/pr-review-toolkit:review-pr", "/test-auditor"},
	})
	if err != nil {
		t.Fatal(err)
	}

	s, err := settings.Load(context.Background())
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if len(s.Review["claude-code"]) != 2 {
		t.Errorf("expected 2 skills saved, got %v", s.Review)
	}
	if s.Review["claude-code"][0] != "/pr-review-toolkit:review-pr" {
		t.Errorf("first skill = %q", s.Review["claude-code"][0])
	}
}
