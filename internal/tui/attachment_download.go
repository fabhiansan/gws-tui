package tui

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) detailAttachmentAtCursor() (api.Attachment, bool) {
	if m.detailAttachmentAt != nil {
		if att, ok := m.detailAttachmentAt[m.detailCursor]; ok {
			return att, true
		}
	}
	if m.detailImageAt != nil {
		if att, ok := m.detailImageAt[m.detailCursor]; ok {
			return att, true
		}
	}
	return api.Attachment{}, false
}

func (m Model) downloadAttachment(attachment api.Attachment) (Model, tea.Cmd) {
	name := attachmentDownloadName(attachment)
	m.toast = "downloading " + truncate(name, 48)
	return m, downloadAttachmentCmd(m.ctx, m.client, attachment)
}

func downloadAttachmentCmd(ctx context.Context, client api.WorkspaceClient, attachment api.Attachment) tea.Cmd {
	return func() tea.Msg {
		name := attachmentDownloadName(attachment)
		if client == nil {
			return attachmentDownloadedMsg{name: name, err: errors.New("workspace client is unavailable")}
		}
		if attachment.MediaResourceName() == "" {
			return attachmentDownloadedMsg{name: name, err: errors.New("attachment media resource is missing")}
		}
		outputPath, err := attachmentDownloadPath(attachment)
		if err != nil {
			return attachmentDownloadedMsg{name: name, err: err}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		c, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := client.DownloadAttachment(c, attachment, outputPath); err != nil {
			return attachmentDownloadedMsg{name: name, err: err}
		}
		return attachmentDownloadedMsg{name: name, path: outputPath}
	}
}

func attachmentDownloadPath(attachment api.Attachment) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return uniqueDownloadPath(filepath.Join(dir, attachmentDownloadName(attachment))), nil
}

func uniqueDownloadPath(path string) string {
	if !fileExists(path) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if !fileExists(candidate) {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d%s", base, time.Now().UnixNano(), ext)
}

func compactHomePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return path
	}
	return filepath.Join("~", rel)
}

func attachmentDownloadName(attachment api.Attachment) string {
	name := strings.TrimSpace(attachment.DisplayName())
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "attachment"
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "attachment"
	}
	name = sanitizeDownloadFileName(name)
	if filepath.Ext(name) == "" {
		if ext := attachmentExtension(attachment.ContentType); ext != "" {
			name += ext
		}
	}
	return name
}

func sanitizeDownloadFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r):
			b.WriteRune('_')
		case strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(strings.TrimSpace(b.String()), ".")
	if out == "" {
		return "attachment"
	}
	return out
}

func attachmentExtension(contentType string) string {
	contentType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	if contentType == "" {
		return ""
	}
	exts, err := mime.ExtensionsByType(contentType)
	if err != nil || len(exts) == 0 {
		return ""
	}
	return exts[0]
}
