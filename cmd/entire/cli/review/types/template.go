// Package types — see reviewer.go for package-level rationale.
//
// template.go provides ReviewerTemplate, a struct that implements
// AgentReviewer using two caller-supplied functions: BuildCmd (per-agent
// argv/env construction) and Parser (per-agent stdout-to-Event stream).
//
// All three currently-supported agents (claude-code, codex, gemini-cli)
// share the Start/Process/Wait/Events scaffolding. Only the build-cmd
// step and the stdout parser genuinely differ. The template owns the
// shared lifecycle (spawn → pipe stdout → run parser → forward events
// → close on exit); each agent supplies the two unique pieces.
package types

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// ReviewerTemplate implements AgentReviewer for agents whose only per-agent
// quirks are argv/env construction and stdout parsing. Both fields must be
// set before Start is called; nil values cause Start to panic immediately.
type ReviewerTemplate struct {
	// AgentName is returned by Name(). Stable identifier per agent.
	AgentName string

	// BuildCmd constructs the *exec.Cmd to spawn the agent process,
	// including argv, stdin (if any), and ENTIRE_REVIEW_* env vars.
	// The command MUST NOT have started yet; the template will call Start.
	BuildCmd func(ctx context.Context, cfg RunConfig) *exec.Cmd

	// Parser converts the agent's stdout stream into a sequence of Events.
	// The returned channel must close when stdout closes. Implementations
	// must emit Started first, Finished{Success: ...} or RunError last,
	// and check scanner.Err() before emitting Finished{Success: true}.
	Parser func(stdout io.Reader) <-chan Event
}

// Compile-time check.
var _ AgentReviewer = (*ReviewerTemplate)(nil)

// Name returns the agent's stable identifier.
func (t *ReviewerTemplate) Name() string { return t.AgentName }

// Start spawns the agent process, wires stdout through the parser, and
// returns a Process whose Events channel streams the parsed event sequence.
//
// Returns ErrTemplateMisconfigured if AgentName, BuildCmd, or Parser is unset,
// or if BuildCmd returns nil. These are programmer errors but failing fast
// with a typed error is friendlier than a downstream nil deref — and it
// keeps Start from panicking inside a multi-agent fan-out (CU8) where one
// misconfigured template would otherwise kill the whole run.
func (t *ReviewerTemplate) Start(ctx context.Context, cfg RunConfig) (Process, error) {
	if t.AgentName == "" {
		return nil, fmt.Errorf("ReviewerTemplate.Start: %w (empty AgentName)", ErrTemplateMisconfigured)
	}
	if t.BuildCmd == nil {
		return nil, fmt.Errorf("ReviewerTemplate.Start: %w (nil BuildCmd for agent %q)", ErrTemplateMisconfigured, t.AgentName)
	}
	if t.Parser == nil {
		return nil, fmt.Errorf("ReviewerTemplate.Start: %w (nil Parser for agent %q)", ErrTemplateMisconfigured, t.AgentName)
	}
	cmd := t.BuildCmd(ctx, cfg)
	if cmd == nil {
		return nil, fmt.Errorf("ReviewerTemplate.Start: %w (BuildCmd returned nil for agent %q)", ErrTemplateMisconfigured, t.AgentName)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: stdout pipe: %w", t.AgentName, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: start: %w", t.AgentName, err)
	}
	p := &templateProcess{cmd: cmd, events: make(chan Event, 32)}
	go p.run(stdout, t.Parser)
	return p, nil
}

// ErrTemplateMisconfigured is returned by ReviewerTemplate.Start when one of
// the required fields (AgentName, BuildCmd, Parser) is unset, or when
// BuildCmd returns nil. Use errors.Is to detect this in tests or higher
// layers (e.g., a multi-agent orchestrator that needs to skip a misconfigured
// agent rather than crashing the whole run).
var ErrTemplateMisconfigured = errors.New("ReviewerTemplate misconfigured")

// templateProcess is the shared Process implementation for ReviewerTemplate.
type templateProcess struct {
	cmd    *exec.Cmd
	events chan Event
}

// Events returns the channel that streams parsed events from the agent process.
func (p *templateProcess) Events() <-chan Event { return p.events }

// Wait returns the agent process's exit error.
//
// Per CU2's Process contract: nil on clean exit (exit code 0); ctx.Err()
// on cancellation; *exec.ExitError on non-zero exit; other types for I/O
// or pipe failures. The error type is preserved (not wrapped) so callers
// can use errors.As and errors.Is to classify.
func (p *templateProcess) Wait() error {
	return p.cmd.Wait() //nolint:wrapcheck // interface boundary; classifyStatus needs raw type
}

func (p *templateProcess) run(stdout io.Reader, parser func(io.Reader) <-chan Event) {
	defer close(p.events)
	for ev := range parser(stdout) {
		p.events <- ev
	}
}
