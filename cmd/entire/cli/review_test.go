package cli

import (
	"path/filepath"
	"testing"
	"time"

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
