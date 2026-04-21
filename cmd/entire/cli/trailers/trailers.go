// Package trailers provides parsing and formatting for Entire commit message trailers.
// Trailers are key-value metadata appended to git commit messages following the
// git trailer convention (key: value format after a blank line).
package trailers

import (
	"fmt"
	"regexp"
	"strings"

	checkpointID "github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
)

// Trailer key constants used in commit messages.
const (
	// MetadataTrailerKey points to the metadata directory within a commit tree.
	MetadataTrailerKey = "Entire-Metadata"

	// MetadataTaskTrailerKey points to the task metadata directory for subagent checkpoints.
	MetadataTaskTrailerKey = "Entire-Metadata-Task"

	// StrategyTrailerKey indicates which strategy created the commit.
	StrategyTrailerKey = "Entire-Strategy"

	// BaseCommitTrailerKey links shadow commits to their base code commit.
	BaseCommitTrailerKey = "Base-Commit"

	// SessionTrailerKey identifies which session created a commit.
	SessionTrailerKey = "Entire-Session"

	// CondensationTrailerKey identifies the condensation ID for a commit (legacy).
	CondensationTrailerKey = "Entire-Condensation"

	// SourceRefTrailerKey links code commits to their metadata on a shadow/metadata branch.
	// Format: "<branch>@<commit-hash>" e.g. "entire/metadata@abc123def456"
	SourceRefTrailerKey = "Entire-Source-Ref"

	// CheckpointTrailerKey links commits to their checkpoint metadata on entire/checkpoints/v1.
	// Format: 12 hex characters e.g. "a3b2c4d5e6f7"
	// This trailer survives git amend and rebase operations.
	CheckpointTrailerKey = "Entire-Checkpoint"

	// EphemeralBranchTrailerKey identifies the shadow branch that a checkpoint originated from.
	// Used in manual-commit strategy checkpoint commits on entire/checkpoints/v1 branch.
	// Format: full branch name e.g. "entire/2b4c177"
	EphemeralBranchTrailerKey = "Ephemeral-branch"

	// AgentTrailerKey identifies the agent that created a checkpoint.
	// Format: human-readable agent name e.g. "Claude Code", "Cursor"
	AgentTrailerKey = "Entire-Agent"
)

// Review trailer keys — used on empty "Review" commits on the user's branch.
const (
	ReviewByTrailerKey         = "Entire-Review-By"
	ReviewAgentTrailerKey      = "Entire-Review-Agent"
	ReviewSkillsTrailerKey     = "Entire-Review-Skills"
	ReviewSessionTrailerKey    = "Entire-Review-Session"
	ReviewCheckpointTrailerKey = "Entire-Review-Checkpoint"
	ReviewedUpToTrailerKey     = "Entire-Reviewed-Up-To"
	ReviewStatusTrailerKey     = "Entire-Review-Status"
)

// ReviewStatus values. Strings, not enums, so unknown future values
// deserialize without breaking.
const (
	ReviewStatusClosed  = "closed"
	ReviewStatusClean   = "clean"
	ReviewStatusSkipped = "skipped"
)

// ReviewMetadata captures all review-trailer values for a single review commit.
type ReviewMetadata struct {
	By           string
	Agent        string
	Skills       []string
	Session      string
	Checkpoint   string
	ReviewedUpTo string
	Status       string
}

