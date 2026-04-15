package cursor

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

var cursorCommandRunner = exec.CommandContext

// GenerateText sends a prompt to the Cursor agent CLI and returns the raw text response.
func (c *CursorAgent) GenerateText(ctx context.Context, prompt string, model string) (string, error) {
	if err := agent.ValidateInlinePromptSize("cursor", prompt); err != nil {
		return "", err
	}

	args := []string{"-p", prompt, "--force", "--trust", "--workspace", os.TempDir()}
	if model != "" {
		args = append(args, "--model", model)
	}

	result, err := agent.RunIsolatedTextGeneratorCLI(ctx, cursorCommandRunner, "agent", "cursor", args, "")
	if err != nil {
		return "", fmt.Errorf("cursor text generation failed: %w", err)
	}
	return result, nil
}
