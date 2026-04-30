package codex

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

// Reviewer is the AgentReviewer implementation for codex.
//
// Argv shape: codex exec --skip-git-repo-check -.
// Prompt is piped via stdin (the trailing "-" tells codex to read from stdin).
// Stdout includes chrome (banners, hook notices, exec blocks, CSI sequences)
// that output_filter.go strips before emitting AssistantText events.
type Reviewer struct{}

// NewReviewer creates a new codex AgentReviewer.
func NewReviewer() *Reviewer { return &Reviewer{} }

// Compile-time interface check.
var _ reviewtypes.AgentReviewer = (*Reviewer)(nil)

// Name returns the agent's registry key.
func (*Reviewer) Name() string { return "codex" }

// Start spawns codex with the review prompt on stdin and returns a streaming Process.
func (r *Reviewer) Start(ctx context.Context, cfg reviewtypes.RunConfig) (reviewtypes.Process, error) {
	cmd := buildCodexReviewCmd(ctx, cfg)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codex: start: %w", err)
	}
	p := &codexReviewProcess{cmd: cmd, events: make(chan reviewtypes.Event, 32)}
	go p.run(stdout)
	return p, nil
}

// buildCodexReviewCmd builds the exec.Cmd for a codex review run.
// Exported at package level for test inspection of argv, stdin, and env.
func buildCodexReviewCmd(ctx context.Context, cfg reviewtypes.RunConfig) *exec.Cmd {
	prompt := composeCodexReviewPrompt(cfg)
	cmd := exec.CommandContext(ctx, "codex", "exec", "--skip-git-repo-check", "-")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = appendCodexReviewEnv(os.Environ(), cfg, prompt)
	return cmd
}

// appendCodexReviewEnv appends ENTIRE_REVIEW_* vars to the given base environment.
func appendCodexReviewEnv(base []string, cfg reviewtypes.RunConfig, prompt string) []string {
	skillsJSON, _ := review.EncodeSkills(cfg.Skills) //nolint:errcheck // EncodeSkills only fails on json.Marshal([]string), which is infallible
	return append(base,
		review.EnvSession+"=1",
		review.EnvAgent+"=codex",
		review.EnvSkills+"="+skillsJSON,
		review.EnvPrompt+"="+prompt,
		review.EnvStartingSHA+"="+cfg.StartingSHA,
	)
}

// composeCodexReviewPrompt concatenates Skills, AlwaysPrompt, and PerRunPrompt
// (skipping empty strings) with double-newline separators.
func composeCodexReviewPrompt(cfg reviewtypes.RunConfig) string {
	parts := make([]string, 0, len(cfg.Skills)+2)
	parts = append(parts, cfg.Skills...)
	parts = append(parts, cfg.AlwaysPrompt, cfg.PerRunPrompt)
	var out strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(p)
	}
	return out.String()
}

// parseCodexOutput wraps the reader with the chrome filter and converts
// remaining lines into a stream of Events.
// On clean EOF emits Finished{Success: true}. On a scanner error (including
// errors propagated from Strip via pipe CloseWithError) emits RunError then
// Finished{Success: false}.
//
// Exposed for golden-file contract testing.
func parseCodexOutput(r io.Reader) <-chan reviewtypes.Event {
	out := make(chan reviewtypes.Event, 32)
	go func() {
		defer close(out)
		out <- reviewtypes.Started{}
		scanner := bufio.NewScanner(Strip(r))
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

// codexReviewProcess is the running codex review process.
type codexReviewProcess struct {
	cmd    *exec.Cmd
	events chan reviewtypes.Event
}

func (p *codexReviewProcess) Events() <-chan reviewtypes.Event { return p.events }

// Wait implements Process.Wait. The *exec.ExitError passes through unwrapped
// per the Process.Wait contract; callers may errors.As for *exec.ExitError.
func (p *codexReviewProcess) Wait() error { return p.cmd.Wait() } //nolint:wrapcheck // Process.Wait contract allows *exec.ExitError passthrough

func (p *codexReviewProcess) run(stdout io.Reader) {
	defer close(p.events)
	for ev := range parseCodexOutput(stdout) {
		p.events <- ev
	}
}