// Pre-compiled regexes for trailer parsing.
var (
	// Trailer parsing regexes.
	strategyTrailerRegex     = regexp.MustCompile(StrategyTrailerKey + `:\s*(.+)`)
	metadataTrailerRegex     = regexp.MustCompile(MetadataTrailerKey + `:\s*(.+)`)
	taskMetadataTrailerRegex = regexp.MustCompile(MetadataTaskTrailerKey + `:\s*(.+)`)
	baseCommitTrailerRegex   = regexp.MustCompile(BaseCommitTrailerKey + `:\s*([a-f0-9]{40})`)
	condensationTrailerRegex = regexp.MustCompile(CondensationTrailerKey + `:\s*(.+)`)
	sessionTrailerRegex      = regexp.MustCompile(SessionTrailerKey + `:\s*(.+)`)
	checkpointTrailerRegex   = regexp.MustCompile(CheckpointTrailerKey + `:\s*(` + checkpointID.Pattern + `)(?:\s|$)`)

	// Review trailer parsing regexes.
	reviewByTrailerRegex         = regexp.MustCompile(ReviewByTrailerKey + `:\s*(.+)`)
	reviewAgentTrailerRegex      = regexp.MustCompile(ReviewAgentTrailerKey + `:\s*(.+)`)
	reviewSkillsTrailerRegex     = regexp.MustCompile(ReviewSkillsTrailerKey + `:\s*(.+)`)
	reviewSessionTrailerRegex    = regexp.MustCompile(ReviewSessionTrailerKey + `:\s*(.+)`)
	reviewCheckpointTrailerRegex = regexp.MustCompile(ReviewCheckpointTrailerKey + `:\s*([a-f0-9]{12})`)
	reviewedUpToTrailerRegex     = regexp.MustCompile(ReviewedUpToTrailerKey + `:\s*([a-f0-9]{40})`)
	reviewStatusTrailerRegex     = regexp.MustCompile(ReviewStatusTrailerKey + `:\s*(.+)`)
)

// ParseStrategy extracts strategy from commit message.
// Returns the strategy name and true if found, empty string and false otherwise.
func ParseStrategy(commitMessage string) (string, bool) {
	matches := strategyTrailerRegex.FindStringSubmatch(commitMessage)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1]), true
	}
	return "", false
}

// ParseMetadata extracts metadata dir from commit message.
// Returns the metadata directory and true if found, empty string and false otherwise.
func ParseMetadata(commitMessage string) (string, bool) {
	matches := metadataTrailerRegex.FindStringSubmatch(commitMessage)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1]), true
	}
	return "", false
}

// ParseTaskMetadata extracts task metadata dir from commit message.
// Returns the task metadata directory and true if found, empty string and false otherwise.
func ParseTaskMetadata(commitMessage string) (string, bool) {
	matches := taskMetadataTrailerRegex.FindStringSubmatch(commitMessage)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1]), true
	}
	return "", false
}

// ParseBaseCommit extracts the base commit SHA from a commit message.
// Returns the full SHA and true if found, empty string and false otherwise.
func ParseBaseCommit(commitMessage string) (string, bool) {
	matches := baseCommitTrailerRegex.FindStringSubmatch(commitMessage)
	if len(matches) > 1 {
		return matches[1], true
	}
	return "", false
}

// ParseCondensation extracts the condensation ID from a commit message.
// Returns the condensation ID and true if found, empty string and false otherwise.
func ParseCondensation(commitMessage string) (string, bool) {
	matches := condensationTrailerRegex.FindStringSubmatch(commitMessage)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1]), true
	}
	return "", false
}

// ParseSession extracts the session ID from a commit message.
// Returns the session ID and true if found, empty string and false otherwise.
// Note: If multiple Entire-Session trailers exist, this returns only the first one.
// Use ParseAllSessions to get all session IDs.
func ParseSession(commitMessage string) (string, bool) {
	matches := sessionTrailerRegex.FindStringSubmatch(commitMessage)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1]), true
	}
	return "", false
}

// ParseCheckpoint extracts the checkpoint ID from a commit message.
// Returns the CheckpointID and true if found, empty ID and false otherwise.
func ParseCheckpoint(commitMessage string) (checkpointID.CheckpointID, bool) {
	matches := checkpointTrailerRegex.FindStringSubmatch(commitMessage)
	if len(matches) > 1 {
		idStr := strings.TrimSpace(matches[1])
		// Validate it's a proper checkpoint ID
		if cpID, err := checkpointID.NewCheckpointID(idStr); err == nil {
			return cpID, true
		}
	}
	return checkpointID.EmptyCheckpointID, false
}

// ParseAllCheckpoints extracts all checkpoint IDs from a commit message.
// Returns a slice of CheckpointIDs (may be empty if none found).
// Duplicate IDs are deduplicated while preserving order.
// This is useful for squash merge commits that contain multiple Entire-Checkpoint trailers.
func ParseAllCheckpoints(commitMessage string) []checkpointID.CheckpointID {
	matches := checkpointTrailerRegex.FindAllStringSubmatch(commitMessage, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	ids := make([]checkpointID.CheckpointID, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			idStr := strings.TrimSpace(match[1])
			if !seen[idStr] {
				if cpID, err := checkpointID.NewCheckpointID(idStr); err == nil {
					seen[idStr] = true
					ids = append(ids, cpID)
				}
			}
		}
	}
	return ids
}

