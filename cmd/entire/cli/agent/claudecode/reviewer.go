package claudecode

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/review"
	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

// Reviewer is the AgentReviewer implementation for claude-code.
//
// Argv shape: claude -p "<prompt>".
// Prompt is passed as a command-line argument; stdin is unused.
// Stdout in -p mode is the assistant's plain-text response (no JSON envelope).
type Reviewer struct{}

// NewReviewer creates a new claude-code AgentReviewer.
func NewReviewer() *Reviewer { return &Reviewer{} }

// Compile-time interface check.
var _ reviewtypes.AgentReviewer = (*Reviewer)(nil)

// Name returns the agent's registry key.
func (*Reviewer) Name() string { return "claude-code" }

// Start spawns claude with the review prompt and returns a streaming Process.
// Binary lookup is deferred to exec.Cmd.Start, so a missing binary surfaces
// as an error from this call (Start propagates os/exec's "executable not found"
// error immediately when cmd.Start fails).
func (r *Reviewer) Start(ctx context.Context, cfg reviewtypes.RunConfig) (reviewtypes.Process, error) {
	cmd := buildReviewCmd(ctx, cfg)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude-code: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude-code: start: %w", err)
	}
	p := &reviewProcess{cmd: cmd, events: make(chan reviewtypes.Event, 32)}
	go p.run(stdout)
	return p, nil
}

// buildReviewCmd builds the exec.Cmd for a claude review run.
// Exported at package level for test inspection of argv and env.
func buildReviewCmd(ctx context.Context, cfg reviewtypes.RunConfig) *exec.Cmd {
	prompt := composeReviewPrompt(cfg)
	cmd := exec.CommandContext(ctx, "claude", "-p", prompt)
	cmd.Env = appendReviewEnv(os.Environ(), "claude-code", cfg, prompt)
	return cmd
}

// appendReviewEnv appends ENTIRE_REVIEW_* vars to the given base environment.
func appendReviewEnv(base []string, agentName string, cfg reviewtypes.RunConfig, prompt string) []string {
	skillsJSON, _ := review.EncodeSkills(cfg.Skills) //nolint:errcheck // EncodeSkills only fails on json.Marshal([]string), which is infallible
	return append(base,
		review.EnvSession+"=1",
		review.EnvAgent+"="+agentName,
		review.EnvSkills+"="+skillsJSON,
		review.EnvPrompt+"="+prompt,
		review.EnvStartingSHA+"="+cfg.StartingSHA,
	)
}

// composeReviewPrompt concatenates Skills, AlwaysPrompt, and PerRunPrompt
// (skipping empty strings) with double-newline separators.
// Full composition with a scope clause lands in CU5.
func composeReviewPrompt(cfg reviewtypes.RunConfig) string {
	parts := make([]string, 0, len(cfg.Skills)+2)
	parts = append(parts, cfg.Skills...)
	parts = append(parts, cfg.AlwaysPrompt, cfg.PerRunPrompt)
	return joinNonEmpty(parts, "\n\n")
}

// joinNonEmpty joins non-empty strings with the given separator.
func joinNonEmpty(parts []string, sep string) string {
	var out strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString(sep)
		}
		out.WriteString(p)
	}
	return out.String()
}

// parseClaudeOutput converts claude's -p mode stdout into a stream of Events.
// In -p mode claude emits the assistant's response as plain text (one line per
// stdout line). The parser emits Started once, then one AssistantText per
// non-empty line, then Finished{Success: true} on clean EOF or
// RunError + Finished{Success: false} on a torn stream (scanner error).
//
// Exposed for golden-file contract testing.
func parseClaudeOutput(r io.Reader) <-chan reviewtypes.Event {
	out := make(chan reviewtypes.Event, 32)
	go func() {
		defer close(out)
		out <- reviewtypes.Started{}
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			out <- reviewtypes.AssistantText{Text: line}
		}
		if err := scanner.Err(); err != nil {
			out <- reviewtypes.RunError{Err: fmt.Errorf("read stdout: %w", err)}
			out <- reviewtypes.Finished{Success: false}
			return
		}
		out <- reviewtypes.Finished{Success: true}
	}()
	return out
}

// reviewProcess is the running claude review process.
type reviewProcess struct {
	cmd    *exec.Cmd
	events chan reviewtypes.Event
}

func (p *reviewProcess) Events() <-chan reviewtypes.Event { return p.events }

// Wait implements Process.Wait. The *exec.ExitError passes through unwrapped
// per the Process.Wait contract; callers may errors.As for *exec.ExitError.
func (p *reviewProcess) Wait() error { return p.cmd.Wait() } //nolint:wrapcheck // Process.Wait contract allows *exec.ExitError passthrough

func (p *reviewProcess) run(stdout io.Reader) {
	defer close(p.events)
	for ev := range parseClaudeOutput(stdout) {
		p.events <- ev
	}
}
