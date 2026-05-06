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
)

const (
	// EnvSession is the review-session indicator. `entire review` sets this
	// to "1" on the spawned agent process; the lifecycle hook treats any
	// other value (including unset) as a normal coding session. Kept as a
	// sentinel string rather than a bool so future versions can carry
	// additional metadata in the value without breaking the contract.
	EnvSession = "ENTIRE_REVIEW_SESSION"

	// EnvAgent is the name of the agent spawned for the review (e.g.
	// "claude-code"). The lifecycle hook requires this to match the hook's
	// agent before tagging the session, preventing stale exported review env
	// from tagging sessions for a different agent.
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
	// was invoked. The lifecycle hook requires this to match the session's
	// initial base_commit before tagging the session, so stale env from an old
	// HEAD does not mark a later normal session as a review.
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
