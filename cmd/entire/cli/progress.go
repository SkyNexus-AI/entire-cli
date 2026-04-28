package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/interactive"
)

// spinnerFrames matches the bubbles/spinner Dot frames used by the activity
// TUI, so a CLI spinner here visually matches `entire activity`.
var spinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

const spinnerInterval = 100 * time.Millisecond

// startSpinner prints msg followed by an animated spinner to w until the
// returned stop function is called. The stop function clears the spinner
// line and prints suffix (with a newline) if non-empty.
//
// When w is not a terminal (CI, redirected output, agent subprocess), the
// spinner is suppressed and msg is printed once with a trailing "..." so
// non-interactive callers still see what's happening without ANSI noise.
func startSpinner(w io.Writer, msg string) func(suffix string) {
	if !interactive.IsTerminalWriter(w) {
		fmt.Fprintln(w, msg+"...")
		return func(suffix string) {
			if suffix != "" {
				fmt.Fprintln(w, suffix)
			}
		}
	}

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(spinnerInterval)
		defer ticker.Stop()
		frame := 0
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Fprintf(w, "\r%s %s", spinnerFrames[frame], msg)
				frame = (frame + 1) % len(spinnerFrames)
			}
		}
	}()
	return func(suffix string) {
		close(done)
		<-stopped
		fmt.Fprint(w, "\r\033[K") // clear current line
		if suffix != "" {
			fmt.Fprintln(w, suffix)
		}
	}
}
