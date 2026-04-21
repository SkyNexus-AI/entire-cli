package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

const (
	agentUnknown   = "unknown"
	dateUnknown    = "unknown"
	statsTimeframe = "last-month"
	statsLimit     = 1000
)

// knownAgents maps normalized agent strings from the API to display IDs.
// This is intentionally broader than agent.AgentType constants because
// the API returns agents that may not have CLI integrations (amp, pi, kiro).
var knownAgents = map[string]string{
	"claude":     "claude",
	"claudecode": "claude",
	"gemini":     "gemini",
	"geminicli":  "gemini",
	"amp":        "amp",
	"codex":      "codex",
	"opencode":   "opencode",
	"copilot":    "copilot",
	"copilotcli": "copilot",
	"pi":         "pi",
	"cursor":     "cursor",
	"droid":      "droid",
	"kiro":       "kiro",
}

func newStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show your activity overview",
		Long:  "Display checkpoint stats, repository breakdown, and recent commits from entire.io",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStats(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	return cmd
}

func runStats(ctx context.Context, w, errW io.Writer) error {
	client, err := NewAuthenticatedAPIClient(false)
	if err != nil {
		fmt.Fprintln(errW, "Not logged in. Run 'entire login' to authenticate.")
		return NewSilentError(err)
	}

	// Non-interactive fallback: piped output or accessibility mode
	if !isTerminalWriter(w) || IsAccessibleMode() {
		return runStatsStatic(ctx, w, client)
	}

	return runStatsTUI(ctx, client)
}

func runStatsStatic(ctx context.Context, w io.Writer, client *api.Client) error {
	var checkpoints []userCheckpoint
	var streakDates []string
	var commits []userCommit

	g, gCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var fetchErr error
		checkpoints, streakDates, fetchErr = fetchCheckpoints(gCtx, client)
		return fetchErr
	})
	g.Go(func() error {
		var fetchErr error
		commits, fetchErr = fetchCommits(gCtx, client)
		return fetchErr
	})
	if err := g.Wait(); err != nil {
		return fmt.Errorf("fetch stats: %w", err)
	}

	stats := computeContributionStats(checkpoints, streakDates)
	repos := computeRepoContributions(checkpoints)
	hourly := computeHourlyData(checkpoints)
	days := groupCommitsByDay(commits)

	sty := newStatsStyles(w)
	renderStats(w, sty, stats, repos, hourly, days)
	return nil
}

func fetchCheckpoints(ctx context.Context, client *api.Client) ([]userCheckpoint, []string, error) {
	path := fmt.Sprintf("/api/v1/stats/checkpoints?timeframe=%s&limit=%d", statsTimeframe, statsLimit)
	resp, err := client.Get(ctx, path)
	if err != nil {
		return nil, nil, fmt.Errorf("GET checkpoints: %w", err)
	}
	defer resp.Body.Close()

	if err := api.CheckResponse(resp); err != nil {
		return nil, nil, fmt.Errorf("checkpoints response: %w", err)
	}

	var result userCheckpointsResponse
	if err := api.DecodeJSON(resp, &result); err != nil {
		return nil, nil, fmt.Errorf("decode checkpoints: %w", err)
	}
	return result.Checkpoints, result.StreakDates, nil
}

func fetchCommits(ctx context.Context, client *api.Client) ([]userCommit, error) {
	path := fmt.Sprintf("/api/v1/stats/commits?timeframe=%s&limit=%d", statsTimeframe, statsLimit)
	resp, err := client.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("GET commits: %w", err)
	}
	defer resp.Body.Close()

	if err := api.CheckResponse(resp); err != nil {
		return nil, fmt.Errorf("commits response: %w", err)
	}

	var result userCommitsResponse
	if err := api.DecodeJSON(resp, &result); err != nil {
		return nil, fmt.Errorf("decode commits: %w", err)
	}
	return result.Commits, nil
}

// computeContributionStats mirrors the frontend's computeStats + computeStreaks.
func computeContributionStats(checkpoints []userCheckpoint, streakDates []string) contributionStats {
	stats := contributionStats{}
	if len(checkpoints) == 0 {
		return stats
	}

	stats.Tasks = len(checkpoints)

	var tokenSum, tokenCount, sessionSum, maxSteps int
	for _, cp := range checkpoints {
		if cp.InputTokens != nil || cp.OutputTokens != nil {
			tokenSum += derefOr(cp.InputTokens, 0) + derefOr(cp.OutputTokens, 0)
			tokenCount++
		}
		sessionSum += derefOr(cp.SessionCount, 1)
		if s := derefOr(cp.Steps, 1); s > maxSteps {
			maxSteps = s
		}
	}

	if tokenCount > 0 {
		stats.Throughput = float64(tokenSum) / float64(tokenCount) / 1000.0
	}
	stats.Iteration = float64(sessionSum) / float64(len(checkpoints))
	// 2 min/step is the frontend's heuristic for turn duration
	stats.ContinuityH = float64(maxSteps) * 2.0 / 60.0
	stats.Streak, stats.CurrentStreak = computeStreaks(streakDates)

	return stats
}

