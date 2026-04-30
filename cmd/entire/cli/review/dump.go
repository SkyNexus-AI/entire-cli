// Package review — see env.go for package-level rationale.
//
// dump.go provides DumpSink, a Sink implementation that writes a
// per-agent narrative dump to an io.Writer after the run completes.
// AgentEvent is a no-op; events are read from RunSummary.AgentRuns[].Buffer
// in RunFinished.
package review

import (
	"fmt"
	"io"
	"strings"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

// DumpSink writes per-agent narrative blocks to W after the run completes.
type DumpSink struct {
	W io.Writer
}

// Compile-time interface check.
var _ reviewtypes.Sink = DumpSink{}

// AgentEvent is intentionally a no-op. DumpSink renders post-run from
// the AgentRun.Buffer slices in RunFinished.
func (DumpSink) AgentEvent(_ string, _ reviewtypes.Event) {}

// RunFinished writes a narrative block per agent, then a counts line.
func (s DumpSink) RunFinished(summary reviewtypes.RunSummary) {
	for _, run := range summary.AgentRuns {
		s.dumpAgent(run)
	}
	s.dumpCounts(summary)
}

func (s DumpSink) dumpAgent(run reviewtypes.AgentRun) {
	fmt.Fprintf(s.W, "─────── %s review ───────\n", run.Name)
	if run.Status == reviewtypes.AgentStatusCancelled {
		fmt.Fprintln(s.W, "(cancelled)")
		return
	}
	if run.Status == reviewtypes.AgentStatusFailed {
		// Surface the wait error if any (process exit failure), then any
		// agent-level RunError events the parser emitted (typically a torn
		// stdout stream — caught at the orchestrator level by classifyStatus
		// even when the process itself exited 0).
		if run.Err != nil {
			fmt.Fprintf(s.W, "(failed: %v)\n", run.Err)
		} else {
			fmt.Fprintln(s.W, "(failed)")
		}
		for _, ev := range run.Buffer {
			if re, ok := ev.(reviewtypes.RunError); ok && re.Err != nil {
				fmt.Fprintf(s.W, "  agent error: %v\n", re.Err)
			}
		}
		// Render any narrative text the agent produced before the failure
		// surfaced — useful when the parser tore mid-response so reviewers
		// can see partial output instead of a bare "(failed)" line.
		if narrative := joinAssistantText(run.Buffer); narrative != "" {
			fmt.Fprintln(s.W, narrative)
		}
		return
	}
	if narrative := joinAssistantText(run.Buffer); narrative != "" {
		fmt.Fprintln(s.W, narrative)
	}
}

// joinAssistantText extracts AssistantText events from a buffer and joins
// them with newlines, trimming the result to keep dump output tight.
func joinAssistantText(buf []reviewtypes.Event) string {
	var b strings.Builder
	for _, ev := range buf {
		if at, ok := ev.(reviewtypes.AssistantText); ok {
			b.WriteString(at.Text)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func (s DumpSink) dumpCounts(summary reviewtypes.RunSummary) {
	succ, fail, canc := 0, 0, 0
	for _, r := range summary.AgentRuns {
		switch r.Status {
		case reviewtypes.AgentStatusSucceeded:
			succ++
		case reviewtypes.AgentStatusFailed:
			fail++
		case reviewtypes.AgentStatusCancelled:
			canc++
		case reviewtypes.AgentStatusUnknown:
			// Unknown status: not counted in any bucket.
		}
	}
	fmt.Fprintf(s.W, "%d agent(s) done — %d succeeded, %d failed, %d cancelled\n",
		len(summary.AgentRuns), succ, fail, canc)
}
