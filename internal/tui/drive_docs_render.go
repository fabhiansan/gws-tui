package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) driveListRows(width int) (string, []string, int, int) {
	title := fmt.Sprintf(" [1]-Drive (%d) ", len(m.driveFiles))
	lines := []string{}
	selStart, selEnd := -1, -1
	for i, file := range m.driveFiles {
		prefix := " "
		if i == m.selected[FeatureDrive] {
			selStart = len(lines)
		}
		kind := driveFileKind(file.MimeType)
		meta := kind
		if !file.ModifiedTime.IsZero() {
			meta += " " + relative(file.ModifiedTime)
		}
		lines = append(lines, prefix+m.icon("◫", "f")+" "+truncate(file.Name, width-16)+" "+m.subtle(meta))
		if i == m.selected[FeatureDrive] {
			selEnd = len(lines) - 1
		}
	}
	return title, lines, selStart, selEnd
}

func (m Model) docsListRows(width int) (string, []string, int, int) {
	title := ""
	if strings.TrimSpace(m.search) != "" {
		title = fmt.Sprintf(" [1]-Search /%s (%d) ", m.search, len(m.docFiles))
	} else {
		title = fmt.Sprintf(" [1]-Docs (%d) ", len(m.docFiles))
	}
	lines := []string{}
	selStart, selEnd := -1, -1
	for i, file := range m.docFiles {
		prefix := " "
		if i == m.selected[FeatureDocs] {
			selStart = len(lines)
		}
		meta := ""
		if !file.ModifiedTime.IsZero() {
			meta = " " + m.subtle(relative(file.ModifiedTime))
		}
		lines = append(lines, prefix+m.icon("▤", "d")+" "+truncate(file.Name, width-12)+meta)
		if i == m.selected[FeatureDocs] {
			selEnd = len(lines) - 1
		}
	}
	return title, lines, selStart, selEnd
}

func (m Model) driveDetail() string {
	file := m.selectedDriveFile()
	if file.ID == "" {
		return centerText("No Drive files found.", m.detail.Width)
	}
	lines := []string{
		"Name:     " + file.Name,
		"Type:     " + fallback(driveFileKind(file.MimeType), "-"),
		"Modified: " + formatOptionalTime(file.ModifiedTime, "Mon, 02 Jan 2006 15:04"),
		"Size:     " + formatBytes(file.Size),
		"Link:     " + fallback(file.WebViewLink, "-"),
		"Resource: " + file.ID,
		"",
		m.subtle("y yank metadata · / search · m load more"),
	}
	return displayText(strings.Join(wrapDetailLines(lines, m.detailTextWidth()), "\n"))
}

func (m *Model) docsDetail() string {
	file := m.selectedDocFile()
	if file.ID == "" {
		return centerText("No Google Docs files found.", m.detail.Width)
	}
	if m.docLoadingID == file.ID {
		return centerText("Loading document...", m.detail.Width)
	}
	title := fallback(m.doc.Title, file.Name)
	width := m.detailTextWidth()
	lines := []string{
		"Title:    " + title,
		"Modified: " + formatOptionalTime(file.ModifiedTime, "Mon, 02 Jan 2006 15:04"),
		"Link:     " + fallback(file.WebViewLink, "-"),
		"Resource: " + file.ID,
		"",
		m.subtle("Document"),
		"",
	}
	lines = wrapDetailLines(lines, width)
	if len(m.doc.Blocks) > 0 {
		lines = append(lines, m.renderDocBlocks(m.doc.Blocks, width, countDisplayLines(lines))...)
	} else {
		body := fallback(m.doc.Body, "(empty document)")
		lines = append(lines, wrapDetailLines(strings.Split(body, "\n"), width)...)
	}
	lines = append(lines, "", m.subtle("y yank text · / search · m load more"))
	return displayText(strings.Join(lines, "\n"))
}

