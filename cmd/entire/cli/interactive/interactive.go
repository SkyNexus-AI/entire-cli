// Package interactive provides TTY-related helpers shared between the cli
// and strategy packages without inducing an import cycle (strategy cannot
// import cli).
package interactive

import "os"

// CanPromptInteractively checks if /dev/tty is available for interactive prompts.
// Returns false when running as an agent subprocess (no controlling terminal).
//
// In test environments, ENTIRE_TEST_TTY overrides the real check:
//   - ENTIRE_TEST_TTY=1 → simulate human (TTY available)
//   - ENTIRE_TEST_TTY=0 → simulate agent (no TTY)
func CanPromptInteractively() bool {
	if v := os.Getenv("ENTIRE_TEST_TTY"); v != "" {
		return v == "1"
	}

	// Gemini CLI sets GEMINI_CLI=1 when running shell commands.
	// Gemini subprocesses may have access to the user's TTY, but they can't
	// actually respond to interactive prompts. Treat them as non-TTY.
	// See: https://geminicli.com/docs/tools/shell/
	if os.Getenv("GEMINI_CLI") != "" {
		return false
	}

	// Copilot CLI sets COPILOT_CLI=1 when running hook subprocesses (v0.0.421+).
	// Like Gemini, the subprocess may inherit the user's TTY but can't respond
	// to interactive prompts.
	if os.Getenv("COPILOT_CLI") != "" {
		return false
	}

	// Pi Coding Agent sets PI_CODING_AGENT=true when running shell commands.
	// Like other agents, the subprocess may inherit the TTY but can't respond
	// to interactive prompts.
	if os.Getenv("PI_CODING_AGENT") != "" {
		return false
	}

	// GIT_TERMINAL_PROMPT=0 disables git's own terminal prompts.
	// Factory AI Droid (and other non-interactive environments like CI) set this.
	// Since we run as a git hook, respect it — if the environment doesn't want
	// git prompting, our hook shouldn't prompt either.
	if os.Getenv("GIT_TERMINAL_PROMPT") == "0" {
		return false
	}

	// CI=<non-empty> is the de-facto convention across CI providers (GitHub
	// Actions, CircleCI, GitLab, Travis, Buildkite, etc.). Self-hosted runners
	// and some Docker configs expose /dev/tty, so the TTY probe below isn't
	// enough — an interactive prompt on CI would hang the pipeline forever.
	if os.Getenv("CI") != "" {
		return false
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = tty.Close()
	return true
}
