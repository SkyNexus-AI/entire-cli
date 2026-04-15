package copilotcli

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

var copilotCommandRunner = exec.CommandContext

// GenerateText sends a prompt to the Copilot CLI and returns the raw text response.
func (c *CopilotCLIAgent) GenerateText(ctx context.Context, prompt string, model string) (string, error) {
	if err := agent.ValidateInlinePromptSize("copilot", prompt); err != nil {
		return "", err
	}

	args := []string{"-p", prompt, "--allow-all-tools"}
	if model != "" {
		args = append(args, "--model", model)
	}

	result, err := agent.RunIsolatedTextGeneratorCLI(ctx, copilotCommandRunner, "copilot", "copilot", args, "")
	if err != nil {
		return "", fmt.Errorf("copilot text generation failed: %w", err)
	}
	return result, nil
}
