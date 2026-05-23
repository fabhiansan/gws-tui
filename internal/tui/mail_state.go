package tui

import (
	"strings"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) selectedMail() api.MailThread {
	if len(m.mailThreads) == 0 {
		return api.MailThread{}
	}
	return m.mailThreads[clamp(m.selected[FeatureMail], len(m.mailThreads))]
}

// mailFolderByName resolves a folder display name to its definition, falling
// back to the Inbox so the inbox is always reachable.
func (m Model) mailFolderByName(name string) api.MailLabel {
	for _, folder := range m.mailFolderList() {
		if strings.EqualFold(folder.Name, name) {
			return folder
		}
	}
	return mailSystemFolderDefs[0]
}

// mailFolderIndex returns the position of a folder name in the sidebar list.
func (m Model) mailFolderIndex(name string) int {
	for i, folder := range m.mailFolderList() {
		if strings.EqualFold(folder.Name, name) {
			return i
		}
	}
	return 0
}

// mailQueryForFolder builds the Gmail thread query for a folder, carrying the
// resolved label IDs / search expression so custom labels and folders like
// Spam or All Mail fetch correctly.
func mailQueryForFolder(folder api.MailLabel, search, pageToken string) api.MailQuery {
	return api.MailQuery{
		Label:            folder.Name,
		LabelIDs:         folder.LabelIDs,
		LabelQuery:       folder.Query,
		IncludeSpamTrash: folder.IncludeSpamTrash,
		Search:           search,
		PageToken:        pageToken,
	}
}
