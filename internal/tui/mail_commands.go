package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) loadMailSectionCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		labels, labelsErr := m.client.MailLabels(ctx)
		threads, threadsErr := m.client.MailThreads(ctx, api.MailQuery{Label: "Inbox"})
		return featureRefreshedMsg{feature: FeatureMail, labels: labels, threads: threads, startup: true, err: firstErr(labelsErr, threadsErr)}
	}
}
