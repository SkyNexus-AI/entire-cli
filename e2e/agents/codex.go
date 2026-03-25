package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func init() {
	if env := os.Getenv("E2E_AGENT"); env != "" && env != "codex" {
		return
	}
	Register(&Codex{})
	RegisterGate("codex", 2)
}

// Codex implements the E2E Agent interface for OpenAI's Codex CLI.
type Codex struct{}

func (c *Codex) Name() string               { return "codex" }
func (c *Codex) Binary() string             { return "codex" }
func (c *Codex) EntireAgent() string        { return "codex" }
func (c *Codex) PromptPattern() string      { return `>` }
func (c *Codex) TimeoutMultiplier() float64 { return 1.5 }

func (c *Codex) Bootstrap() error {
	// On CI, ensure OPENAI_API_KEY is available.
	if os.Getenv("CI") == "" {
		return nil
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		return nil
	}
	return nil
}

func (c *Codex) IsTransientError(out Output, err error) bool {
	if err == nil {
		return false
	}
	combined := out.Stdout + out.Stderr
	for _, p := range []string{"overloaded", "rate limit", "rate_limit", "503", "529", "ECONNRESET", "ETIMEDOUT"} {
		if strings.Contains(combined, p) {
			return true
		}
	}
	return false
}

// codexHome creates an isolated CODEX_HOME for a test run.
// This keeps trust entries and feature flags out of the real ~/.codex/config.toml.
// Auth still works via OPENAI_API_KEY env var (checked before CODEX_HOME).
func codexHome() (string, func(), error) {
	dir, err := os.MkdirTemp("", "codex-home-*")
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

func (c *Codex) RunPrompt(ctx context.Context, dir string, prompt string, opts ...Option) (Output, error) {
	cfg := &runConfig{}
	for _, o := range opts {
		o(cfg)
	}

	home, cleanup, err := codexHome()
	if err != nil {
		return Output{}, fmt.Errorf("create codex home: %w", err)
	}
	defer cleanup()

	args := []string{
		"exec",
		"--dangerously-bypass-approvals-and-sandbox",
	}
	if cfg.Model != "" {
		args = append(args, "-m", cfg.Model)
	}
	args = append(args, prompt)

	displayArgs := []string{
		"exec",
		"--dangerously-bypass-approvals-and-sandbox",
	}
	if cfg.Model != "" {
		displayArgs = append(displayArgs, "-m", cfg.Model)
	}
	displayArgs = append(displayArgs, fmt.Sprintf("%q", prompt))

	env := append(filterEnv(os.Environ(), "ENTIRE_TEST_TTY", "CODEX_HOME"),
		"CODEX_HOME="+home,
	)

	// entire enable wrote trust + feature flag via CODEX_HOME during SetupRepo.
	// But SetupRepo's CODEX_HOME pointed at a different temp dir. We need to
	// re-seed trust for this isolated home so Codex loads .codex/hooks.json.
	absDir, _ := filepath.Abs(dir)
	if err := seedCodexHome(home, absDir); err != nil {
		return Output{}, fmt.Errorf("seed codex home: %w", err)
	}

	cmd := exec.CommandContext(ctx, c.Binary(), args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	exitCode := 0
	if err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return Output{
		Command:  c.Binary() + " " + strings.Join(displayArgs, " "),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, err
}

func (c *Codex) StartSession(ctx context.Context, dir string) (Session, error) {
	name := fmt.Sprintf("codex-test-%d", time.Now().UnixNano())

	home, cleanup, err := codexHome()
	if err != nil {
		return nil, fmt.Errorf("create codex home: %w", err)
	}

	absDir, _ := filepath.Abs(dir)
	if err := seedCodexHome(home, absDir); err != nil {
		cleanup()
		return nil, fmt.Errorf("seed codex home: %w", err)
	}

	env := filterEnv(os.Environ(), "ENTIRE_TEST_TTY", "CODEX_HOME")

	s, err := NewTmuxSession(name, dir, nil, "env",
		"CODEX_HOME="+home,
		"codex", "--dangerously-bypass-approvals-and-sandbox",
	)
	if err != nil {
		cleanup()
		return nil, err
	}
	s.OnClose(cleanup)

	// Forward API key if present
	for _, e := range env {
		if strings.HasPrefix(e, "OPENAI_API_KEY=") {
			_ = s.Send("export " + e)
		}
	}

	if _, err := s.WaitFor(c.PromptPattern(), 30*time.Second); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("waiting for codex prompt: %w", err)
	}

	return s, nil
}

// seedCodexHome writes trust + feature flag config so Codex loads the
// project's .codex/ layer. This mirrors what `entire enable` does via
// ensureProjectTrusted, but into the isolated E2E CODEX_HOME.
func seedCodexHome(home, projectDir string) error {
	config := fmt.Sprintf("[features]\ncodex_hooks = true\n\n[projects.%q]\ntrust_level = \"trusted\"\n", projectDir)
	if err := os.MkdirAll(home, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600)
}
