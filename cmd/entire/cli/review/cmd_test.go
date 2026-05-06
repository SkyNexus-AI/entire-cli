package review_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	cli "github.com/entireio/cli/cmd/entire/cli"
	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/review"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// setupCmdTestRepo initialises a temp git repo with one commit and chdirs into it.
func setupCmdTestRepo(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "x")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)
}

// installHooksForCmdTest installs the given agent's hooks into the CWD-relative repo.
func installHooksForCmdTest(t *testing.T, agentName types.AgentName) {
	t.Helper()
	ag, err := agent.Get(agentName)
	if err != nil {
		t.Fatalf("agent.Get(%q): %v", agentName, err)
	}
	hs, ok := agent.AsHookSupport(ag)
	if !ok {
		t.Fatalf("agent %q does not support hooks", agentName)
	}
	if _, err := hs.InstallHooks(context.Background(), false, false); err != nil {
		t.Fatalf("InstallHooks(%q): %v", agentName, err)
	}
}

// TestReviewCmd_Help verifies `entire review --help` contains the expected
// flags and subcommands without panicking.
func TestReviewCmd_Help(t *testing.T) {
	t.Parallel()
	rootCmd := cli.NewRootCmd()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"review", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"review", "--edit", "--agent", "attach"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help output missing %q: %s", want, out)
		}
	}
	// --track-only was intentionally dropped by PR #1009.
	if strings.Contains(out, "track-only") {
		t.Error("--help output should NOT contain track-only flag (dropped in #1009)")
	}
}

// TestNewReviewCmd_NoHiddenFlags ensures the removed internal flags are gone.
func TestNewReviewCmd_NoHiddenFlags(t *testing.T) {
	t.Parallel()
	rootCmd := cli.NewRootCmd()
	reviewCmd, _, err := rootCmd.Find([]string{"review"})
	if err != nil || reviewCmd == nil {
		t.Fatal("review subcommand not found")
	}
	for _, name := range []string{"postreview", "finalize", "session", "track-only"} {
		if reviewCmd.Flags().Lookup(name) != nil {
			t.Errorf("found removed flag: --%s", name)
		}
	}
}

// TestRunReview_MissingHooksAborts verifies that `entire review` aborts with a
// clear error when the configured agent has no lifecycle hooks installed.
func TestRunReview_MissingHooksAborts(t *testing.T) {
	setupCmdTestRepo(t)

	// Save config but don't install hooks.
	if err := review.SaveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		"claude-code": {Skills: []string{testReviewSkill}},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := cli.NewRootCmd()
	errBuf := &bytes.Buffer{}
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"review"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when hooks are not installed")
	}
	if !strings.Contains(errBuf.String(), "Hooks are not installed") {
		t.Errorf("expected 'Hooks are not installed' in stderr, got: %s", errBuf.String())
	}

	_, ok, readErr := review.ReadPendingReviewMarker(context.Background())
	if readErr != nil || ok {
		t.Errorf("marker should not exist when hooks are missing: ok=%v err=%v", ok, readErr)
	}
}

// TestRunReview_NonLaunchableAgentPreservesMarker verifies that the pending
// marker is NOT cleared when a non-launchable agent is selected. Uses cursor
// because it has HookSupport but no Launcher.
//
// Regression: previously the cleanup defer was registered before the
// LauncherFor check, so the marker was wiped on the !ok path, breaking
// the hand-off message.
func TestRunReview_NonLaunchableAgentPreservesMarker(t *testing.T) {
	setupCmdTestRepo(t)

	const nonLaunchableAgent = "cursor"
	installHooksForCmdTest(t, types.AgentName(nonLaunchableAgent))

	// Confirm cursor has no Launcher; skip if a future change adds one.
	if _, hasLauncher := agent.LauncherFor(types.AgentName(nonLaunchableAgent)); hasLauncher {
		t.Skipf("%s now implements Launcher; pick another non-launchable agent", nonLaunchableAgent)
	}

	// Use prompt-only config: cursor has no curated built-ins, so a Skills
	// value would trip the installed-skill guard before reaching this path.
	if err := review.SaveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		nonLaunchableAgent: {Prompt: "review the diff"},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := cli.NewRootCmd()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"review"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Marker written") {
		t.Errorf("expected marker-written message, got: %s", out)
	}

	m, ok, err := review.ReadPendingReviewMarker(context.Background())
	if err != nil {
		t.Fatalf("ReadPendingReviewMarker: %v", err)
	}
	if !ok {
		t.Fatal("marker was cleared — hand-off is broken")
	}
	if m.AgentName != nonLaunchableAgent {
		t.Errorf("AgentName = %q, want %s", m.AgentName, nonLaunchableAgent)
	}
}

