package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/api"
	"golang.org/x/sync/errgroup"
)

// statsDataMsg is sent when API data has been fetched.
type statsDataMsg struct {
	stats  contributionStats
	repos  []repoContribution
	hourly []hourlyPoint
	days   []commitDay
}

// statsErrMsg is sent when fetching fails.
type statsErrMsg struct{ err error }

type statsModel struct {
	// Data (nil until loaded)
	stats  *contributionStats
	repos  []repoContribution
	hourly []hourlyPoint
	days   []commitDay

	// Loading state
	loading bool
	loadErr error
	spinner spinner.Model

	// Fetch context
	ctx    context.Context
	client *api.Client

	// View state
	viewport viewport.Model
	sty      statsStyles
	width    int
	height   int
	ready    bool
}

func runStatsTUI(ctx context.Context, client *api.Client) error {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	m := statsModel{
		loading: true,
		spinner: sp,
		ctx:     ctx,
		client:  client,
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("stats TUI: %w", err)
	}
	return nil
}

func (m statsModel) fetchData() tea.Msg { //nolint:ireturn // bubbletea Cmd signature requires tea.Msg return
	var checkpoints []userCheckpoint
	var streakDates []string
	var commits []userCommit

	g, gCtx := errgroup.WithContext(m.ctx)
	g.Go(func() error {
		var err error
		checkpoints, streakDates, err = fetchCheckpoints(gCtx, m.client)
		return err
	})
	g.Go(func() error {
		var err error
		commits, err = fetchCommits(gCtx, m.client)
		return err
	})
	if err := g.Wait(); err != nil {
		return statsErrMsg{err: err}
	}

	stats := computeContributionStats(checkpoints, streakDates)
	repos := computeRepoContributions(checkpoints)
	hourly := computeHourlyData(checkpoints)
	days := groupCommitsByDay(commits)

	return statsDataMsg{
		stats:  stats,
		repos:  repos,
		hourly: hourly,
		days:   days,
	}
}

func (m statsModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.fetchData)
}

func (m statsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) { //nolint:ireturn // bubbletea interface
	switch msg := msg.(type) {
	case statsDataMsg:
		m.loading = false
		m.stats = &msg.stats
		m.repos = msg.repos
		m.hourly = msg.hourly
		m.days = msg.days
		if m.width > 0 {
			m = m.withViewport()
		}
		return m, nil

	case statsErrMsg:
		m.loading = false
		m.loadErr = msg.err
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyEscape || msg.Type == tea.KeyCtrlC || msg.String() == "q" {
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.sty = newStatsStylesWithWidth(m.width)
		if m.stats != nil {
			m = m.withViewport()
		}
		return m, nil

	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	}

	if m.ready {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m statsModel) withViewport() statsModel {
	headerHeight := m.headerLineCount()
	vpHeight := m.height - headerHeight - 1
	if vpHeight < 1 {
		vpHeight = 1
	}

	if !m.ready {
		m.viewport = viewport.New(m.width, vpHeight)
		m.ready = true
	} else {
		m.viewport.Width = m.width
		m.viewport.Height = vpHeight
	}
	m.viewport.SetContent(m.renderCommits())
	return m
}

func (m statsModel) View() string {
	if m.loadErr != nil {
		return fmt.Sprintf("\n  Failed to load stats: %s\n\n  Press q to quit.\n", m.loadErr)
	}

	if m.loading {
		return fmt.Sprintf("\n  %s Loading stats...\n", m.spinner.View())
	}

	if !m.ready {
		return ""
	}

	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	b.WriteString(m.renderFooter())
	return b.String()
}

func (m statsModel) renderHeader() string {
	if m.stats == nil {
		return ""
	}
	var buf bytes.Buffer
	buf.WriteString("\n")
	renderStatCards(&buf, m.sty, *m.stats)
	buf.WriteString("\n")
	renderContributionChart(&buf, m.sty, m.hourly, m.repos)
	buf.WriteString("\n")
	renderRepoChart(&buf, m.sty, m.repos)
	buf.WriteString("\n")
	return buf.String()
}

func (m statsModel) headerLineCount() int {
	return strings.Count(m.renderHeader(), "\n")
}

func (m statsModel) renderCommits() string {
	var buf bytes.Buffer
	renderCommitListN(&buf, m.sty, m.days, -1)
	return buf.String()
}

func (m statsModel) renderFooter() string {
	if !m.sty.colorEnabled {
		return ""
	}
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
	sep := helpStyle.Render(" · ")

	scrollPct := ""
	if m.viewport.TotalLineCount() > m.viewport.Height {
		pct := int(m.viewport.ScrollPercent() * 100)
		scrollPct = sep + helpStyle.Render(strings.Repeat(" ", max(0, m.width-40))+padLeft(pct)+"%")
	}

	return keyStyle.Render("↑↓") + helpStyle.Render(" scroll") +
		sep + keyStyle.Render("q") + helpStyle.Render(" quit") +
		scrollPct
}

func newStatsStylesWithWidth(width int) statsStyles {
	return statsStyles{
		colorEnabled: true,
		width:        width,
		bold:         lipgloss.NewStyle().Bold(true),
		dim:          lipgloss.NewStyle().Faint(true),
		label:        lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Bold(true),
		value:        lipgloss.NewStyle().Bold(true),
		unit:         lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		desc:         lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		repoNm:       lipgloss.NewStyle().Foreground(lipgloss.Color("7")),
		commitH:      lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		commitM:      lipgloss.NewStyle().Bold(true),
		add:          lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		del:          lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		muted:        lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
	}
}

func padLeft(n int) string {
	s := strings.Builder{}
	if n < 10 {
		s.WriteString("  ")
	} else if n < 100 {
		s.WriteString(" ")
	}
	fmt.Fprintf(&s, "%d", n)
	return s.String()
}
