package cli

// review_bridge.go wires cli-package implementations into the review
// subpackage's NewCommand Deps struct. Functions that need checkpoint
// access (headHasReviewCheckpoint) and per-agent reviewer constructors
// (launchableReviewerFor) live here to avoid the import cycle:
//   review → checkpoint → codex → review
//   review → claudecode/codex/geminicli → review

import (
	"github.com/spf13/cobra"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/claudecode"
	"github.com/entireio/cli/cmd/entire/cli/agent/codex"
	"github.com/entireio/cli/cmd/entire/cli/agent/geminicli"
	cliReview "github.com/entireio/cli/cmd/entire/cli/review"
	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

// buildReviewDeps builds the review.Deps struct used by review.NewCommand.
// attachCmd is the cobra.Command for `entire review attach`; pass nil in
// tests that don't need the subcommand.
func buildReviewDeps(attachCmd *cobra.Command) cliReview.Deps {
	return cliReview.Deps{
		GetAgentsWithHooksInstalled: GetAgentsWithHooksInstalled,
		NewSilentError: func(err error) error {
			return NewSilentError(err)
		},
		HeadHasReviewCheckpoint: headHasReviewCheckpoint,
		ReviewerFor:             launchableReviewerFor,
		PromptForAgentFn:        nil, // use real PromptForAgent
		AttachCmd:               attachCmd,
	}
}

// launchableReviewerFor returns the AgentReviewer for known launchable agents,
// or nil for non-launchable agents (cursor, opencode, factoryai-droid,
// copilot-cli). This lives in the cli package to avoid the import cycle:
//
//	review/cmd.go → claudecode/codex/geminicli → review
func launchableReviewerFor(agentName string) reviewtypes.AgentReviewer {
	switch agentName {
	case string(agent.AgentNameClaudeCode):
		return claudecode.NewReviewer()
	case string(agent.AgentNameCodex):
		return codex.NewReviewer()
	case string(agent.AgentNameGemini):
		return geminicli.NewReviewer()
	default:
		return nil
	}
}
