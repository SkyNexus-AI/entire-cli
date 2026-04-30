package codex

import (
	"io"
	"strings"
	"testing"
)

func TestFilterLine_BannerDropped(t *testing.T) {
	t.Parallel()
	banners := []string{
		"─────── codex ───────",
		"─────────────────────",
		"version 0.1.0 (linux)",
	}
	for _, line := range banners {
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			_, ok := FilterLine(line)
			if ok {
				t.Errorf("FilterLine(%q) returned ok=true; expected banner to be dropped", line)
			}
		})
	}
}

func TestFilterLine_ExecBlockDropped(t *testing.T) {
	t.Parallel()
	cases := []string{
		"exec",
		"exec node /usr/local/lib/codex/hooks.js in /repo",
		"exec git diff HEAD in /home/user/project",
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			_, ok := FilterLine(line)
			if ok {
				t.Errorf("FilterLine(%q) returned ok=true; expected exec block to be dropped", line)
			}
		})
	}
}

func TestFilterLine_ExecNarrativeKept(t *testing.T) {
	t.Parallel()
	// Lines that contain "exec" but are not exec-block headers must pass through.
	narratives := []string{
		"The executor completed the task successfully.",
		"exec succeeded (exit 0)",
		"Running the executable with --flag",
		"codex will execute the review skills",
	}
	for _, line := range narratives {
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			got, ok := FilterLine(line)
			if !ok {
				t.Errorf("FilterLine(%q) dropped a narrative line; expected it to pass through", line)
			}
			if got == "" {
				t.Errorf("FilterLine(%q) returned empty string for passing line", line)
			}
		})
	}
}

func TestFilterLine_HookFiringDropped(t *testing.T) {
	t.Parallel()
	cases := []string{
		"[hooks] firing user-prompt-submit for session abc123",
		"[hooks] firing stop for session abc123",
		"[hooks] some hook notice",
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			_, ok := FilterLine(line)
			if ok {
				t.Errorf("FilterLine(%q) returned ok=true; expected hook notice to be dropped", line)
			}
		})
	}
}

func TestFilterLine_CSISequenceStripped(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		// Cursor hide — line becomes empty after stripping → dropped.
		{"\x1b[?25l", ""},
		// Cursor show — same.
		{"\x1b[?25h", ""},
		// Erase line — same.
		{"\x1b[2K", ""},
		// CSI embedded in narrative — stripped but line remains.
		{"\x1b[32mgreen text\x1b[0m", "green text"},
		// Semicolon in CSI parameter.
		{"\x1b[1;32mbolded\x1b[0m", "bolded"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			got, ok := FilterLine(tc.raw)
			if tc.want == "" {
				if ok {
					t.Errorf("FilterLine(%q) returned ok=true; expected CSI-only line to be dropped", tc.raw)
				}
			} else {
				if !ok {
					t.Errorf("FilterLine(%q) dropped line; expected %q", tc.raw, tc.want)
				}
				if got != tc.want {
					t.Errorf("FilterLine(%q) = %q, want %q", tc.raw, got, tc.want)
				}
			}
		})
	}
}

func TestFilterLine_TimestampLogDropped(t *testing.T) {
	t.Parallel()
	cases := []string{
		"2026-04-30T10:00:00.000Z ERROR: hook failed",
		"2026-04-30T10:00:00Z INFO: session started",
		"2026-01-01T00:00:00.000Z DEBUG: verbose output",
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			_, ok := FilterLine(line)
			if ok {
				t.Errorf("FilterLine(%q) returned ok=true; expected timestamp log to be dropped", line)
			}
		})
	}
}

func TestFilterLine_NarrativePassThrough(t *testing.T) {
	t.Parallel()
	narratives := []string{
		"I've reviewed the changes on this branch.",
		"The `AgentReviewer` interface provides a clean abstraction.",
		"**Key observations:**",
		"1. The `Event` sealed sum type is correctly implemented.",
		"No blocking issues found.",
	}
	for _, line := range narratives {
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			got, ok := FilterLine(line)
			if !ok {
				t.Errorf("FilterLine(%q) dropped narrative; expected passthrough", line)
			}
			if got == "" {
				t.Errorf("FilterLine(%q) returned empty string for narrative", line)
			}
		})
	}
}

func TestFilterLine_VersionNarrativeKept(t *testing.T) {
	t.Parallel()
	cases := []string{
		"version 1.2.3 of go-git fixes the issue",
		"The version 1.0 release notes mention this regression.",
		"Bump version 2.5.7 — see changelog.",
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			got, ok := FilterLine(line)
			if !ok {
				t.Errorf("benign narrative %q got dropped", line)
			}
			if got == "" {
				t.Errorf("benign narrative %q returned empty string", line)
			}
		})
	}
}

func TestStrip_FullFixture(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		"─────── codex ───────",
		"version 0.1.0 (linux)",
		"",
		"[hooks] firing user-prompt-submit for session abc123",
		"exec node /usr/local/lib/codex/hooks.js in /repo",
		"",
		"\x1b[?25l",
		"This is the narrative output from the agent.",
		"It spans multiple lines.",
		"\x1b[?25h",
		"exec git diff HEAD in /repo",
		"2026-04-30T10:00:00.000Z ERROR: something logged",
		"Final conclusion: no issues found.",
	}, "\n")

	filtered := Strip(strings.NewReader(input))
	data, err := io.ReadAll(filtered)
	if err != nil {
		t.Fatalf("Strip read: %v", err)
	}
	output := string(data)

	// Chrome must be absent.
	chromeMustBeAbsent := []string{
		"─────── codex",
		"version 0.1.0",
		"[hooks]",
		"exec node",
		"exec git diff",
		"\x1b[",
		"2026-04-30T",
		"ERROR:",
	}
	for _, pattern := range chromeMustBeAbsent {
		if strings.Contains(output, pattern) {
			t.Errorf("chrome pattern %q must not appear in filtered output; got:\n%s", pattern, output)
		}
	}

	// Narrative must survive.
	narrativeMustSurvive := []string{
		"This is the narrative output from the agent.",
		"It spans multiple lines.",
		"Final conclusion: no issues found.",
	}
	for _, want := range narrativeMustSurvive {
		if !strings.Contains(output, want) {
			t.Errorf("narrative %q missing from filtered output; got:\n%s", want, output)
		}
	}
}
