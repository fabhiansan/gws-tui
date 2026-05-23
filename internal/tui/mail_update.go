package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

// focusMailSidebar moves focus onto the folder rail, placing the cursor on the
// folder that is currently loaded.
func (m *Model) focusMailSidebar() {
	m.focusedPane = paneMailSidebar
	m.mailFolderCursor = m.mailFolderIndex(m.mailFolder)
}

func (m *Model) moveMailFolderCursor(delta int) {
	folders := m.mailFolderList()
	if len(folders) == 0 {
		m.mailFolderCursor = 0
		return
	}
	m.mailFolderCursor = clamp(m.mailFolderCursor+delta, len(folders))
}

// selectMailFolder loads the folder under the sidebar cursor and moves focus
// to the inbox list so the user can start browsing it immediately.
func (m Model) selectMailFolder() (Model, tea.Cmd) {
	folders := m.mailFolderList()
	if len(folders) == 0 {
		return m, nil
	}
	folder := folders[clamp(m.mailFolderCursor, len(folders))]
	m.mailFolder = folder.Name
	m.search = ""
	m.selected[FeatureMail] = 0
	m.focusedPane = paneList
	return m.loadMailFolder(folder)
}

// loadMailFolder fetches the thread list for a folder, replacing the inbox.
// A cached folder is shown instantly and skips the network, mirroring the way
// loadSelectedChat reuses ChatMessagesBySpace.
func (m Model) loadMailFolder(folder api.MailLabel) (Model, tea.Cmd) {
	if m.applyCachedSelectedMail() {
		m.loading = false
		m.clampSelections()
		m.persistWorkspaceCache()
		return m, tea.Batch(m.imageDownloadCmdsForMail(m.mailThreads)...)
	}
	m.loading = true
	m.mailThreads = nil
	m.mailNext = ""
	labels := m.mailLabels
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()

		page, err := m.client.MailThreads(ctx, mailQueryForFolder(folder, "", ""))
		return featureRefreshedMsg{
			feature: FeatureMail,
			labels:  labels,
			threads: page,
			err:     err,
		}
	}
}

func (m *Model) applyMailThread(thread api.MailThread) {
	if thread.ID == "" {
		return
	}
	m.rememberMailThread(thread)
	for i := range m.mailThreads {
		if m.mailThreads[i].ID == thread.ID {
			m.mailThreads[i] = thread
			return
		}
	}
	m.mailThreads = append([]api.MailThread{thread}, m.mailThreads...)
}

func (m Model) toggleSelectedStar() (Model, tea.Cmd) {
	thread := m.selectedMail()
	if thread.ID == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		updated, err := m.client.ToggleStar(m.ctx, thread.ID)
		return mailActionMsg{thread: updated, err: err, label: "star toggled"}
	}
}

func (m Model) toggleSelectedUnread() (Model, tea.Cmd) {
	thread := m.selectedMail()
	if thread.ID == "" {
		return m, nil
	}
	unread := !thread.Unread
	label := "marked read"
	if unread {
		label = "marked unread"
	}
	return m, func() tea.Msg {
		updated, err := m.client.SetMailUnread(m.ctx, thread.ID, unread)
		return mailActionMsg{thread: updated, err: err, label: label}
	}
}

func (m Model) archiveSelectedMail() (Model, tea.Cmd) {
	thread := m.selectedMail()
	if thread.ID == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.ArchiveMail(m.ctx, thread.ID)
		return mailActionMsg{thread: thread, err: err, label: "archived"}
	}
}

func (m Model) trashSelectedMail() (Model, tea.Cmd) {
	thread := m.selectedMail()
	if thread.ID == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.TrashMail(m.ctx, thread.ID)
		return mailActionMsg{thread: thread, err: err, label: "moved to trash"}
	}
}

func (m *Model) handleMailAction(msg mailActionMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		m.err = msg.err.Error()
	} else {
		m.toast = msg.label
		m.applyMailThread(msg.thread)
		m.persistWorkspaceCache()
		cmds = append(cmds, m.imageDownloadCmdsForMail([]api.MailThread{msg.thread})...)
	}

	return cmds
}

func (m *Model) handleMailDraftAction(msg mailDraftActionMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		m.err = msg.err.Error()
	} else {
		m.toast = "draft saved"
	}

	return cmds
}
