package cli

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/versioninfo"
	"github.com/spf13/cobra"
)

func newDoctorBundleCmd() *cobra.Command {
	var outFlag string

	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Produce a diagnostic bundle (zip) for bug reports",
		Long: `Produce a zip archive containing logs, settings, and a git snapshot suitable
for attaching to bug reports.

The archive includes:
  - .entire/logs/  (operational logs)
  - .entire/settings.json and settings.local.json (if present)
  - git-status.txt, git-log.txt, git-remote.txt
  - version.txt with CLI version, Go version, OS/Arch

By default the archive is written to a path inside the OS temp directory and
that path is printed to stdout. Use --out to choose a specific path.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			repoRoot, err := paths.WorktreeRoot(ctx)
			if err != nil {
				cmd.SilenceUsage = true
				return errors.New("not a git repository")
			}

			outPath := outFlag
			if outPath == "" {
				outPath = filepath.Join(os.TempDir(), fmt.Sprintf("entire-bundle-%s.zip", time.Now().UTC().Format("20060102-150405")))
			}

			if err := writeDoctorBundle(ctx, repoRoot, outPath); err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), outPath)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outFlag, "out", "o", "", "Path to write the bundle archive (default: OS temp dir)")
	return cmd
}

func writeDoctorBundle(ctx context.Context, repoRoot, outPath string) error {
	out, err := os.Create(outPath) //nolint:gosec // user-provided output path is intentional
	if err != nil {
		return fmt.Errorf("create bundle: %w", err)
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	logsDir := filepath.Join(repoRoot, logging.LogsDir)
	if err := addDirToZip(zw, logsDir, "logs"); err != nil {
		return err
	}

	for _, name := range []string{"settings.json", "settings.local.json"} {
		src := filepath.Join(repoRoot, ".entire", name)
		if err := addFileToZip(zw, src, filepath.Join("settings", name)); err != nil {
			return err
		}
	}

	if err := addCommandOutput(ctx, zw, "git-status.txt", repoRoot, "git", "status", "--short", "--branch"); err != nil {
		return err
	}
	if err := addCommandOutput(ctx, zw, "git-log.txt", repoRoot, "git", "log", "-n", "50", "--oneline"); err != nil {
		return err
	}
	if err := addCommandOutput(ctx, zw, "git-remote.txt", repoRoot, "git", "remote", "-v"); err != nil {
		return err
	}

	if err := addStringToZip(zw, "version.txt", versionInfoString()); err != nil {
		return err
	}

	return nil
}

func versionInfoString() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Entire CLI %s (%s)\n", versioninfo.Version, versioninfo.Commit)
	fmt.Fprintf(&sb, "Go: %s\n", runtime.Version())
	fmt.Fprintf(&sb, "OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return sb.String()
}

func addDirToZip(zw *zip.Writer, srcDir, archivePrefix string) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", srcDir, err)
	}
	if !info.IsDir() {
		return nil
	}
	walkErr := filepath.Walk(srcDir, func(path string, fi os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if fi.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("rel: %w", err)
		}
		return addFileToZip(zw, path, filepath.Join(archivePrefix, rel))
	})
	if walkErr != nil {
		return fmt.Errorf("walk %s: %w", srcDir, walkErr)
	}
	return nil
}

func addFileToZip(zw *zip.Writer, src, archivePath string) error {
	f, err := os.Open(src) //nolint:gosec // path comes from repo-internal walk
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer f.Close()

	w, err := zw.Create(archivePath)
	if err != nil {
		return fmt.Errorf("zip create %s: %w", archivePath, err)
	}
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("zip copy %s: %w", archivePath, err)
	}
	return nil
}

func addStringToZip(zw *zip.Writer, archivePath, contents string) error {
	w, err := zw.Create(archivePath)
	if err != nil {
		return fmt.Errorf("zip create %s: %w", archivePath, err)
	}
	if _, err := io.WriteString(w, contents); err != nil {
		return fmt.Errorf("zip write %s: %w", archivePath, err)
	}
	return nil
}

func addCommandOutput(ctx context.Context, zw *zip.Writer, archivePath, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		out = append(out, []byte(fmt.Sprintf("\n[error: %v]\n", err))...)
	}
	return addStringToZip(zw, archivePath, string(out))
}
