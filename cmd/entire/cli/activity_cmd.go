package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

const (
	agentUnknown      = "unknown"
	dateUnknown       = "unknown"
	activityTimeframe = "last-month"
	activityLimit     = 1000
)

// knownAgents maps normalized agent strings from the API to display IDs.
// Used for the commit list, where per-checkpoint agent strings are free-form.
// The /me/activity endpoint returns already-normalized canonical IDs.
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

func newActivityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show your activity overview",
		Long:  "Display your activity overview, repository breakdown, and recent commits from entire.io",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runActivity(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	return cmd
}

func runActivity(ctx context.Context, w, errW io.Writer) error {
	client, err := NewAuthenticatedAPIClient(false)
	if err != nil {
		fmt.Fprintln(errW, "Not logged in. Run 'entire login' to authenticate.")
		return NewSilentError(err)
	}

	// Non-interactive fallback: piped output or accessibility mode
	if !isTerminalWriter(w) || IsAccessibleMode() {
		return runActivityStatic(ctx, w, client)
	}

	return runActivityTUI(ctx, client)
}

func runActivityStatic(ctx context.Context, w io.Writer, client *api.Client) error {
	activity, commits, err := fetchActivityData(ctx, client)
	if err != nil {
		return err
	}

	stats := contributionStats{
		Tasks:         activity.Stats.Tasks,
		Throughput:    activity.Stats.Throughput,
		Iteration:     activity.Stats.Iteration,
		Orchestration: activity.Stats.Orchestration,
		Streak:        activity.Stats.Streak,
		CurrentStreak: activity.Stats.CurrentStreak,
	}
	days := groupCommitsByDay(commits)

	sty := newActivityStyles(w)
	renderActivity(w, sty, stats, activity.Repos, activity.HourlyContributions, days)
	return nil
}

// fetchActivityData fetches aggregated activity and commits concurrently.
func fetchActivityData(ctx context.Context, client *api.Client) (*userActivityResponse, []userCommit, error) {
	var activity *userActivityResponse
	var commits []userCommit

	g, gCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		activity, err = fetchActivity(gCtx, client)
		return err
	})
	g.Go(func() error {
		var err error
		commits, err = fetchCommits(gCtx, client)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, nil, fmt.Errorf("fetch activity: %w", err)
	}
	return activity, commits, nil
}

func fetchActivity(ctx context.Context, client *api.Client) (*userActivityResponse, error) {
	q := url.Values{}
	q.Set("timezone", detectTimezone())
	q.Set("timeframe", activityTimeframe)
	q.Set("limit", strconv.Itoa(activityLimit))
	path := "/api/v1/me/activity?" + q.Encode()

	resp, err := client.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("GET activity: %w", err)
	}
	defer resp.Body.Close()

	if err := api.CheckResponse(resp); err != nil {
		return nil, fmt.Errorf("activity response: %w", err)
	}

	var result userActivityResponse
	if err := api.DecodeJSON(resp, &result); err != nil {
		return nil, fmt.Errorf("decode activity: %w", err)
	}
	return &result, nil
}

func fetchCommits(ctx context.Context, client *api.Client) ([]userCommit, error) {
	path := fmt.Sprintf("/api/v1/me/commits?timeframe=%s&limit=%d", activityTimeframe, activityLimit)
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

// detectTimezone returns an IANA timezone identifier for the current host.
// Tries $TZ first, then the /etc/localtime symlink (Unix), falling back to UTC.
func detectTimezone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		const marker = "/zoneinfo/"
		if idx := strings.LastIndex(link, marker); idx >= 0 {
			return link[idx+len(marker):]
		}
	}
	if name := time.Local.String(); name != "" && name != "Local" {
		return name
	}
	return "UTC"
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
