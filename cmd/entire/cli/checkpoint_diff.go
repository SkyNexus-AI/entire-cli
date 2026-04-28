package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/spf13/cobra"
)

func newCheckpointDiffCmd() *cobra.Command {
	var jsonFlag bool

	cmd := &cobra.Command{
		Use:   "diff <checkpoint-a> <checkpoint-b>",
		Short: "Compare two checkpoints",
		Long: `Compare two checkpoints by ID and report the delta in files touched,
token usage, and session counts.

Both arguments must be full 12-character hex checkpoint IDs.

Examples:
  entire checkpoint diff a3b2c4d5e6f7 b1c2d3e4f5a6
  entire checkpoint diff a3b2c4d5e6f7 b1c2d3e4f5a6 --json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			aID, err := id.NewCheckpointID(args[0])
			if err != nil {
				return fmt.Errorf("invalid checkpoint A: %w", err)
			}
			bID, err := id.NewCheckpointID(args[1])
			if err != nil {
				return fmt.Errorf("invalid checkpoint B: %w", err)
			}

			repo, err := strategy.OpenRepository(ctx)
			if err != nil {
				return fmt.Errorf("open repository: %w", err)
			}
			store := checkpoint.NewGitStore(repo)

			a, err := store.ReadCommitted(ctx, aID)
			if err != nil {
				return fmt.Errorf("read checkpoint %s: %w", aID, err)
			}
			b, err := store.ReadCommitted(ctx, bID)
			if err != nil {
				return fmt.Errorf("read checkpoint %s: %w", bID, err)
			}

			d := computeCheckpointDiff(a, b)
			if jsonFlag {
				return writeCheckpointDiffJSON(cmd.OutOrStdout(), d)
			}
			return writeCheckpointDiffText(cmd.OutOrStdout(), d)
		},
	}

	cmd.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON")
	return cmd
}

type checkpointDiff struct {
	A             *checkpoint.CheckpointSummary `json:"-"`
	B             *checkpoint.CheckpointSummary `json:"-"`
	AID           string                        `json:"a"`
	BID           string                        `json:"b"`
	ASessions     int                           `json:"a_sessions"`
	BSessions     int                           `json:"b_sessions"`
	SessionsDelta int                           `json:"sessions_delta"`
	FilesAdded    []string                      `json:"files_added"`
	FilesRemoved  []string                      `json:"files_removed"`
	TokensDelta   tokenDelta                    `json:"tokens_delta"`
}

type tokenDelta struct {
	Total      int `json:"total"`
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
}

func computeCheckpointDiff(a, b *checkpoint.CheckpointSummary) *checkpointDiff {
	out := &checkpointDiff{
		A:             a,
		B:             b,
		AID:           a.CheckpointID.String(),
		BID:           b.CheckpointID.String(),
		ASessions:     a.CheckpointsCount,
		BSessions:     b.CheckpointsCount,
		SessionsDelta: b.CheckpointsCount - a.CheckpointsCount,
	}

	aFiles := make(map[string]struct{}, len(a.FilesTouched))
	for _, f := range a.FilesTouched {
		aFiles[f] = struct{}{}
	}
	bFiles := make(map[string]struct{}, len(b.FilesTouched))
	for _, f := range b.FilesTouched {
		bFiles[f] = struct{}{}
	}
	for f := range bFiles {
		if _, ok := aFiles[f]; !ok {
			out.FilesAdded = append(out.FilesAdded, f)
		}
	}
	for f := range aFiles {
		if _, ok := bFiles[f]; !ok {
			out.FilesRemoved = append(out.FilesRemoved, f)
		}
	}
	sort.Strings(out.FilesAdded)
	sort.Strings(out.FilesRemoved)

	if a.TokenUsage != nil || b.TokenUsage != nil {
		var aIn, aOut, aCR, aCW int
		var bIn, bOut, bCR, bCW int
		if a.TokenUsage != nil {
			aIn = a.TokenUsage.InputTokens
			aOut = a.TokenUsage.OutputTokens
			aCR = a.TokenUsage.CacheReadTokens
			aCW = a.TokenUsage.CacheCreationTokens
		}
		if b.TokenUsage != nil {
			bIn = b.TokenUsage.InputTokens
			bOut = b.TokenUsage.OutputTokens
			bCR = b.TokenUsage.CacheReadTokens
			bCW = b.TokenUsage.CacheCreationTokens
		}
		out.TokensDelta = tokenDelta{
			Total:      (bIn + bOut + bCR + bCW) - (aIn + aOut + aCR + aCW),
			Input:      bIn - aIn,
			Output:     bOut - aOut,
			CacheRead:  bCR - aCR,
			CacheWrite: bCW - aCW,
		}
	}

	return out
}

func writeCheckpointDiffText(w io.Writer, d *checkpointDiff) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Diff: %s → %s\n", d.AID, d.BID)
	fmt.Fprintf(&sb, "\nSessions:        %+d (%d → %d)\n", d.SessionsDelta, d.ASessions, d.BSessions)

	sb.WriteString("\nTokens:\n")
	fmt.Fprintf(&sb, "  total:        %+d\n", d.TokensDelta.Total)
	fmt.Fprintf(&sb, "  input:        %+d\n", d.TokensDelta.Input)
	fmt.Fprintf(&sb, "  output:       %+d\n", d.TokensDelta.Output)
	fmt.Fprintf(&sb, "  cache_read:   %+d\n", d.TokensDelta.CacheRead)
	fmt.Fprintf(&sb, "  cache_write:  %+d\n", d.TokensDelta.CacheWrite)

	if len(d.FilesAdded) > 0 {
		fmt.Fprintf(&sb, "\nFiles added (%d):\n", len(d.FilesAdded))
		for _, f := range d.FilesAdded {
			sb.WriteString("  + " + f + "\n")
		}
	}
	if len(d.FilesRemoved) > 0 {
		fmt.Fprintf(&sb, "\nFiles removed (%d):\n", len(d.FilesRemoved))
		for _, f := range d.FilesRemoved {
			sb.WriteString("  - " + f + "\n")
		}
	}
	if len(d.FilesAdded) == 0 && len(d.FilesRemoved) == 0 {
		sb.WriteString("\nFiles touched: identical set.\n")
	}

	if _, err := io.WriteString(w, sb.String()); err != nil {
		return fmt.Errorf("write diff: %w", err)
	}
	return nil
}

func writeCheckpointDiffJSON(w io.Writer, d *checkpointDiff) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	return nil
}
