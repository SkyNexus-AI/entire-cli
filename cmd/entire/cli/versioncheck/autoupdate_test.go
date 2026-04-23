package versioncheck

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// autoUpdateFixture wires the test seams for MaybeAutoUpdate.
type autoUpdateFixture struct {
	installCalls int
	installErr   error
	lastCommand  string
	confirmValue bool
	confirmErr   error
}

func newAutoUpdateFixture(t *testing.T) *autoUpdateFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(envKillSwitch, "")
	// Force interactive mode on by default; individual tests can opt out.
	t.Setenv("ENTIRE_TEST_TTY", "1")

	f := &autoUpdateFixture{confirmValue: true}

	origRun := runInstaller
	runInstaller = func(_ context.Context, cmd string) error {
		f.installCalls++
		f.lastCommand = cmd
		return f.installErr
	}
	origConfirm := confirmUpdate
	confirmUpdate = func() (bool, error) { return f.confirmValue, f.confirmErr }

	t.Cleanup(func() {
		runInstaller = origRun
		confirmUpdate = origConfirm
	})
	return f
}

// useBrewExecutable points the install-manager detector at a brew cellar path.
func useBrewExecutable(t *testing.T) {
	t.Helper()
	orig := executablePath
	executablePath = func() (string, error) {
		return "/opt/homebrew/Cellar/entire/1.0.0/bin/entire", nil
	}
	t.Cleanup(func() { executablePath = orig })
}

func TestMaybeAutoUpdate_KillSwitch(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)
	t.Setenv(envKillSwitch, "1")

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 0 {
		t.Errorf("installer called with kill-switch set")
	}
}

func TestMaybeAutoUpdate_NoTTY(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)
	t.Setenv("ENTIRE_TEST_TTY", "0")

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 0 {
		t.Errorf("installer called without TTY")
	}
}

func TestMaybeAutoUpdate_CIEnv(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)
	// Clear the test override so the real CanPromptInteractively path runs.
	t.Setenv("ENTIRE_TEST_TTY", "")
	t.Setenv("CI", "true")

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 0 {
		t.Errorf("installer called on CI (CI=true)")
	}
}

func TestMaybeAutoUpdate_UserDeclines(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)
	f.confirmValue = false

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 0 {
		t.Errorf("installer called after user declined")
	}
}

func TestMaybeAutoUpdate_HappyPath(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 1 {
		t.Fatalf("installer called %d times, want 1", f.installCalls)
	}
	if f.lastCommand != "brew upgrade --cask entire" {
		t.Errorf("installer got %q, want brew upgrade --cask entire", f.lastCommand)
	}
	if !strings.Contains(buf.String(), "Update complete") {
		t.Errorf("missing success message: %q", buf.String())
	}
}

func TestMaybeAutoUpdate_InstallerFailurePrintedToUser(t *testing.T) {
	f := newAutoUpdateFixture(t)
	useBrewExecutable(t)
	f.installErr = errors.New("boom")

	var buf bytes.Buffer
	MaybeAutoUpdate(context.Background(), &buf, "1.0.0")

	if f.installCalls != 1 {
		t.Fatalf("installer called %d times, want 1", f.installCalls)
	}
	out := buf.String()
	if !strings.Contains(out, "Update failed") {
		t.Errorf("missing failure message: %q", out)
	}
	// Failure message should include a manual-retry hint with the exact command.
	if !strings.Contains(out, "Try again later running:") {
		t.Errorf("missing retry hint: %q", out)
	}
	if !strings.Contains(out, "brew upgrade --cask entire") {
		t.Errorf("retry hint missing installer command: %q", out)
	}
}