// ParseAllSessions extracts all session IDs from a commit message.
// Returns a slice of session IDs (may be empty if none found).
// Duplicate session IDs are deduplicated while preserving order.
// This is useful for commits that may have multiple Entire-Session trailers.
func ParseAllSessions(commitMessage string) []string {
	matches := sessionTrailerRegex.FindAllStringSubmatch(commitMessage, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	sessionIDs := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			sessionID := strings.TrimSpace(match[1])
			if !seen[sessionID] {
				seen[sessionID] = true
				sessionIDs = append(sessionIDs, sessionID)
			}
		}
	}
	return sessionIDs
}

// FormatStrategy creates a commit message with just the strategy trailer.
func FormatStrategy(message, strategy string) string {
	return fmt.Sprintf("%s\n\n%s: %s\n", message, StrategyTrailerKey, strategy)
}

// FormatTaskMetadata creates a commit message with task metadata trailer.
func FormatTaskMetadata(message, taskMetadataDir string) string {
	return fmt.Sprintf("%s\n\n%s: %s\n", message, MetadataTaskTrailerKey, taskMetadataDir)
}

// FormatTaskMetadataWithStrategy creates a commit message with task metadata and strategy trailers.
func FormatTaskMetadataWithStrategy(message, taskMetadataDir, strategy string) string {
	return fmt.Sprintf("%s\n\n%s: %s\n%s: %s\n", message, MetadataTaskTrailerKey, taskMetadataDir, StrategyTrailerKey, strategy)
}

// FormatSourceRef creates a formatted source ref string for the trailer.
// Format: "<branch>@<commit-hash-prefix>" (hash truncated to ShortIDLength chars)
func FormatSourceRef(branch, commitHash string) string {
	shortHash := commitHash
	if len(shortHash) > checkpointID.ShortIDLength {
		shortHash = shortHash[:checkpointID.ShortIDLength]
	}
	return fmt.Sprintf("%s@%s", branch, shortHash)
}

// FormatMetadata creates a commit message with metadata trailer.
func FormatMetadata(message, metadataDir string) string {
	return fmt.Sprintf("%s\n\n%s: %s\n", message, MetadataTrailerKey, metadataDir)
}

// FormatMetadataWithStrategy creates a commit message with metadata and strategy trailers.
func FormatMetadataWithStrategy(message, metadataDir, strategy string) string {
	return fmt.Sprintf("%s\n\n%s: %s\n%s: %s\n", message, MetadataTrailerKey, metadataDir, StrategyTrailerKey, strategy)
}

// FormatShadowCommit creates a commit message for manual-commit strategy checkpoints.
// Includes Entire-Metadata, Entire-Session, and Entire-Strategy trailers.
func FormatShadowCommit(message, metadataDir, sessionID string) string {
	var sb strings.Builder
	sb.WriteString(message)
	sb.WriteString("\n\n")
	fmt.Fprintf(&sb, "%s: %s\n", MetadataTrailerKey, metadataDir)
	fmt.Fprintf(&sb, "%s: %s\n", SessionTrailerKey, sessionID)
	fmt.Fprintf(&sb, "%s: %s\n", StrategyTrailerKey, "manual-commit")
	return sb.String()
}

// FormatShadowTaskCommit creates a commit message for manual-commit task checkpoints.
// Includes Entire-Metadata-Task, Entire-Session, and Entire-Strategy trailers.
func FormatShadowTaskCommit(message, taskMetadataDir, sessionID string) string {
	var sb strings.Builder
	sb.WriteString(message)
	sb.WriteString("\n\n")
	fmt.Fprintf(&sb, "%s: %s\n", MetadataTaskTrailerKey, taskMetadataDir)
	fmt.Fprintf(&sb, "%s: %s\n", SessionTrailerKey, sessionID)
	fmt.Fprintf(&sb, "%s: %s\n", StrategyTrailerKey, "manual-commit")
	return sb.String()
}

// FormatCheckpoint creates a commit message with a checkpoint trailer.
// This links user commits to their checkpoint metadata on entire/checkpoints/v1 branch.
func FormatCheckpoint(message string, cpID checkpointID.CheckpointID) string {
	return fmt.Sprintf("%s\n\n%s: %s\n", message, CheckpointTrailerKey, cpID.String())
}

