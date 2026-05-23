package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m *Model) applyMeetSpace(space api.MeetSpace) {
	replaced := false
	for i := range m.meetSpaces {
		if m.meetSpaces[i].Name == space.Name {
			m.meetSpaces[i] = space
			replaced = true
			break
		}
	}
	if !replaced {
		m.meetSpaces = append(m.meetSpaces, space)
	}
	m.meetSpaces = sortedMeetSpaces(m.meetSpaces)
}

func (m Model) createMeetSpaceNow() (Model, tea.Cmd) {
	m.toast = "creating meet space..."
	return m, func() tea.Msg {
		space, err := m.client.CreateMeetSpace(m.ctx, "")
		return meetActionMsg{space: space, err: err, label: "meet space created"}
	}
}

func (m Model) openMeetLink() (Model, tea.Cmd) {
	space := m.selectedMeet()
	joinURL := space.JoinURL()
	if joinURL == "" {
		m.toast = "no Meet URL available"
		return m, nil
	}
	return m, func() tea.Msg {
		err := openURL(joinURL)
		return meetActionMsg{space: space, err: err, label: "opening browser"}
	}
}

func (m Model) copyMeetLink() (Model, tea.Cmd) {
	space := m.selectedMeet()
	joinURL := space.JoinURL()
	if joinURL == "" {
		m.toast = "no Meet URL to copy"
		return m, nil
	}
	return m, func() tea.Msg {
		err := copyText(joinURL)
		return meetActionMsg{space: space, err: err, label: "link copied"}
	}
}

func (m Model) endSelectedMeet() (Model, tea.Cmd) {
	space := m.selectedMeet()
	spaceName := space.SpaceResourceName()
	if spaceName == "" {
		m.toast = "no Meet space to end"
		return m, nil
	}
	if !space.IsActive() {
		m.toast = "no active conference to end"
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.EndMeetSpace(m.ctx, spaceName)
		ended := space
		ended.Active = false
		ended.ActiveConference = nil
		if ended.EndTime.IsZero() {
			ended.EndTime = time.Now()
		}
		return meetActionMsg{space: ended, err: err, label: "conference ended"}
	}
}

func (m *Model) handleMeetAction(msg meetActionMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		m.err = msg.err.Error()
	} else {
		m.toast = msg.label
		if msg.space.Name != "" {
			m.applyMeetSpace(msg.space)
			m.persistWorkspaceCache()
		}
	}

	return cmds
}
