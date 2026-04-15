package cursor

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

func TestGenerateText_RejectsOversizedPrompt(t *testing.T) {
	t.Parallel()

	originalRunner := cursorCommandRunner
	t.Cleanup(func() {
		cursorCommandRunner = originalRunner
	})

	called := false
	cursorCommandRunner = func(context.Context, string, ...string) *exec.Cmd {
		called = true
		return nil
	}

	ag := &CursorAgent{}
	_, err := ag.GenerateText(context.Background(), strings.Repeat("a", agent.MaxInlinePromptBytes+1), "")
	if err == nil {
		t.Fatal("expected oversized prompt error")
	}
	if called {
		t.Fatal("expected command runner not to be called")
	}
	if !strings.Contains(err.Error(), "too large for CLI argument transport") {
		t.Fatalf("unexpected error: %v", err)
	}
}
