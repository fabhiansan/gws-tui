package tui

import "github.com/fabhiansan/gws-tui/internal/api"

func (m Model) selectedDriveFile() api.DriveFile {
	if len(m.driveFiles) == 0 {
		return api.DriveFile{}
	}
	return m.driveFiles[clamp(m.selected[FeatureDrive], len(m.driveFiles))]
}

func (m Model) selectedDocFile() api.DriveFile {
	if len(m.docFiles) == 0 {
		return api.DriveFile{}
	}
	return m.docFiles[clamp(m.selected[FeatureDocs], len(m.docFiles))]
}
