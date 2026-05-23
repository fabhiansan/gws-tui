package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) loadDriveSectionCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		files, err := m.client.DriveFiles(ctx, api.DriveQuery{})
		return featureRefreshedMsg{feature: FeatureDrive, driveFiles: files, startup: true, err: err}
	}
}

func (m Model) loadDocsSectionCmd() tea.Cmd {
	selected := m.selected[FeatureDocs]
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		files, filesErr := m.client.Docs(ctx, api.DriveQuery{})
		doc := api.DocDocument{}
		var docErr error
		if len(files.Items) > 0 {
			doc, docErr = m.client.Doc(ctx, files.Items[clamp(selected, len(files.Items))].ID)
		}
		return featureRefreshedMsg{feature: FeatureDocs, docFiles: files, doc: doc, startup: true, err: firstErr(filesErr, docErr)}
	}
}
