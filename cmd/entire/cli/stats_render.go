package cli

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

type statsStyles struct {
	colorEnabled bool
	width        int

	bold    lipgloss.Style
	dim     lipgloss.Style
	label   lipgloss.Style
	value   lipgloss.Style
	unit    lipgloss.Style
	desc    lipgloss.Style
	repoNm  lipgloss.Style
	commitH lipgloss.Style
	commitM lipgloss.Style
	add     lipgloss.Style
	del     lipgloss.Style
	muted   lipgloss.Style
}

// getFullTerminalWidth returns the terminal width without the 80-char cap
// used by other commands. Stats benefits from wide output for bar charts.
func getFullTerminalWidth(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 { //nolint:gosec // G115: uintptr->int is safe for fd
			return width
		}
	}
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		if f == nil {
			continue
		}
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 { //nolint:gosec // G115: uintptr->int is safe for fd
			return width
		}
	}
	return 80
}

func newStatsStyles(w io.Writer) statsStyles {
	useColor := shouldUseColor(w)
	width := getFullTerminalWidth(w)

	s := statsStyles{
		colorEnabled: useColor,
		width:        width,
	}

	if useColor {
		s.bold = lipgloss.NewStyle().Bold(true)
		s.dim = lipgloss.NewStyle().Faint(true)
		s.label = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Bold(true)
		s.value = lipgloss.NewStyle().Bold(true)
		s.unit = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		s.desc = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		s.repoNm = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
		s.commitH = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		s.commitM = lipgloss.NewStyle().Bold(true)
		s.add = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
		s.del = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
		s.muted = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	}

	return s
}

func (s statsStyles) render(style lipgloss.Style, text string) string {
	if !s.colorEnabled {
		return text
	}
	return style.Render(text)
}

func (s statsStyles) renderAgent(agentID, text string) string {
	if !s.colorEnabled {
		return text
	}
	display := agentDisplayMap[agentID]
	return lipgloss.NewStyle().Foreground(lipgloss.Color(display.Color)).Render(text)
}

type agentDisplay struct {
	Label string
	Color string // ANSI 256 color code
	Char  rune   // block character for bar charts
}

var agentDisplayMap = map[string]agentDisplay{
	"claude":   {Label: "Claude Code", Color: "208", Char: '█'},
	"gemini":   {Label: "Gemini", Color: "141", Char: '█'},
	"amp":      {Label: "Amp", Color: "219", Char: '█'},
	"codex":    {Label: "Codex", Color: "75", Char: '█'},
	"opencode": {Label: "OpenCode", Color: "37", Char: '█'},
	"copilot":  {Label: "Copilot", Color: "117", Char: '█'},
	"pi":       {Label: "Pi", Color: "204", Char: '█'},
	"cursor":   {Label: "Cursor", Color: "114", Char: '█'},
	"droid":    {Label: "Droid", Color: "215", Char: '█'},
	"kiro":     {Label: "Kiro", Color: "222", Char: '█'},
	"unknown":  {Label: "Unknown", Color: "245", Char: '░'},
}

var agentOrder = []string{
	"claude", "codex", "gemini", "amp", "opencode",
	"copilot", "pi", "cursor", "droid", "kiro", "unknown",
}

func renderStats(w io.Writer, sty statsStyles, stats contributionStats, repos []repoContribution, days []commitDay) {
	fmt.Fprintln(w)
	renderStatCards(w, sty, stats)
	fmt.Fprintln(w)
	renderAgentBreakdown(w, sty, repos)
	fmt.Fprintln(w)
	renderRepoChart(w, sty, repos)
	fmt.Fprintln(w)
	renderCommitList(w, sty, days)
}

func renderStatCards(w io.Writer, sty statsStyles, stats contributionStats) {
	cards := []struct {
		label string
		value string
		unit  string
		desc  string
	}{
		{"THROUGHPUT", fmt.Sprintf("%.1f", stats.Throughput), "k", "Avg. tokens/checkpoint"},
		{"ITERATION", fmt.Sprintf("%.1f", stats.Iteration), "x", "Avg sessions/checkpoint"},
		{"CONTINUITY", fmt.Sprintf("%.1f", stats.ContinuityH), "h", "Peak session length"},
		{"STREAK", strconv.Itoa(stats.Streak), " day", fmt.Sprintf("%d current", stats.CurrentStreak)},
	}

	cardWidth := (sty.width - 3) / 4 // 3 separators
	if cardWidth < 16 {
		cardWidth = 16
	}

	var topParts, midParts, botParts []string
	for _, c := range cards {
		lbl := padOrTruncate(c.label, cardWidth)
		dsc := padOrTruncate(c.desc, cardWidth)

		topParts = append(topParts, sty.render(sty.label, lbl))
		midParts = append(midParts, sty.render(sty.value, c.value)+sty.render(sty.unit, c.unit)+
			strings.Repeat(" ", max(0, cardWidth-len(c.value)-len(c.unit))))
		botParts = append(botParts, sty.render(sty.desc, dsc))
	}

	sep := sty.render(sty.dim, " │ ")
	fmt.Fprintln(w, strings.Join(topParts, sep))
	fmt.Fprintln(w, strings.Join(midParts, sep))
	fmt.Fprintln(w, strings.Join(botParts, sep))
}

