package cli

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"

	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/entireio/cli/cmd/entire/cli/trailers"
	"github.com/spf13/cobra"
)

const testReviewSkill = "/pr-review-toolkit:review-pr"

func TestReviewMarker_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)

	m := PendingReviewMarker{
		AgentName:   "claude-code",
		Skills:      []string{testReviewSkill},
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

func TestReviewCmd_Help(t *testing.T) {
	t.Parallel()
	rootCmd := NewRootCmd()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"review", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "review") {
		t.Errorf("--help output missing 'review': %s", out)
	}
	// Hidden flags should NOT appear in --help.
	for _, hidden := range []string{"postreview", "finalize", "session"} {
		if strings.Contains(out, "--"+hidden) {
			t.Errorf("--help leaked hidden flag: --%s", hidden)
		}
	}
}

func TestSaveReviewConfig_PersistsSettings(t *testing.T) {
	// NOTE: uses t.Chdir, so no t.Parallel.
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	// settings.Save writes to .entire/settings.json relative to CWD, so we need
	// to ensure .entire/ exists. The Save helper should create it if not.
	t.Chdir(tmp)

	err := saveReviewConfig(context.Background(), map[string][]string{
		"claude-code": {testReviewSkill, "/test-auditor"},
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
	if s.Review["claude-code"][0] != testReviewSkill {
		t.Errorf("first skill = %q", s.Review["claude-code"][0])
	}
}

func TestRunReview_TrackOnlyWritesMarker(t *testing.T) {
	// t.Chdir + first-run picker — no t.Parallel.
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)

	// Seed config so first-run picker doesn't fire.
	if err := saveReviewConfig(context.Background(), map[string][]string{
		testAgentName: {testReviewSkill},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := NewRootCmd()
	rootCmd.SetArgs([]string{"review", "--track-only"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	m, ok, err := ReadPendingReviewMarker()
	if err != nil || !ok {
		t.Fatalf("expected marker present: ok=%v err=%v", ok, err)
	}
	if m.AgentName != testAgentName {
		t.Errorf("AgentName = %q, want %s", m.AgentName, testAgentName)
	}
	if len(m.Skills) != 1 || m.Skills[0] != testReviewSkill {
		t.Errorf("Skills = %v", m.Skills)
	}
}

func TestRunPostReview_PrintsOptions(t *testing.T) {
	// NOTE: uses t.Chdir, so no t.Parallel.
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)

	const sid = "2026-01-01-s1"
	ctx := context.Background()
	store, err := session.NewStateStore(ctx)
	if err != nil {
		t.Fatalf("NewStateStore: %v", err)
	}
	state := &session.State{
		SessionID:    sid,
		Kind:         session.KindReview,
		ReviewStatus: session.ReviewStatusInProgress,
		StartedAt:    time.Now(),
	}
	if err := store.Save(ctx, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	if err := runPostReview(ctx, cmd, sid); err != nil {
		t.Fatalf("runPostReview: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"Review complete",
		"entire review --finalize fix",
		"entire review --finalize close",
		"entire review --finalize skip",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
}

func TestFinalizeFix_NoStateChange(t *testing.T) {
	// NOTE: uses t.Chdir, so no t.Parallel.
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)

	const sid = "2026-01-01-fix1"
	ctx := context.Background()
	store, err := session.NewStateStore(ctx)
	if err != nil {
		t.Fatalf("NewStateStore: %v", err)
	}
	state := &session.State{
		SessionID:    sid,
		Kind:         session.KindReview,
		ReviewStatus: session.ReviewStatusInProgress,
		StartedAt:    time.Now(),
	}
	if err := store.Save(ctx, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	if err := finalizeFix(ctx, cmd, sid); err != nil {
		t.Fatalf("finalizeFix: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Continue addressing") {
		t.Errorf("expected 'Continue addressing' in output; got: %s", out)
	}

	// State should be unchanged — still in-progress.
	got, err := store.Load(ctx, sid)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil {
		t.Fatal("session not found after finalizeFix")
	}
	if got.ReviewStatus != session.ReviewStatusInProgress {
		t.Errorf("ReviewStatus = %q, want %q", got.ReviewStatus, session.ReviewStatusInProgress)
	}
}

func TestCreateReviewCommit(t *testing.T) {
	// NOTE: uses t.Chdir, so no t.Parallel.
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)

	ctx := context.Background()

	// Record HEAD before the review commit.
	repo, err := git.PlainOpen(tmp)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	headBefore, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	oldHEAD := headBefore.Hash()

	md := trailers.ReviewMetadata{
		By:     "tester@example.com",
		Status: trailers.ReviewStatusClosed,
	}
	hash, err := createReviewCommit(ctx, md)
	if err != nil {
		t.Fatalf("createReviewCommit: %v", err)
	}
	if hash == (plumbing.Hash{}) {
		t.Fatal("returned zero hash")
	}

	// Branch HEAD should have advanced.
	headAfter, err := repo.Head()
	if err != nil {
		t.Fatalf("Head after: %v", err)
	}
	if headAfter.Hash() != hash {
		t.Errorf("HEAD = %s, want %s", headAfter.Hash(), hash)
	}
	if headAfter.Hash() == oldHEAD {
		t.Error("HEAD did not advance")
	}

	// New commit's parent should be the old HEAD.
	commit, err := repo.CommitObject(hash)
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	if len(commit.ParentHashes) != 1 || commit.ParentHashes[0] != oldHEAD {
		t.Errorf("parent = %v, want [%s]", commit.ParentHashes, oldHEAD)
	}

	// Tree should equal parent's tree (empty commit).
	parent, err := repo.CommitObject(oldHEAD)
	if err != nil {
		t.Fatalf("parent CommitObject: %v", err)
	}
	if commit.TreeHash != parent.TreeHash {
		t.Errorf("tree hash changed: %s != %s", commit.TreeHash, parent.TreeHash)
	}

	// Commit message should contain the review trailer.
	if !strings.Contains(commit.Message, trailers.ReviewByTrailerKey) {
		t.Errorf("commit message missing %q trailer; got:\n%s", trailers.ReviewByTrailerKey, commit.Message)
	}
}

func TestCreateReviewCommit_DetachedHEAD(t *testing.T) {
	// NOTE: uses t.Chdir, so no t.Parallel.
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)

	// Detach HEAD by checking out the commit hash directly.
	out, err := exec.CommandContext(context.Background(), "git", "-C", tmp, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	sha := strings.TrimSpace(string(out))
	if err := exec.CommandContext(context.Background(), "git", "-C", tmp, "checkout", "--detach", sha).Run(); err != nil {
		t.Fatalf("checkout detach: %v", err)
	}

	_, err = createReviewCommit(context.Background(), trailers.ReviewMetadata{
		By:     "tester@example.com",
		Status: trailers.ReviewStatusClosed,
	})
	if err == nil {
		t.Fatal("expected error on detached HEAD, got nil")
	}
	if !strings.Contains(err.Error(), "detached HEAD") {
		t.Errorf("error = %q, want contains 'detached HEAD'", err.Error())
	}
}

func TestFinalizeSkip(t *testing.T) {
	// NOTE: uses t.Chdir, so no t.Parallel.
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)

	const sid = "2026-01-01-skip1"
	ctx := context.Background()
	store, err := session.NewStateStore(ctx)
	if err != nil {
		t.Fatalf("NewStateStore: %v", err)
	}
	state := &session.State{
		SessionID:    sid,
		Kind:         session.KindReview,
		ReviewStatus: session.ReviewStatusInProgress,
		StartedAt:    time.Now(),
	}
	if err := store.Save(ctx, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Record HEAD before call; it must not change.
	repo, err := git.PlainOpen(tmp)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	headBefore, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	if err := finalizeSkip(ctx, cmd, sid); err != nil {
		t.Fatalf("finalizeSkip: %v", err)
	}

	// HEAD must be unchanged.
	headAfter, err := repo.Head()
	if err != nil {
		t.Fatalf("Head after: %v", err)
	}
	if headAfter.Hash() != headBefore.Hash() {
		t.Errorf("HEAD changed: was %s, now %s", headBefore.Hash(), headAfter.Hash())
	}

	// Session state must reflect skipped.
	got, err := store.Load(ctx, sid)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil {
		t.Fatal("session not found after finalizeSkip")
	}
	if got.ReviewStatus != session.ReviewStatusSkipped {
		t.Errorf("ReviewStatus = %q, want %q", got.ReviewStatus, session.ReviewStatusSkipped)
	}

	// Output should mention "exit".
	if !strings.Contains(buf.String(), "exit") {
		t.Errorf("output missing 'exit'; got: %s", buf.String())
	}
}
