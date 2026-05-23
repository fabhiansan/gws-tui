package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) openSelectedDoc() (Model, tea.Cmd) {
	m.focusedPane = paneDetail
	return m.loadSelectedDoc()
}

func (m Model) loadSelectedDoc() (Model, tea.Cmd) {
	file := m.selectedDocFile()
	if file.ID == "" {
		m.doc = api.DocDocument{}
		m.docLoadingID = ""
		return m, nil
	}
	if m.doc.ID == file.ID && m.doc.Body != "" {
		return m, nil
	}
	m.doc = api.DocDocument{ID: file.ID, Title: file.Name}
	m.docLoadingID = file.ID
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()

		doc, err := m.client.Doc(ctx, file.ID)
		return docLoadedMsg{documentID: file.ID, doc: doc, err: err}
	}
}

func (m Model) downloadSelectedDriveFile() (Model, tea.Cmd) {
	file := m.selectedDriveFile()
	if file.ID == "" {
		return m, nil
	}
	if strings.HasPrefix(file.MimeType, "application/vnd.google-apps.") {
		m.toast = "Google-native files open in Docs tab"
		return m, nil
	}
	attachment := api.Attachment{
		ID:           file.ID,
		ResourceName: "drive/files/" + file.ID,
		Name:         file.Name,
		ContentType:  file.MimeType,
	}
	return m.downloadAttachment(attachment)
}

func (m *Model) handleDocLoaded(msg docLoadedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.documentID != m.selectedDocFile().ID {
		return cmds
	}
	m.docLoadingID = ""
	if msg.err != nil {
		m.err = msg.err.Error()
		return cmds
	}
	m.doc = msg.doc
	m.persistWorkspaceCache()

	return cmds
}
