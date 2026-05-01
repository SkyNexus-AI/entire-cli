// Package review contains the env-var contract between `entire review` (which
// spawns the agent process) and the lifecycle hook (which adopts the session).
// These names are stable API; renaming any constant is a breaking change.
//
// Design rationale: each spawned agent inherits its own copy of the process
// environment, so multi-tenant correctness (multiple worktrees, multi-agent
// runs) holds by construction — one agent's env vars cannot bleed into
// another agent's session. The lifecycle UserPromptSubmit hook reads these
// env vars to tag the in-flight session as a review session (Kind =
// "agent_review") and records which skills were run.
package review

import (
	"encoding/json"
	"fmt"
	"strings"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

const (
	// EnvSession is the review-session indicator. `entire review` sets this
	// to "1" on the spawned agent process; the lifecycle hook treats any
	// other value (including unset) as a normal coding session. Kept as a
	// sentinel string rather than a bool so future versions can carry
	// additional metadata in the value without breaking the contract.
	EnvSession = "ENTIRE_REVIEW_SESSION"

	// EnvAgent is the name of the agent spawned for the review (e.g.
	// "claude-code"). The lifecycle hook logs this for diagnostics; the
	// agent's identity is otherwise persisted via session state's existing
	// agent_type field set during normal turn-start handling, so adoption
	// itself does not need to copy this value into review-specific state.
	EnvAgent = "ENTIRE_REVIEW_AGENT"

	// EnvSkills is a JSON-encoded []string of skill invocations passed to the
	// agent verbatim (e.g. `["/pr-review-toolkit:review-pr","/test-auditor"]`).
	// Use EncodeSkills / DecodeSkills to round-trip the value safely.
	EnvSkills = "ENTIRE_REVIEW_SKILLS"

	// EnvPrompt is the full prompt text sent to the agent at review start. The
	// lifecycle hook stores this so the checkpoint records what the agent was
	// asked to review.
	EnvPrompt = "ENTIRE_REVIEW_PROMPT"

	// EnvStartingSHA is the git commit SHA that was HEAD when `entire review`
	// was invoked. Set on the spawned process so future consumers (e.g. the
	// re-run guard or `entire status`) can detect whether commits occurred
	// during the review without walking history. CU1's adoption path does
	// not currently consume it — session state's existing base_commit field
	// captures the same SHA via normal turn-start handling, so this env var
	// is reserved for future code that needs a separate review-spawn anchor
	// (e.g. comparing review-time HEAD to base_commit when sessions span
	// commits made outside the agent).
	EnvStartingSHA = "ENTIRE_REVIEW_STARTING_SHA"
)

// EncodeSkills serialises a slice of skill invocation strings to a JSON value
// suitable for storing in the ENTIRE_REVIEW_SKILLS environment variable.
// An empty or nil slice encodes to the literal string "[]".
func EncodeSkills(skills []string) (string, error) {
	if len(skills) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(skills)
	if err != nil {
		return "", fmt.Errorf("encode skills: %w", err)
	}
	return string(b), nil
}

// DecodeSkills deserialises a value previously produced by EncodeSkills.
// An empty string decodes to a nil slice (no skills configured).
// Any other value that is not valid JSON returns an error.
func DecodeSkills(encoded string) ([]string, error) {
	if encoded == "" {
		return nil, nil
	}
	var skills []string
	if err := json.Unmarshal([]byte(encoded), &skills); err != nil {
		return nil, fmt.Errorf("decode skills: %w", err)
	}
	return skills, nil
}

// AppendReviewEnv adds the ENTIRE_REVIEW_* env vars to base, returning
// the new slice. Used by per-agent reviewers in their AgentReviewer.Start
// implementations to propagate the review-session contract to spawned
// agent processes.
//
// agentName must be the agent's stable registry key (e.g. "claude-code").
// cfg carries skills and the starting SHA. prompt is the full composed
// prompt text (result of ComposeReviewPrompt).
//
// Any pre-existing ENTIRE_REVIEW_* entries in base are stripped before the
// new values are appended. This handles nested invocations (an `entire
// review` run spawning another agent that calls `entire review`) and stale
// inheritance from a parent shell — the most-recent values must win, with
// no chance of duplicate keys whose precedence is implementation-defined.
func AppendReviewEnv(base []string, agentName string, cfg reviewtypes.RunConfig, prompt string) []string {
	skillsJSON, _ := EncodeSkills(cfg.Skills) //nolint:errcheck // EncodeSkills only fails on json.Marshal([]string), which is infallible
	out := make([]string, 0, len(base)+5)
	for _, kv := range base {
		if isReviewEnvEntry(kv) {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		EnvSession+"=1",
		EnvAgent+"="+agentName,
		EnvSkills+"="+skillsJSON,
		EnvPrompt+"="+prompt,
		EnvStartingSHA+"="+cfg.StartingSHA,
	)
}

// isReviewEnvEntry reports whether kv is a "KEY=VALUE" entry whose key is
// one of the ENTIRE_REVIEW_* contract variables.
func isReviewEnvEntry(kv string) bool {
	for _, prefix := range []string{
		EnvSession + "=",
		EnvAgent + "=",
		EnvSkills + "=",
		EnvPrompt + "=",
		EnvStartingSHA + "=",
	} {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}
