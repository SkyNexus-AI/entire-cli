package cli

import (
	"bytes"
	"context"
	"io"
	"testing"

	dispatchpkg "github.com/entireio/cli/cmd/entire/cli/dispatch"
	"github.com/spf13/cobra"
)

func TestParseDispatchFlags_OrgDefaultsToAllBranches(t *testing.T) {
	t.Parallel()

	opts, err := parseDispatchFlags(
		&cobra.Command{},
		false,
		"7d",
		"",
		false,
		nil,
		"entireio",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if opts.AllBranches {
		t.Fatal("did not expect org mode without --all-branches to default to all branches")
	}
	if opts.Branches != nil {
		t.Fatalf("expected nil branches, got %v", opts.Branches)
	}
	if !opts.Generate {
		t.Fatal("expected dispatch generation to default on")
	}
}

func TestParseDispatchFlags_ServerReposAreAllowed(t *testing.T) {
	t.Parallel()

	opts, err := parseDispatchFlags(
		&cobra.Command{},
		false,
		"7d",
		"",
		false,
		[]string{"entireio/cli", "entireio/entire.io"},
		"",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(opts.RepoPaths); got != 2 {
		t.Fatalf("expected 2 repo slugs, got %d", got)
	}
	if opts.Mode != 0 {
		t.Fatalf("expected server mode, got %v", opts.Mode)
	}
	if opts.Branches != nil {
		t.Fatalf("expected nil branches for server repo default-branch mode, got %v", opts.Branches)
	}
	if opts.AllBranches {
		t.Fatal("did not expect all branches for server repo default-branch mode")
	}
	if !opts.Generate {
		t.Fatal("expected dispatch generation to default on")
	}
}

func TestParseDispatchFlags_LocalRejectsRepos(t *testing.T) {
	t.Parallel()

	_, err := parseDispatchFlags(
		&cobra.Command{},
		true,
		"7d",
		"",
		false,
		[]string{"entireio/cli"},
		"",
		"",
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "--repos cannot be used with --local" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDispatchFlags_LocalRejectsOrg(t *testing.T) {
	t.Parallel()

	_, err := parseDispatchFlags(
		&cobra.Command{},
		true,
		"7d",
		"",
		false,
		nil,
		"entireio",
		"",
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "--org cannot be used with --local" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDispatchFlags_AllBranchesFlag(t *testing.T) {
	t.Parallel()

	opts, err := parseDispatchFlags(
		&cobra.Command{},
		false,
		"7d",
		"",
		true,
		nil,
		"",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.AllBranches {
		t.Fatal("expected all branches to propagate")
	}
	if opts.Branches != nil {
		t.Fatalf("expected nil branches, got %v", opts.Branches)
	}
}

func TestShouldRunDispatchWizard(t *testing.T) {
	t.Parallel()

	if !shouldRunDispatchWizard(0, true, true) {
		t.Fatal("expected wizard to run when stdin and stdout are terminals with no flags")
	}
	if shouldRunDispatchWizard(0, false, true) {
		t.Fatal("expected wizard not to run when stdin is piped")
	}
	if shouldRunDispatchWizard(1, true, true) {
		t.Fatal("expected wizard not to run when flags are provided")
	}
}

func TestNewDispatchCmd_DoesNotExposeGenerateFlag(t *testing.T) {
	t.Parallel()

	cmd := newDispatchCmd()
	if cmd.Flags().Lookup("generate") != nil {
		t.Fatal("expected dispatch command not to expose generate")
	}
}

func TestNewDispatchCmd_DoesNotExposeBranchesFlag(t *testing.T) {
	t.Parallel()

	cmd := newDispatchCmd()
	if cmd.Flags().Lookup("branches") != nil {
		t.Fatal("expected dispatch command not to expose branches")
	}
}

func TestNewDispatchCmd_ExposesAllBranchesFlag(t *testing.T) {
	t.Parallel()

	cmd := newDispatchCmd()
	if cmd.Flags().Lookup("all-branches") == nil {
		t.Fatal("expected dispatch command to expose all-branches")
	}
}

func TestNewDispatchCmd_DoesNotExposeDryRunFlag(t *testing.T) {
	t.Parallel()

	cmd := newDispatchCmd()
	if cmd.Flags().Lookup("dry-run") != nil {
		t.Fatal("expected dispatch command not to expose dry-run")
	}
}

func TestNewDispatchCmd_DoesNotExposeWaitFlag(t *testing.T) {
	t.Parallel()

	cmd := newDispatchCmd()
	if cmd.Flags().Lookup("wait") != nil {
		t.Fatal("expected dispatch command not to expose wait")
	}
}

func TestNewDispatchCmd_DoesNotExposeFormatFlag(t *testing.T) {
	t.Parallel()

	cmd := newDispatchCmd()
	if cmd.Flags().Lookup("format") != nil {
		t.Fatal("expected dispatch command not to expose format")
	}
}

func TestNewDispatchCmd_NonTerminalPrintsPlainMarkdown(t *testing.T) {
	oldRunDispatch := runDispatch
	oldTerminalMode := dispatchTerminalMode
	oldMarkdown := renderDispatchMarkdown
	runDispatch = func(_ context.Context, opts dispatchpkg.Options) (*dispatchpkg.Dispatch, error) {
		if !opts.Generate {
			t.Fatal("expected generate flag to propagate")
		}
		return &dispatchpkg.Dispatch{GeneratedText: "generated dispatch", Generated: true}, nil
	}
	dispatchTerminalMode = func(_ io.Writer) bool { return false }
	renderDispatchMarkdown = func(dispatch *dispatchpkg.Dispatch) string {
		if dispatch.GeneratedText != "generated dispatch" {
			t.Fatalf("unexpected dispatch: %+v", dispatch)
		}
		return "# generated dispatch\n"
	}
	t.Cleanup(func() {
		runDispatch = oldRunDispatch
		dispatchTerminalMode = oldTerminalMode
		renderDispatchMarkdown = oldMarkdown
	})

	cmd := newDispatchCmd()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--repos", "entireio/cli"})
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "# generated dispatch\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestNewDispatchCmd_TerminalUsesInteractiveRenderer(t *testing.T) {
	oldTerminalMode := dispatchTerminalMode
	oldInteractive := runInteractiveDispatch
	oldGlow := renderTerminalMarkdown
	dispatchTerminalMode = func(_ io.Writer) bool { return true }
	runInteractiveDispatch = func(_ context.Context, _ io.Writer, opts dispatchpkg.Options) (string, error) {
		if !opts.Generate {
			t.Fatal("expected generation to be on by default")
		}
		return "# generated dispatch\n", nil
	}
	renderTerminalMarkdown = func(_ io.Writer, markdown string) (string, error) {
		if markdown != "# generated dispatch\n" {
			t.Fatalf("unexpected markdown: %q", markdown)
		}
		return "glow output\n", nil
	}
	t.Cleanup(func() {
		dispatchTerminalMode = oldTerminalMode
		runInteractiveDispatch = oldInteractive
		renderTerminalMarkdown = oldGlow
	})

	cmd := newDispatchCmd()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--repos", "entireio/cli"})
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "glow output\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("unexpected stderr: %q", got)
	}
}
