package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/entireio/cli/cmd/entire/cli/paths"
)

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

// copyFile copies a file from src to dst.
// Both paths must be absolute after cleaning; dst must reside under either the
// repo worktree root, the git common dir, or the user's home directory (for
// agent session dirs such as ~/.claude/).
func copyFile(src, dst string) error {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)

	if !filepath.IsAbs(dst) {
		return fmt.Errorf("copyFile: dst must be absolute, got %q", dst)
	}
	if err := validateCopyDst(dst); err != nil {
		return err
	}

	input, err := os.ReadFile(src)
	if err != nil {
		return err //nolint:wrapcheck // already present in codebase
	}
	if err := os.WriteFile(dst, input, 0o600); err != nil { //nolint:gosec // dst validated above
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

// validateCopyDst ensures dst is under an allowed directory: the repo worktree,
// the user's home directory (for agent session dirs like ~/.claude/), or the
// system temp directory (used during tests).
func validateCopyDst(dst string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	repoRoot, err := paths.WorktreeRoot(context.Background())
	if err != nil {
		repoRoot = ""
	}
	tmpDir := os.TempDir()

	allowed := make([]string, 0, 3)
	if repoRoot != "" {
		allowed = append(allowed, repoRoot)
	}
	if home != "" {
		allowed = append(allowed, home)
	}
	if tmpDir != "" {
		// Resolve symlinks: on macOS os.TempDir() returns /var/folders/...
		// but t.TempDir() resolves through /private/var/folders/...
		if resolved, err := filepath.EvalSymlinks(tmpDir); err == nil {
			allowed = append(allowed, resolved)
		}
		allowed = append(allowed, tmpDir)
	}

	// Also resolve dst symlinks for consistent comparison (on macOS, /var → /private/var)
	resolvedDst := dst
	if r, err := filepath.EvalSymlinks(filepath.Dir(dst)); err == nil {
		resolvedDst = filepath.Join(r, filepath.Base(dst))
	}

	for _, dir := range allowed {
		if strings.HasPrefix(dst, dir+string(filepath.Separator)) || dst == dir {
			return nil
		}
		if strings.HasPrefix(resolvedDst, dir+string(filepath.Separator)) || resolvedDst == dir {
			return nil
		}
	}

	return fmt.Errorf("copyFile: dst %q is outside allowed directories", dst)
}
