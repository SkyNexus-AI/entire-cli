package cli

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type statsModel struct {
	stats  contributionStats
	repos  []repoContribution
	hourly []hourlyPoint
	days   []commitDay

	viewport viewport.Model
	sty      statsStyles
	width    int
	height   int
	ready    bool
}

func runStatsTUI(stats contributionStats, repos []repoContribution, hourly []hourlyPoint, days []commitDay) error {
	m := statsModel{
		stats:  stats,
		repos:  repos,
		hourly: hourly,
		days:   days,
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("stats TUI: %w", err)
	}
	return nil
}

func (m statsModel) Init() tea.Cmd {
	return nil
}

func (m statsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) { //nolint:ireturn // bubbletea interface
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyEscape || msg.Type == tea.KeyCtrlC || msg.String() == "q" {
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.sty = newStatsStylesWithWidth(m.width)

		headerHeight := m.headerLineCount()
		vpHeight := m.height - headerHeight - 1 // 1 for footer
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
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m statsModel) View() string {
	if !m.ready {
		return "Loading..."
	}

	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	b.WriteString(m.renderFooter())
	return b.String()
}

func (m statsModel) renderHeader() string {
	var buf bytes.Buffer
	buf.WriteString("\n")
	renderStatCards(&buf, m.sty, m.stats)
	buf.WriteString("\n")
	renderContributionChart(&buf, m.sty, m.hourly, m.repos)
	buf.WriteString("\n")
	renderRepoChart(&buf, m.sty, m.repos)
	buf.WriteString("\n")
	return buf.String()
}

func (m statsModel) headerLineCount() int {
	header := m.renderHeader()
	return strings.Count(header, "\n")
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
	s := statsStyles{
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
	return s
}

func padLeft(n int) string {
	s := strings.Builder{}
	if n < 10 {
		s.WriteString("  ")
	} else if n < 100 {
		s.WriteString(" ")
	}
	s.WriteString(strings.TrimSpace(strings.Repeat(" ", 0)))
	fmt.Fprintf(&s, "%d", n)
	return s.String()
}