func renderAgentBreakdown(w io.Writer, sty statsStyles, repos []repoContribution) {
	agentTotals := make(map[string]int)
	total := 0
	for _, r := range repos {
		total += r.Total
		for agent, count := range r.Agents {
			agentTotals[agent] += count
		}
	}

	if total == 0 {
		return
	}

	var parts []string
	for _, id := range agentOrder {
		count, ok := agentTotals[id]
		if !ok || count == 0 {
			continue
		}
		pct := float64(count) / float64(total) * 100
		display := agentDisplayMap[id]
		parts = append(parts, sty.renderAgent(id, fmt.Sprintf("● %s %d%%", display.Label, int(math.Round(pct)))))
	}

	fmt.Fprintf(w, "%s  %s\n",
		sty.render(sty.label, "CONTRIBUTIONS"),
		sty.render(sty.muted, fmt.Sprintf("%d checkpoints", total)))
	fmt.Fprintln(w, strings.Join(parts, sty.render(sty.dim, "  ")))
}

func renderRepoChart(w io.Writer, sty statsStyles, repos []repoContribution) {
	if len(repos) == 0 {
		return
	}

	fmt.Fprintln(w, sty.render(sty.label, "REPOSITORIES"))

	maxRepos := 5
	if len(repos) < maxRepos {
		maxRepos = len(repos)
	}
	display := repos[:maxRepos]

	maxCount := display[0].Total

	maxNameLen := 0
	for _, r := range display {
		if len(r.Repo) > maxNameLen {
			maxNameLen = len(r.Repo)
		}
	}
	if maxNameLen > 30 {
		maxNameLen = 30
	}

	countWidth := len(strconv.Itoa(maxCount))
	barWidth := sty.width - maxNameLen - countWidth - 4
	if barWidth < 10 {
		barWidth = 10
	}

	for _, r := range display {
		name := r.Repo
		if len(name) > maxNameLen {
			name = name[:maxNameLen-1] + "…"
		}
		name = fmt.Sprintf("%-*s", maxNameLen, name)

		bar := renderAgentBar(sty, r.Agents, maxCount, barWidth)
		count := fmt.Sprintf("%*d", countWidth, r.Total)

		fmt.Fprintf(w, "%s %s %s\n",
			sty.render(sty.repoNm, name),
			bar,
			sty.render(sty.muted, count))
	}
}

func renderAgentBar(sty statsStyles, agents map[string]int, maxCount, barWidth int) string {
	if maxCount == 0 {
		return strings.Repeat(" ", barWidth)
	}

	var b strings.Builder

	filled := 0
	for _, id := range agentOrder {
		count, ok := agents[id]
		if !ok || count == 0 {
			continue
		}
		segWidth := int(math.Round(float64(count) / float64(maxCount) * float64(barWidth)))
		if segWidth < 1 && count > 0 {
			segWidth = 1
		}
		if filled+segWidth > barWidth {
			segWidth = barWidth - filled
		}
		if segWidth <= 0 {
			continue
		}

		display := agentDisplayMap[id]
		seg := strings.Repeat(string(display.Char), segWidth)
		b.WriteString(sty.renderAgent(id, seg))
		filled += segWidth
	}

	if filled < barWidth {
		b.WriteString(sty.render(sty.dim, strings.Repeat("░", barWidth-filled)))
	}

	return b.String()
}

func renderCommitList(w io.Writer, sty statsStyles, days []commitDay) {
	renderCommitListN(w, sty, days, 3)
}

