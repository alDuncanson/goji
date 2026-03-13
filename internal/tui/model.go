package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"goji/internal/orchestrator"
)

type model struct {
	orch   *orchestrator.Orchestrator
	snap   orchestrator.Snapshot
	err    error
	width  int
	height int
}

type snapshotMsg struct {
	snap orchestrator.Snapshot
	err  error
}

func New(orch *orchestrator.Orchestrator) tea.Model {
	return model{orch: orch}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(fetchSnapshotCmd(m.orch), tickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.KeyMsg:
		switch typed.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			m.orch.RequestRefresh()
			return m, fetchSnapshotCmd(m.orch)
		}
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		return m, nil
	case snapshotMsg:
		m.snap = typed.snap
		m.err = typed.err
		return m, nil
	case time.Time:
		return m, tea.Batch(fetchSnapshotCmd(m.orch), tickCmd())
	}

	return m, nil
}

func (m model) View() string {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)

	if m.err != nil {
		return errorStyle.Render("snapshot unavailable: "+m.err.Error()) + "\n"
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render("goji — Symphony-style GitHub Orchestration"))
	b.WriteString("\n")
	b.WriteString(muted.Render("press r to refresh now • press q to quit"))
	b.WriteString("\n\n")

	b.WriteString(labelStyle.Render("Polling"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("checking=%t  next_poll=%s  interval=%dms\n",
		m.snap.Polling.Checking,
		durationFromMS(m.snap.Polling.NextPollInMS),
		m.snap.Polling.PollIntervalMS,
	))

	b.WriteString("\n")
	b.WriteString(labelStyle.Render("Totals"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("running=%d  retrying=%d  tokens(in/out/total)=%d/%d/%d  runtime=%ds\n",
		m.snap.Counts["running"],
		m.snap.Counts["retrying"],
		m.snap.Totals.InputTokens,
		m.snap.Totals.OutputTokens,
		m.snap.Totals.TotalTokens,
		m.snap.Totals.SecondsRunning,
	))

	b.WriteString("\n")
	b.WriteString(labelStyle.Render("Running Sessions"))
	b.WriteString("\n")
	if len(m.snap.Running) == 0 {
		b.WriteString(muted.Render("no active sessions"))
		b.WriteString("\n")
	} else {
		for _, row := range m.snap.Running {
			b.WriteString(fmt.Sprintf("%s  state=%s  turns=%d  retry=%d  age=%s  event=%s\n",
				padRight(row.Identifier, 10),
				padRight(row.State, 12),
				row.TurnCount,
				row.RetryAttempt,
				durationFromSec(row.RuntimeSeconds),
				truncate(row.LastEvent+" "+row.LastMessage, 80),
			))
		}
	}

	b.WriteString("\n")
	b.WriteString(labelStyle.Render("Retry Queue"))
	b.WriteString("\n")
	if len(m.snap.Retrying) == 0 {
		b.WriteString(muted.Render("no queued retries"))
		b.WriteString("\n")
	} else {
		for _, row := range m.snap.Retrying {
			b.WriteString(fmt.Sprintf("%s  attempt=%d  due=%s  err=%s\n",
				padRight(row.Identifier, 10),
				row.Attempt,
				durationFromMS(row.DueInMS),
				truncate(row.Error, 80),
			))
		}
	}

	b.WriteString("\n")
	b.WriteString(labelStyle.Render("Recent Events"))
	b.WriteString("\n")
	if len(m.snap.Events) == 0 {
		b.WriteString(muted.Render("no events yet"))
		b.WriteString("\n")
	} else {
		start := len(m.snap.Events) - 10
		if start < 0 {
			start = 0
		}
		for _, event := range m.snap.Events[start:] {
			b.WriteString(fmt.Sprintf("%s  %-5s  %-24s  %s\n",
				event.At.Format("15:04:05"),
				strings.ToUpper(event.Level),
				padRight(event.Type, 24),
				truncate(event.Message, 90),
			))
		}
	}

	return b.String()
}

func fetchSnapshotCmd(orch *orchestrator.Orchestrator) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		snap, err := orch.Snapshot(ctx)
		return snapshotMsg{snap: snap, err: err}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return t })
}

func durationFromMS(ms int) string {
	if ms < 0 {
		ms = 0
	}
	return (time.Duration(ms) * time.Millisecond).String()
}

func durationFromSec(sec int) string {
	if sec < 0 {
		sec = 0
	}
	return (time.Duration(sec) * time.Second).String()
}

func padRight(value string, width int) string {
	if len(value) >= width {
		return value[:width]
	}
	return value + strings.Repeat(" ", width-len(value))
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
