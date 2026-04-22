// Package interactive provides TTY-related helpers shared between the cli
// and strategy packages without inducing an import cycle (strategy cannot
// import cli).
package interactive

import (
	"io"
	"os"

	"golang.org/x/term"
)

// CanPromptInteractively reports whether interactive confirmation prompts
// (huh forms, yes/no questions, etc.) can be shown. Returns false in CI,
// tests without ENTIRE_TEST_TTY=1, agent subprocesses that inherit a TTY
// but can't respond to prompts, and other environments without a
// controlling TTY.
//
// ENTIRE_TEST_TTY overrides every other check so tests can exercise both
// interactive and non-interactive paths deterministically without a real pty:
//   - ENTIRE_TEST_TTY=1 forces interactive mode on
//   - any other non-empty value forces interactive mode off
//   - unset falls through to agent-env guards and /dev/tty probing
func CanPromptInteractively() bool {
	if v, ok := os.LookupEnv("ENTIRE_TEST_TTY"); ok {
		return v == "1"
	}

	// Agent subprocesses may inherit the user's TTY but can't respond to
	// interactive prompts. Treat them as non-TTY.
	//   - GEMINI_CLI=1: Gemini CLI shell tool (https://geminicli.com/docs/tools/shell/)
	//   - COPILOT_CLI=1: Copilot CLI hook subprocesses (v0.0.421+)
	//   - PI_CODING_AGENT=true: Pi Coding Agent shell tool
	//   - GIT_TERMINAL_PROMPT=0: caller (CI, Factory AI Droid, etc.) asked
	//     git to stop prompting; respect it from git-hook context too.
	if os.Getenv("GEMINI_CLI") != "" ||
		os.Getenv("COPILOT_CLI") != "" ||
		os.Getenv("PI_CODING_AGENT") != "" ||
		os.Getenv("GIT_TERMINAL_PROMPT") == "0" {
		return false
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = tty.Close()
	return true
}

// IsTerminalWriter reports whether w is an *os.File backed by a terminal.
// Use for deciding on color, pager, progress bars, or other writer-scoped
// TTY formatting. For "can I prompt the user?" use CanPromptInteractively.
func IsTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd())) //nolint:gosec // G115: uintptr->int is safe for fd
}
