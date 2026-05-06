package review

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

func makeSummary(runs ...reviewtypes.AgentRun) reviewtypes.RunSummary {
	return reviewtypes.RunSummary{AgentRuns: runs}
}

func TestDumpSink_SucceededAgent(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	sink := DumpSink{W: &buf}

	run := reviewtypes.AgentRun{
		Name:   "claude-code",
		Status: reviewtypes.AgentStatusSucceeded,
		Buffer: []reviewtypes.Event{
			reviewtypes.AssistantText{Text: "First finding"},
			reviewtypes.AssistantText{Text: "Second finding"},
		},
	}
	sink.RunFinished(makeSummary(run))

	out := buf.String()
	if !strings.Contains(out, "─────── claude-code review ───────") {
		t.Errorf("expected agent header, got:\n%s", out)
	}
	if !strings.Contains(out, "First finding") {
		t.Errorf("expected first finding in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Second finding") {
		t.Errorf("expected second finding in output, got:\n%s", out)
	}
	if !strings.Contains(out, "1 agent(s) done — 1 succeeded, 0 failed, 0 cancelled") {
		t.Errorf("expected counts line, got:\n%s", out)
	}
}

func TestDumpSink_FailedAgentWithErr(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	sink := DumpSink{W: &buf}

	run := reviewtypes.AgentRun{
		Name:   "codex",
		Status: reviewtypes.AgentStatusFailed,
		Err:    errors.New("binary not found"),
	}
	sink.RunFinished(makeSummary(run))

	out := buf.String()
	if !strings.Contains(out, "(failed: binary not found)") {
		t.Errorf("expected error message in output, got:\n%s", out)
	}
	if !strings.Contains(out, "1 agent(s) done — 0 succeeded, 1 failed, 0 cancelled") {
		t.Errorf("expected counts line, got:\n%s", out)
	}
}

func TestDumpSink_FailedAgentNoErr(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	sink := DumpSink{W: &buf}

	run := reviewtypes.AgentRun{
		Name:   "codex",
		Status: reviewtypes.AgentStatusFailed,
		Err:    nil,
	}
	sink.RunFinished(makeSummary(run))

	out := buf.String()
	if !strings.Contains(out, "(failed)") {
		t.Errorf("expected (failed) in output, got:\n%s", out)
	}
	// Must not contain "(failed: " which would indicate an error was printed.
	if strings.Contains(out, "(failed: ") {
		t.Errorf("unexpected error detail in output, got:\n%s", out)
	}
}

func TestDumpSink_CancelledAgent(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	sink := DumpSink{W: &buf}

	run := reviewtypes.AgentRun{
		Name:   "gemini-cli",
		Status: reviewtypes.AgentStatusCancelled,
		Buffer: []reviewtypes.Event{
			reviewtypes.AssistantText{Text: "partial output"},
		},
	}
	sink.RunFinished(makeSummary(run))

	out := buf.String()
	if !strings.Contains(out, "(cancelled)") {
		t.Errorf("expected (cancelled) in output, got:\n%s", out)
	}
	// Narrative should not be dumped for cancelled runs.
	if strings.Contains(out, "partial output") {
		t.Errorf("narrative should not appear for cancelled agent, got:\n%s", out)
	}
}

func TestDumpSink_Mixed(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	sink := DumpSink{W: &buf}

	summary := makeSummary(
		reviewtypes.AgentRun{
			Name:   "claude-code",
			Status: reviewtypes.AgentStatusSucceeded,
			Buffer: []reviewtypes.Event{reviewtypes.AssistantText{Text: "looks good"}},
		},
		reviewtypes.AgentRun{
			Name:   "codex",
			Status: reviewtypes.AgentStatusFailed,
			Err:    errors.New("timeout"),
		},
		reviewtypes.AgentRun{
			Name:   "gemini-cli",
			Status: reviewtypes.AgentStatusCancelled,
		},
	)
	sink.RunFinished(summary)

	out := buf.String()
	if !strings.Contains(out, "─────── claude-code review ───────") {
		t.Errorf("expected claude-code header, got:\n%s", out)
	}
	if !strings.Contains(out, "─────── codex review ───────") {
		t.Errorf("expected codex header, got:\n%s", out)
	}
	if !strings.Contains(out, "─────── gemini-cli review ───────") {
		t.Errorf("expected gemini-cli header, got:\n%s", out)
	}
	if !strings.Contains(out, "3 agent(s) done — 1 succeeded, 1 failed, 1 cancelled") {
		t.Errorf("expected mixed counts line, got:\n%s", out)
	}
}

func TestDumpSink_EmptyAgentRuns(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	sink := DumpSink{W: &buf}

	sink.RunFinished(reviewtypes.RunSummary{})

	out := buf.String()
	if !strings.Contains(out, "0 agent(s) done — 0 succeeded, 0 failed, 0 cancelled") {
		t.Errorf("expected empty counts line, got:\n%s", out)
	}
}
