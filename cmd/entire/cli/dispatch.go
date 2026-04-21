package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	dispatchpkg "github.com/entireio/cli/cmd/entire/cli/dispatch"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var runDispatch = dispatchpkg.Run
var renderDispatchMarkdown = dispatchpkg.RenderMarkdown
var dispatchTerminalMode = func(w io.Writer) bool { return isTerminalWriter(w) }
var runInteractiveDispatch = defaultRunInteractiveDispatch
var renderTerminalMarkdown = defaultRenderTerminalMarkdown

func newDispatchCmd() *cobra.Command {
	var (
		flagLocal       bool
		flagSince       string
		flagUntil       string
		flagAllBranches bool
		flagRepos       []string
		flagOrg         string
		flagVoice       string
	)

	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Generate a dispatch summarizing recent agent work",
		Long: `Generate a dispatch summarizing recent agent work.

Examples:
  entire dispatch
  entire dispatch --since 14d --all-branches
  entire dispatch --local
  entire dispatch --voice neutral`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var (
				opts dispatchpkg.Options
				err  error
			)

			if shouldRunDispatchWizard(cmd.Flags().NFlag(), isTerminalStdin(os.Stdin), isTerminalWriter(cmd.OutOrStdout())) {
				opts, err = runDispatchWizard(cmd)
			} else {
				opts, err = parseDispatchFlags(cmd, flagLocal, flagSince, flagUntil, flagAllBranches, flagRepos, flagOrg, flagVoice)
			}
			if err != nil {
				if errors.Is(err, errDispatchCancelled) {
					return nil
				}
				return err
			}

			if err := runDispatchCommand(cmd.Context(), cmd.OutOrStdout(), opts); err != nil {
				if errors.Is(err, errDispatchCancelled) {
					return nil
				}
				return err
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagLocal, "local", false, "use local LLM tokens instead of server synthesis")
	cmd.Flags().StringVar(&flagSince, "since", "7d", "time window (Go duration, relative time, or ISO date)")
	cmd.Flags().StringVar(&flagUntil, "until", "", "window end time (defaults to now)")
	cmd.Flags().BoolVar(&flagAllBranches, "all-branches", false, "include all branches instead of the default branch scope")
	cmd.Flags().StringSliceVar(&flagRepos, "repos", nil, "server repo slugs (for example entireio/cli)")
	cmd.Flags().StringVar(&flagOrg, "org", "", "enumerate checkpoints across an org")
	cmd.Flags().StringVar(&flagVoice, "voice", "", "voice preset name, file path, or literal description")

	return cmd
}

func runDispatchCommand(ctx context.Context, outW io.Writer, opts dispatchpkg.Options) error {
	if dispatchTerminalMode(outW) {
		markdown, err := runInteractiveDispatch(ctx, outW, opts)
		if err != nil {
			return err
		}
		rendered, err := renderTerminalMarkdown(outW, markdown)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(outW, rendered)
		return err
	}

	result, err := runDispatch(ctx, opts)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(outW, renderDispatchMarkdown(result))
	return err
}

func isTerminalStdin(file *os.File) bool {
	return term.IsTerminal(int(file.Fd()))
}

func shouldRunDispatchWizard(flagCount int, stdinIsTerminal bool, stdoutIsTerminal bool) bool {
	return flagCount == 0 && stdinIsTerminal && stdoutIsTerminal
}

func parseDispatchFlags(
	cmd *cobra.Command,
	flagLocal bool,
	flagSince string,
	flagUntil string,
	flagAllBranches bool,
	flagRepos []string,
	flagOrg string,
	flagVoice string,
) (dispatchpkg.Options, error) {
	return resolveDispatchOptions(
		flagLocal,
		flagSince,
		flagUntil,
		flagAllBranches,
		flagRepos,
		flagOrg,
		flagVoice,
		func() (string, error) {
			return GetCurrentBranch(cmd.Context())
		},
	)
}

func resolveDispatchOptions(
	flagLocal bool,
	flagSince string,
	flagUntil string,
	flagAllBranches bool,
	flagRepos []string,
	flagOrg string,
	flagVoice string,
	currentBranch func() (string, error),
) (dispatchpkg.Options, error) {
	if flagOrg != "" && len(flagRepos) > 0 {
		return dispatchpkg.Options{}, errors.New("--org and --repos are mutually exclusive")
	}
	if flagLocal && len(flagRepos) > 0 {
		return dispatchpkg.Options{}, errors.New("--repos cannot be used with --local")
	}
	if flagLocal && flagOrg != "" {
		return dispatchpkg.Options{}, errors.New("--org cannot be used with --local")
	}

	mode := dispatchpkg.ModeServer
	if flagLocal {
		mode = dispatchpkg.ModeLocal
	}

	var branches []string
	allBranches := flagAllBranches
	implicitCurrentBranch := false
	if !allBranches {
		if len(flagRepos) > 0 {
			branches = nil
		} else if strings.TrimSpace(flagOrg) != "" {
			branches = nil
		} else {
			currentBranchName, branchErr := currentBranch()
			if branchErr != nil {
				return dispatchpkg.Options{}, branchErr
			}
			branches = []string{currentBranchName}
			implicitCurrentBranch = true
		}
	}

	return dispatchpkg.Options{
		Mode:                  mode,
		RepoPaths:             append([]string(nil), flagRepos...),
		Org:                   flagOrg,
		Since:                 flagSince,
		Until:                 flagUntil,
		Branches:              branches,
		AllBranches:           allBranches,
		ImplicitCurrentBranch: implicitCurrentBranch,
		Generate:              true,
		Voice:                 flagVoice,
	}, nil
}
