package cli

import (
	"errors"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/spf13/cobra"
)

// newCheckpointGroupCmd builds the `entire checkpoint` parent command and
// registers list/show/rewind/search/diff as children.
func newCheckpointGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "checkpoint",
		Aliases: []string{"cp", "checkpoints"},
		Short:   "Inspect, rewind, search, and diff checkpoints",
		Long: `Operations on checkpoints — the persistent records of agent work tied to commits.

Commands:
  list     List checkpoints on the current branch
  show     Show details for a specific checkpoint or commit
  rewind   Browse and rewind to a checkpoint
  search   Search checkpoints (semantic + keyword)
  diff     Compare two checkpoints

Examples:
  entire checkpoint list
  entire checkpoint show <id|sha>
  entire checkpoint rewind --to <id>
  entire checkpoint search "fix login"
  entire checkpoint diff <a> <b>`,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := paths.WorktreeRoot(cmd.Context()); err != nil {
				return errors.New("not a git repository")
			}
			return nil
		},
	}

	cmd.AddCommand(newCheckpointListCmd())
	cmd.AddCommand(newCheckpointShowCmd())
	cmd.AddCommand(newRewindCmd())
	cmd.AddCommand(newSearchCmd())
	cmd.AddCommand(newCheckpointDiffCmd())

	return cmd
}

// newCheckpointListCmd wraps the existing branch-default list view.
func newCheckpointListCmd() *cobra.Command {
	var sessionFlag string
	var noPagerFlag bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List checkpoints on the current branch",
		Long: `List checkpoints on the current branch.

Optionally filter by session ID with --session.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if checkDisabledGuard(cmd.Context(), cmd.OutOrStdout()) {
				return nil
			}
			return runExplainBranchWithFilter(cmd.Context(), cmd.OutOrStdout(), noPagerFlag, sessionFlag)
		},
	}

	cmd.Flags().StringVar(&sessionFlag, "session", "", "Filter checkpoints by session ID (or prefix)")
	cmd.Flags().BoolVar(&noPagerFlag, "no-pager", false, "Disable pager output")
	return cmd
}

// newCheckpointShowCmd wraps explain's checkpoint detail view, accepting either
// a checkpoint ID or a commit SHA as positional argument (auto-detected).
func newCheckpointShowCmd() *cobra.Command {
	var noPagerFlag bool
	var shortFlag bool
	var fullFlag bool
	var rawTranscriptFlag bool
	var generateFlag bool
	var forceFlag bool
	var searchAllFlag bool

	cmd := &cobra.Command{
		Use:   "show <checkpoint-id | commit-sha>",
		Short: "Show details for a checkpoint or commit",
		Long: `Show details for a specific checkpoint or commit.

Auto-detects whether the argument is a checkpoint ID (12 hex) or a commit SHA.

Verbosity:
  Default     Detailed view (ID, session, tokens, intent, prompts, files)
  --short     Summary only
  --full      Parsed full transcript
  --raw       Raw transcript file (JSONL)

Summary generation:
  --generate  Generate an AI summary
  --force     Regenerate even if a summary exists (requires --generate)

Performance:
  --search-all  Remove branch/depth limits (may be slow)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if checkDisabledGuard(cmd.Context(), cmd.OutOrStdout()) {
				return nil
			}
			verbose := !shortFlag
			return runExplain(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				"", "", "", args[0], noPagerFlag, verbose, fullFlag, rawTranscriptFlag, generateFlag, forceFlag, searchAllFlag)
		},
	}

	cmd.Flags().BoolVar(&noPagerFlag, "no-pager", false, "Disable pager output")
	cmd.Flags().BoolVarP(&shortFlag, "short", "s", false, "Show summary only")
	cmd.Flags().BoolVar(&fullFlag, "full", false, "Show full parsed transcript")
	cmd.Flags().BoolVar(&rawTranscriptFlag, "raw", false, "Show raw transcript file (JSONL)")
	cmd.Flags().BoolVar(&generateFlag, "generate", false, "Generate an AI summary for the checkpoint")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Regenerate even if summary exists (requires --generate)")
	cmd.Flags().BoolVar(&searchAllFlag, "search-all", false, "Search all commits (may be slow)")

	cmd.MarkFlagsMutuallyExclusive("short", "full", "raw")
	cmd.MarkFlagsMutuallyExclusive("generate", "raw")
	return cmd
}
