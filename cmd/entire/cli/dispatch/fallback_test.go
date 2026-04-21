package dispatch

import (
	"testing"
	"time"
)

func TestApplyFallbackChain_UsesLocalSummaryFirst(t *testing.T) {
	t.Parallel()

	got := applyFallbackChain([]candidate{{
		CheckpointID:      "cp1",
		LocalSummaryTitle: "local summary",
		CommitSubject:     "ship the thing",
		RepoFullName:      "entireio/cli",
		Branch:            "main",
		CreatedAt:         time.Unix(1, 0).UTC(),
	}})
	if len(got.Used) != 1 || got.Used[0].Bullet.Source != "local_summary" {
		t.Fatalf("unexpected used bullets: %+v", got.Used)
	}
	if got.Used[0].Bullet.Text != "local summary" {
		t.Fatalf("unexpected bullet text: %+v", got.Used[0].Bullet)
	}
}

func TestApplyFallbackChain_FallsBackToCommitMessage(t *testing.T) {
	t.Parallel()

	got := applyFallbackChain([]candidate{{
		CheckpointID:  "cp1",
		CommitSubject: "ship the thing",
		RepoFullName:  "entireio/cli",
		Branch:        "main",
		CreatedAt:     time.Unix(1, 0).UTC(),
	}})
	if len(got.Used) != 1 || got.Used[0].Bullet.Source != "commit_message" {
		t.Fatalf("unexpected used bullets: %+v", got.Used)
	}
}

func TestApplyFallbackChain_UncategorizedWhenNoFallback(t *testing.T) {
	t.Parallel()

	got := applyFallbackChain([]candidate{{CheckpointID: "cp1"}})
	if len(got.Used) != 0 {
		t.Fatalf("expected no bullets, got %d", len(got.Used))
	}
	if got.Warnings.UncategorizedCount != 1 {
		t.Fatalf("unexpected warnings: %+v", got.Warnings)
	}
}