// trailerLineRe matches git trailer format: "Key-Name: value" (no spaces before colon).
var trailerLineRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*: `)

// IsTrailerLine reports whether a line matches git trailer format.
func IsTrailerLine(line string) bool {
	return trailerLineRe.MatchString(line)
}

// appendTrailerLine appends a single pre-formatted trailer line (e.g. "Key: value")
// to message in trailer-block-aware format. If the message already ends with a
// trailer paragraph the line is joined directly to it; otherwise a blank line is
// inserted first to start a new trailer block.
func appendTrailerLine(message, trailerLine string) string {
	trimmed := strings.TrimRight(message, "\n")

	lines := strings.Split(trimmed, "\n")
	i := len(lines) - 1
	for i >= 0 && strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
		i--
	}

	hasTrailerBlock := false
	if i >= 0 {
		last := strings.TrimSpace(lines[i])
		if last != "" && IsTrailerLine(last) {
			for i > 0 {
				i--
				above := strings.TrimSpace(lines[i])
				if strings.HasPrefix(above, "#") {
					continue
				}
				if above == "" {
					hasTrailerBlock = true
					break
				}
				if !IsTrailerLine(above) {
					break
				}
			}
		}
	}

	if hasTrailerBlock {
		return trimmed + "\n" + trailerLine + "\n"
	}
	return trimmed + "\n\n" + trailerLine + "\n"
}

// AppendCheckpointTrailer appends Entire-Checkpoint in trailer-aware format.
// If the message already ends with a trailer paragraph, append directly to it;
// otherwise add a blank line before starting a new trailer block.
func AppendCheckpointTrailer(message, checkpointID string) string {
	trailer := fmt.Sprintf("%s: %s", CheckpointTrailerKey, checkpointID)
	return appendTrailerLine(message, trailer)
}

// AppendReviewTrailers appends all Entire-Review-* trailers to a commit
// message in trailer-aware format (single blank line separating body from
// trailer block).
func AppendReviewTrailers(message string, md ReviewMetadata) string {
	trailers := []struct {
		key, val string
	}{
		{ReviewByTrailerKey, md.By},
		{ReviewAgentTrailerKey, md.Agent},
		{ReviewSkillsTrailerKey, strings.Join(md.Skills, ",")},
		{ReviewSessionTrailerKey, md.Session},
		{ReviewCheckpointTrailerKey, md.Checkpoint},
		{ReviewedUpToTrailerKey, md.ReviewedUpTo},
		{ReviewStatusTrailerKey, md.Status},
	}
	out := message
	for _, tr := range trailers {
		if tr.val == "" {
			continue
		}
		out = appendTrailerLine(out, fmt.Sprintf("%s: %s", tr.key, tr.val))
	}
	return out
}

// ParseReviewMetadata extracts review trailers from a commit message.
// Returns ok=false when no review trailers are present (i.e., not a review commit).
func ParseReviewMetadata(message string) (ReviewMetadata, bool) {
	var md ReviewMetadata
	found := false
	if m := reviewByTrailerRegex.FindStringSubmatch(message); m != nil {
		md.By = strings.TrimSpace(m[1])
		found = true
	}
	if m := reviewAgentTrailerRegex.FindStringSubmatch(message); m != nil {
		md.Agent = strings.TrimSpace(m[1])
		found = true
	}
	if m := reviewSkillsTrailerRegex.FindStringSubmatch(message); m != nil {
		raw := strings.TrimSpace(m[1])
		if raw != "" {
			md.Skills = strings.Split(raw, ",")
		}
		found = true
	}
	if m := reviewSessionTrailerRegex.FindStringSubmatch(message); m != nil {
		md.Session = strings.TrimSpace(m[1])
		found = true
	}
	if m := reviewCheckpointTrailerRegex.FindStringSubmatch(message); m != nil {
		md.Checkpoint = strings.TrimSpace(m[1])
		found = true
	}
	if m := reviewedUpToTrailerRegex.FindStringSubmatch(message); m != nil {
		md.ReviewedUpTo = strings.TrimSpace(m[1])
		found = true
	}
	if m := reviewStatusTrailerRegex.FindStringSubmatch(message); m != nil {
		md.Status = strings.TrimSpace(m[1])
		found = true
	}
	return md, found
}
