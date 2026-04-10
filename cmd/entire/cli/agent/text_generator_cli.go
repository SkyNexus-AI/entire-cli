package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// TextCommandRunner matches exec.CommandContext and allows tests to inject a runner.
type TextCommandRunner func(ctx context.Context, name string, args ...string) *exec.Cmd

// RunIsolatedTextGeneratorCLI executes a text-generation CLI in an isolated temp
// directory with all GIT_* environment variables removed. This avoids recursive
// hook triggers and repo side effects while preserving provider-specific flags.
func RunIsolatedTextGeneratorCLI(ctx context.Context, runner TextCommandRunner, binary, displayName string, args []string, stdin string) (string, error) {
	if runner == nil {
		runner = exec.CommandContext
	}

	cmd := runner(ctx, binary, args...)
	cmd.Dir = os.TempDir()
	cmd.Env = StripGitEnv(os.Environ())
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", fmt.Errorf("%s CLI not found: %w", displayName, err)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			detail := stderr.String()
			if detail == "" {
				detail = stdout.String()
			}
			return "", fmt.Errorf("%s CLI failed (exit %d): %s", displayName, exitErr.ExitCode(), detail)
		}
		return "", fmt.Errorf("failed to run %s CLI: %w", displayName, err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

func StripGitEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "GIT_") {
			filtered = append(filtered, e)
		}
	}
	return filtered
}
