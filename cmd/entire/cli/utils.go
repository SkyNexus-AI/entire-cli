package cli

import (
	"fmt"
	"os"

	"entire.io/cli/cmd/entire/cli/stringutil"

	"github.com/charmbracelet/huh"
)

// TruncateRunes truncates a string to at most maxRunes runes, appending suffix if truncated.
// This is safe for multi-byte UTF-8 characters unlike byte-based slicing.
func TruncateRunes(s string, maxRunes int, suffix string) string {
	return stringutil.TruncateRunes(s, maxRunes, suffix)
}

// CapitalizeFirst capitalizes the first rune of a string.
// This is safe for multi-byte UTF-8 characters unlike byte indexing.
func CapitalizeFirst(s string) string {
	return stringutil.CapitalizeFirst(s)
}

// IsAccessibleMode returns true if accessibility mode should be enabled.
// This checks the ACCESSIBLE environment variable.
// Set ACCESSIBLE=1 (or any non-empty value) to enable accessible mode,
// which uses simpler prompts that work better with screen readers.
func IsAccessibleMode() bool {
	return os.Getenv("ACCESSIBLE") != ""
}

// entireTheme returns the Dracula theme for consistent styling.
func entireTheme() *huh.Theme {
	return huh.ThemeDracula()
}

// NewAccessibleForm creates a new huh form with accessibility mode
// enabled if the ACCESSIBLE environment variable is set.
// Note: WithAccessible() is only available on forms, not individual fields.
// Always wrap confirmations and other prompts in a form to enable accessibility.
func NewAccessibleForm(groups ...*huh.Group) *huh.Form {
	form := huh.NewForm(groups...).WithTheme(entireTheme())
	if IsAccessibleMode() {
		form = form.WithAccessible(true)
	}
	return form
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	input, err := os.ReadFile(src) //nolint:gosec // Reading from controlled git metadata path
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, input, 0o600); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}