// TestRunReview_MissingConfiguredSkillAbortsBeforeMarker verifies that a
// bogus configured skill aborts before writing the pending marker.
func TestRunReview_MissingConfiguredSkillAbortsBeforeMarker(t *testing.T) {
	setupCmdTestRepo(t)
	installHooksForCmdTest(t, "claude-code")

	if err := review.SaveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		"claude-code": {Skills: []string{"/bogus:skill-does-not-exist"}},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := cli.NewRootCmd()
	errBuf := &bytes.Buffer{}
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"review"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when configured skill not installed")
	}
	if !strings.Contains(errBuf.String(), "not installed") {
		t.Errorf("stderr should mention 'not installed', got: %s", errBuf.String())
	}
	_, markerExists, markerErr := review.ReadPendingReviewMarker(context.Background())
	if markerErr != nil {
		t.Fatalf("ReadPendingReviewMarker: %v", markerErr)
	}
	if markerExists {
		t.Error("pending marker should not exist when verification fails")
	}
}

// TestRunReview_PromptOnlyConfigSkipsVerification verifies that a prompt-only
// config (no Skills) skips the installed-skill guard and writes the marker.
func TestRunReview_PromptOnlyConfigSkipsVerification(t *testing.T) {
	setupCmdTestRepo(t)
	installHooksForCmdTest(t, "cursor")

	if err := review.SaveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		"cursor": {Prompt: "review the diff"},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := cli.NewRootCmd()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"review"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, markerExists, markerErr := review.ReadPendingReviewMarker(context.Background())
	if markerErr != nil {
		t.Fatalf("ReadPendingReviewMarker: %v", markerErr)
	}
	if !markerExists {
		t.Error("marker should exist for prompt-only config")
	}
}

// TestRunReview_FlagOverrideSkipsPicker verifies that --agent flag bypasses
// the interactive picker even when multiple eligible agents are configured.
func TestRunReview_FlagOverrideSkipsPicker(t *testing.T) {
	setupCmdTestRepo(t)
	installHooksForCmdTest(t, "cursor")
	installHooksForCmdTest(t, "opencode")

	if err := review.SaveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		"cursor":   {Prompt: "review the diff"},
		"opencode": {Prompt: "review the diff"},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := cli.NewRootCmd()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"review", "--agent", "opencode"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m, ok, err := review.ReadPendingReviewMarker(context.Background())
	if err != nil || !ok {
		t.Fatalf("marker should be written: ok=%v err=%v", ok, err)
	}
	if m.AgentName != "opencode" {
		t.Errorf("AgentName = %q, want opencode", m.AgentName)
	}
}

// TestRunReview_FlagOverrideMustBeEligibleAgent verifies that --agent with an
// agent that has no hooks installed gives a clear error.
func TestRunReview_FlagOverrideMustBeEligibleAgent(t *testing.T) {
	setupCmdTestRepo(t)
	installHooksForCmdTest(t, "cursor")
	// opencode has no hooks installed

	if err := review.SaveReviewConfig(context.Background(), map[string]settings.ReviewConfig{
		"cursor":   {Prompt: "review the diff"},
		"opencode": {Prompt: "review the diff"},
	}); err != nil {
		t.Fatal(err)
	}

	rootCmd := cli.NewRootCmd()
	errBuf := &bytes.Buffer{}
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"review", "--agent", "opencode"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --agent points at hookless agent")
	}
	if !strings.Contains(errBuf.String(), "Hooks are not installed") {
		t.Errorf("stderr should mention 'Hooks are not installed', got: %s", errBuf.String())
	}
}
