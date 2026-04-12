package agent

import (
	"strings"
	"testing"
)

func TestWrapProductionJSONWarningHookCommand(t *testing.T) {
	t.Parallel()

	command := WrapProductionJSONWarningHookCommand("entire hooks claude-code session-start", WarningFormatMultiLine)

	if command == "entire hooks claude-code session-start" {
		t.Fatal("expected wrapped command, got raw command")
	}
	if strings.Contains(command, `>&2`) {
		t.Fatalf("claude wrapper should not print warning to stderr, got %q", command)
	}
	if want := `systemMessage`; !strings.Contains(command, want) {
		t.Fatalf("claude wrapper missing systemMessage JSON, got %q", command)
	}
	if !strings.Contains(command, "Powered by Entire") {
		t.Fatalf("claude wrapper missing warning text, got %q", command)
	}
	if want := "exec entire hooks claude-code session-start"; !strings.Contains(command, want) {
		t.Fatalf("claude wrapper missing exec target, got %q", command)
	}
}

func TestWrapProductionPlainTextWarningHookCommand(t *testing.T) {
	t.Parallel()

	command := WrapProductionPlainTextWarningHookCommand("entire hooks factoryai-droid session-start", WarningFormatSingleLine)

	if command == "entire hooks factoryai-droid session-start" {
		t.Fatal("expected wrapped command, got raw command")
	}
	if strings.Contains(command, `>&2`) {
		t.Fatalf("plain text wrapper should not print warning to stderr, got %q", command)
	}
	if !strings.Contains(command, "Powered by Entire: Tracking is enabled") {
		t.Fatalf("plain text wrapper missing warning text, got %q", command)
	}
	if want := "exec entire hooks factoryai-droid session-start"; !strings.Contains(command, want) {
		t.Fatalf("plain text wrapper missing exec target, got %q", command)
	}
}

func TestMissingEntireWarning(t *testing.T) {
	t.Parallel()

	if got := MissingEntireWarning(WarningFormatSingleLine); strings.Contains(got, "\n") {
		t.Fatalf("single-line warning should not contain newlines, got %q", got)
	}
	if got := MissingEntireWarning(WarningFormatMultiLine); !strings.Contains(got, "\n") {
		t.Fatalf("multiline warning should contain newlines, got %q", got)
	}
}