func computeStreaks(timestamps []string) (longest, current int) {
	if len(timestamps) == 0 {
		return 0, 0
	}

	dateSet := make(map[string]struct{})
	for _, ts := range timestamps {
		t, err := parseFlexibleTime(ts)
		if err != nil {
			continue
		}
		dateSet[t.Local().Format("2006-01-02")] = struct{}{}
	}

	if len(dateSet) == 0 {
		return 0, 0
	}

	// Current streak: from today or yesterday backward
	today := time.Now().Local().Format("2006-01-02")
	check := time.Now().Local()
	if _, ok := dateSet[today]; !ok {
		check = check.AddDate(0, 0, -1)
	}
	for {
		if _, ok := dateSet[check.Format("2006-01-02")]; !ok {
			break
		}
		current++
		check = check.AddDate(0, 0, -1)
	}

	dates := make([]string, 0, len(dateSet))
	for d := range dateSet {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	run := 1
	longest = 1
	for i := 1; i < len(dates); i++ {
		prev, errP := time.Parse("2006-01-02", dates[i-1])
		curr, errC := time.Parse("2006-01-02", dates[i])
		if errP != nil || errC != nil {
			run = 1
			continue
		}
		if curr.Sub(prev).Hours() <= 25 { // 1 day with some tolerance
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
	}

	return longest, current
}

func computeRepoContributions(checkpoints []userCheckpoint) []repoContribution {
	byRepo := make(map[string]*repoContribution)

	for _, cp := range checkpoints {
		rc, ok := byRepo[cp.RepoFullName]
		if !ok {
			rc = &repoContribution{
				Repo:   cp.RepoFullName,
				Agents: make(map[string]int),
			}
			byRepo[cp.RepoFullName] = rc
		}
		rc.Total++
		rc.Agents[normalizeAgentID(cp.Agent)]++
	}

	result := make([]repoContribution, 0, len(byRepo))
	for _, rc := range byRepo {
		result = append(result, *rc)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Total != result[j].Total {
			return result[i].Total > result[j].Total
		}
		return result[i].Repo < result[j].Repo
	})
	return result
}

// computeHourlyData groups checkpoints by date+hour+agent, summing steps.
func computeHourlyData(checkpoints []userCheckpoint) []hourlyPoint {
	type key struct {
		date    string
		hour    int
		agentID string
	}
	grouped := make(map[key]int)

	for _, cp := range checkpoints {
		t, err := parseFlexibleTime(cp.CommitDate)
		if err != nil {
			continue
		}
		local := t.Local()
		k := key{
			date:    local.Format("2006-01-02"),
			hour:    local.Hour(),
			agentID: normalizeAgentID(cp.Agent),
		}
		grouped[k] += derefOr(cp.Steps, 1)
	}

	result := make([]hourlyPoint, 0, len(grouped))
	for k, v := range grouped {
		result = append(result, hourlyPoint{
			Date:    k.date,
			Hour:    k.hour,
			Value:   v,
			AgentID: k.agentID,
		})
	}
	return result
}

func groupCommitsByDay(commits []userCommit) []commitDay {
	byDate := make(map[string][]userCommit)
	var dateOrder []string

	for _, c := range commits {
		date := dateUnknown
		if c.CommitDate != nil {
			if t, err := parseFlexibleTime(*c.CommitDate); err == nil {
				date = t.Local().Format("2006-01-02")
			}
		}
		if _, exists := byDate[date]; !exists {
			dateOrder = append(dateOrder, date)
		}
		byDate[date] = append(byDate[date], c)
	}

	// Sort dates newest first, with unknown dates pushed to the end
	sort.Slice(dateOrder, func(i, j int) bool {
		if dateOrder[i] == dateUnknown {
			return false
		}
		if dateOrder[j] == dateUnknown {
			return true
		}
		return dateOrder[i] > dateOrder[j]
	})

	result := make([]commitDay, 0, len(dateOrder))
	for _, d := range dateOrder {
		result = append(result, commitDay{Date: d, Commits: byDate[d]})
	}
	return result
}

func normalizeAgentID(agent *string) string {
	if agent == nil {
		return agentUnknown
	}
	return normalizeAgentString(*agent)
}

func normalizeAgentString(s string) string {
	if s == "" {
		return agentUnknown
	}

	var sb strings.Builder
	for _, r := range s {
		if r == ' ' || r == '-' || r == '_' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			sb.WriteByte(byte(r + 32))
		} else {
			sb.WriteRune(r)
		}
	}
	lower := sb.String()

	if id, ok := knownAgents[lower]; ok {
		return id
	}

	for _, suffix := range []string{"code", "cli"} {
		if len(lower) > len(suffix) && lower[len(lower)-len(suffix):] == suffix {
			if id, ok := knownAgents[lower[:len(lower)-len(suffix)]]; ok {
				return id
			}
		}
	}

	if strings.HasPrefix(lower, "factoryaidroid") {
		return "droid"
	}

	return agentUnknown
}

// parseFlexibleTime tries RFC3339, then RFC3339Nano.
func parseFlexibleTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse time %q: %w", s, err)
		}
	}
	return t, nil
}

func derefOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}
