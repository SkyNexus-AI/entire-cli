package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestLogoutCmd_PrintsLoggedOut(t *testing.T) {
	t.Parallel()

	cmd := newLogoutCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})

	// RunE calls store.DeleteToken which hits the real keyring.
	// On most CI/dev machines the keyring is available and DeleteToken
	// is a no-op when no token exists, so this exercises the happy path.
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out.String(), "Logged out.") {
		t.Fatalf("output = %q, want to contain %q", out.String(), "Logged out.")
	}
}

func TestLogoutCmd_IsRegistered(t *testing.T) {
	t.Parallel()

	root := NewRootCmd()
	found := false
	for _, c := range root.Commands() {
		if c.Use == "logout" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("logout command not registered on root")
	}
}

func TestLogoutCmd_HasShortDescription(t *testing.T) {
	t.Parallel()

	cmd := newLogoutCmd()
	if cmd.Short == "" {
		t.Fatal("logout command has no short description")
	}
}