func renderCommitListN(w io.Writer, sty statsStyles, days []commitDay, maxDays int) {
	if len(days) == 0 {
		return
	}

	if maxDays <= 0 || maxDays > len(days) {
		maxDays = len(days)
	}

	for _, day := range days[:maxDays] {
		displayDate := formatCommitDate(day.Date)
		commitWord := "commits"
		if len(day.Commits) == 1 {
			commitWord = "commit"
		}

		fmt.Fprintf(w, "%s  %s\n",
			sty.render(sty.bold, displayDate),
			sty.render(sty.muted, fmt.Sprintf("%d %s", len(day.Commits), commitWord)))

		for _, c := range day.Commits {
			sha := c.CommitSHA
			if len(sha) > 7 {
				sha = sha[:7]
			}

			msg := "(no message)"
			if c.CommitMsg != nil && *c.CommitMsg != "" {
				msg = firstLine(*c.CommitMsg)
			}

			var badges []string
			for _, a := range uniqueCommitAgents(c) {
				display := agentDisplayMap[a]
				badges = append(badges, sty.renderAgent(a, display.Label))
			}

			fileStats := fmt.Sprintf("%d files", c.FilesChanged)
			if c.FilesChanged == 1 {
				fileStats = "1 file"
			}

			cpCount := len(c.Checkpoints)
			cpStr := ""
			if cpCount == 1 {
				cpStr = "1 checkpoint"
			} else if cpCount > 1 {
				cpStr = fmt.Sprintf("%d checkpoints", cpCount)
			}

			// Build right-aligned stats: +N / -N  M files  [K checkpoints]
			var statParts []string
			statParts = append(statParts,
				sty.render(sty.add, fmt.Sprintf("+%d", c.Additions))+
					sty.render(sty.muted, " / ")+
					sty.render(sty.del, fmt.Sprintf("-%d", c.Deletions)))
			statParts = append(statParts, sty.render(sty.muted, fileStats))
			if cpStr != "" {
				statParts = append(statParts, sty.render(sty.muted, cpStr))
			}
			rightSide := strings.Join(statParts, sty.render(sty.dim, "  "))
			// Plain length for padding (ANSI codes don't count)
			rightPlain := fmt.Sprintf("+%d / -%d  %s", c.Additions, c.Deletions, fileStats)
			if cpStr != "" {
				rightPlain += "  " + cpStr
			}

			// Build left side: hash  message  repo  [badges]
			left := sty.render(sty.commitH, sha) + " " +
				sty.render(sty.commitM, msg) + " " +
				sty.render(sty.muted, c.RepoFullName)
			leftPlain := sha + " " + msg + " " + c.RepoFullName
			var leftSb359 strings.Builder
			for _, badge := range badges {
				leftSb359.WriteString("  " + badge)
			}
			left += leftSb359.String()
			var leftPlainSb362 strings.Builder
			for _, a := range uniqueCommitAgents(c) {
				leftPlainSb362.WriteString("  " + agentDisplayMap[a].Label)
			}
			leftPlain += leftPlainSb362.String()

			// Truncate message if line would exceed width
			maxMsg := sty.width - (len(leftPlain) - len(msg)) - len(rightPlain) - 2
			if maxMsg < 10 {
				maxMsg = 10
			}
			if len(msg) > maxMsg {
				msg = msg[:maxMsg-3] + "..."
				// Rebuild left side with truncated message
				left = sty.render(sty.commitH, sha) + " " +
					sty.render(sty.commitM, msg) + " " +
					sty.render(sty.muted, c.RepoFullName)
				leftPlain = sha + " " + msg + " " + c.RepoFullName
				var leftSb378 strings.Builder
				for _, badge := range badges {
					leftSb378.WriteString("  " + badge)
				}
				left += leftSb378.String()
				var leftPlainSb381 strings.Builder
				for _, a := range uniqueCommitAgents(c) {
					leftPlainSb381.WriteString("  " + agentDisplayMap[a].Label)
				}
				leftPlain += leftPlainSb381.String()
			}

			gap := sty.width - len(leftPlain) - len(rightPlain)
			if gap < 2 {
				gap = 2
			}
			fmt.Fprintf(w, "%s%s%s\n", left, strings.Repeat(" ", gap), rightSide)
		}
		fmt.Fprintln(w)
	}
}

func uniqueCommitAgents(c userCommit) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, cp := range c.Checkpoints {
		agents := cp.Agents
		// Fall back to singular Agent field when Agents slice is empty
		if len(agents) == 0 && cp.Agent != "" {
			agents = []string{cp.Agent}
		}
		for _, a := range agents {
			id := normalizeAgentString(a)
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				result = append(result, id)
			}
		}
	}
	sort.Strings(result)
	return result
}

func formatCommitDate(dateStr string) string {
	t, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
	if err != nil {
		return dateStr
	}
	now := time.Now().Local()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	days := int(today.Sub(t).Hours() / 24)

	switch days {
	case 0:
		return t.Format("Monday 2 Jan") + " (today)"
	case 1:
		return t.Format("Monday 2 Jan") + " (yesterday)"
	default:
		return t.Format("Monday 2 Jan")
	}
}

func padOrTruncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) > width {
		return string(runes[:width-1]) + "…"
	}
	return s + strings.Repeat(" ", width-len(runes))
}