func (m *Model) renderDocBlocks(blocks []api.DocBlock, width, startLine int) []string {
	var lines []string
	for _, block := range blocks {
		switch block.Kind {
		case api.DocBlockTitle, api.DocBlockSubtitle, api.DocBlockHeading, api.DocBlockParagraph, api.DocBlockListItem:
			lines = append(lines, m.renderDocTextBlock(block, width)...)
		case api.DocBlockTable:
			lines = append(lines, m.renderDocTable(block, width)...)
		case api.DocBlockImage:
			attLines, ranges := m.renderDocImage(block)
			base := startLine + countDisplayLines(lines)
			for _, r := range ranges {
				for i := 0; i < r.rows; i++ {
					m.mapDetailAttachmentLine(base+r.start+i, r.attachment)
				}
			}
			lines = append(lines, attLines...)
		default:
			if text := strings.TrimSpace(block.Text); text != "" {
				lines = append(lines, wrapDetailLines([]string{text}, width)...)
			}
		}
		if len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) != "" {
			lines = append(lines, "")
		}
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func (m Model) renderDocTextBlock(block api.DocBlock, width int) []string {
	text := m.renderDocInlines(block.Inlines)
	if strings.TrimSpace(ansi.Strip(text)) == "" {
		text = displayText(block.Text)
	}
	switch block.Kind {
	case api.DocBlockTitle:
		style := lipgloss.NewStyle().Bold(true)
		if !m.cfg.NoColor {
			style = style.Foreground(lipgloss.Color(m.theme.Accent))
		}
		text = style.Render(text)
	case api.DocBlockSubtitle:
		text = m.subtle(text)
	case api.DocBlockHeading:
		style := lipgloss.NewStyle().Bold(true)
		if !m.cfg.NoColor {
			style = style.Foreground(lipgloss.Color(m.theme.Accent))
		}
		prefix := ""
		if block.Level > 1 {
			prefix = strings.Repeat("#", min(block.Level, 6)) + " "
		}
		text = style.Render(prefix + text)
	case api.DocBlockListItem:
		indent := strings.Repeat("  ", max(0, block.ListLevel))
		marker := m.icon("•", "-")
		return wrapDetailLines([]string{indent + marker + " " + text}, width)
	}
	return wrapDetailLines([]string{text}, width)
}

func (m Model) renderDocInlines(inlines []api.DocInline) string {
	var b strings.Builder
	for _, inline := range inlines {
		text := displayText(inline.Text)
		if text == "" && inline.LinkURL == "" {
			continue
		}
		style := lipgloss.NewStyle().
			Bold(inline.Bold).
			Italic(inline.Italic).
			Underline(inline.Underline || inline.LinkURL != "").
			Strikethrough(inline.Strikethrough)
		if inline.LinkURL != "" && !m.cfg.NoColor {
			style = style.Foreground(lipgloss.Color(m.theme.Accent))
		}
		if text != "" {
			b.WriteString(style.Render(text))
		}
		if inline.LinkURL != "" && !strings.Contains(text, inline.LinkURL) {
			if text != "" {
				b.WriteByte(' ')
			}
			b.WriteString(m.subtle("(" + inline.LinkURL + ")"))
		}
	}
	return b.String()
}

func (m Model) renderDocTable(block api.DocBlock, width int) []string {
	if len(block.Rows) == 0 {
		return nil
	}
	cols := 0
	for _, row := range block.Rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	if cols == 0 {
		return nil
	}
	gap := 3
	maxCell := max(8, (width-(cols-1)*gap)/cols)
	colWidths := make([]int, cols)
	for _, row := range block.Rows {
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(row) {
				cell = displayText(row[i])
			}
			colWidths[i] = max(colWidths[i], min(maxCell, lipgloss.Width(cell)))
		}
	}
	for i := range colWidths {
		if colWidths[i] == 0 {
			colWidths[i] = min(maxCell, 4)
		}
	}

	var lines []string
	for rowIdx, row := range block.Rows {
		cells := make([]string, cols)
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(row) {
				cell = ansi.Truncate(displayText(row[i]), colWidths[i], "…")
			}
			cells[i] = padDisplay(cell, colWidths[i])
		}
		lines = append(lines, strings.Join(cells, " | "))
		if rowIdx == 0 && len(block.Rows) > 1 {
			parts := make([]string, cols)
			for i, w := range colWidths {
				parts[i] = strings.Repeat("-", max(3, w))
			}
			lines = append(lines, m.subtle(strings.Join(parts, "-+-")))
		}
	}
	return wrapDetailLines(lines, width)
}

func (m *Model) renderDocImage(block api.DocBlock) ([]string, []attachmentLineRange) {
	if block.Attachment == nil {
		label := fallback(block.Text, "image")
		return []string{m.subtle("[image] " + label)}, nil
	}
	attLines, ranges := m.renderAttachmentsTracked([]api.Attachment{*block.Attachment})
	return attLines, ranges
}

func padDisplay(value string, width int) string {
	if pad := width - lipgloss.Width(value); pad > 0 {
		return value + strings.Repeat(" ", pad)
	}
	return value
}

func driveFileKind(mime string) string {
	switch mime {
	case "application/vnd.google-apps.folder":
		return "folder"
	case "application/vnd.google-apps.document":
		return "doc"
	case "application/vnd.google-apps.spreadsheet":
		return "sheet"
	case "application/vnd.google-apps.presentation":
		return "slides"
	case "application/pdf":
		return "pdf"
	default:
		if strings.HasPrefix(mime, "image/") {
			return "image"
		}
		if strings.HasPrefix(mime, "video/") {
			return "video"
		}
		if strings.HasPrefix(mime, "text/") {
			return "text"
		}
		return strings.TrimPrefix(mime, "application/")
	}
}

func formatBytes(size int64) string {
	if size <= 0 {
		return "-"
	}
	units := []string{"B", "KB", "MB", "GB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", size, units[unit])
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}
