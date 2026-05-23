package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) loadMeetSectionCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		meet, err := m.client.MeetSpaces(ctx)
		return featureRefreshedMsg{feature: FeatureMeet, meet: meet, startup: true, err: err}
	}
}
