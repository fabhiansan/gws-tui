package tui

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) mailListRows(width int) (string, []string, int, int) {
	title := ""
	if strings.TrimSpace(m.search) != "" {
		title = fmt.Sprintf(" [2]-Search /%s (%d) ", m.search, len(m.mailThreads))
	} else {
		title = fmt.Sprintf(" [2]-%s (%d) ", fallback(m.mailFolder, defaultMailFolder), len(m.mailThreads))
	}
	rowW := max(20, width-m.theme.Pane.GetHorizontalPadding())
	lines := []string{}
	selStart, selEnd := -1, -1
	for i, thread := range m.mailThreads {
		selected := i == m.selected[FeatureMail]
		if selected {
			selStart = len(lines)
		}
		lines = append(lines, m.mailRow(thread, rowW))
		if selected {
			selEnd = len(lines) - 1
		}
	}
	return title, lines, selStart, selEnd
}

// mailView lays out the Mail feature like Gmail: a labels sidebar on the
// left, a wide single-line message list on the right, and a reading pane
// that takes the list's place once a message is opened. Every other feature
// keeps the shared three-pane layout in mainView.
func (m Model) mailView(width, height int) string {
	sidebarHBorder := m.theme.Pane.GetHorizontalBorderSize()
	sidebarVBorder := m.theme.Pane.GetVerticalBorderSize()
	mainHBorder := m.theme.Active.GetHorizontalBorderSize()
	mainVBorder := m.theme.Active.GetVerticalBorderSize()
	actionVBorder := m.theme.Input.GetVerticalBorderSize()
	statusH := 1

	sidebarW := mailSidebarWidth(width)
	mainW := max(30, width-sidebarW-sidebarHBorder-mainHBorder)

	sidebarContentH := max(5, height-statusH-sidebarVBorder)
	sidebar := m.renderMailSidebar(sidebarW, sidebarContentH)

	// The composer pane is hidden while browsing or reading mail so the inbox
	// uses the full height like Gmail; it only slides in below the reading
	// pane when the user focuses the composer to write a quick reply.
	var right string
	if m.focusedPane == paneAction {
		actionContentH := max(3, min(8, strings.Count(m.input.Value(), "\n")+3))
		mainContentH := max(5, height-statusH-mainVBorder-actionContentH-actionVBorder)
		right = lipgloss.JoinVertical(lipgloss.Left,
			m.mailMainPane(mainW, mainContentH),
			m.renderAction(mainW, actionContentH))
	} else {
		mainContentH := max(5, height-statusH-mainVBorder)
		right = m.mailMainPane(mainW, mainContentH)
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, right)
	status := m.renderStatus(width)
	return m.theme.Root.Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left, row, status))
}

// mailListVisible reports whether Mail's right column shows the inbox list
// rather than the reading pane. Browsing — with either the list or the folder
// sidebar focused — shows the list; opening a thread swaps in the reading pane.
func (m Model) mailListVisible() bool {
	return m.focusedPane == paneList || m.focusedPane == paneMailSidebar
}

// mailMainPane renders the slot shared by the inbox list and the reading
// pane: browsing shows the wide message list, and opening a message swaps in
// the reading pane until the user goes back to the list.
func (m Model) mailMainPane(width, height int) string {
	if m.mailListVisible() {
		return m.renderList(width, height)
	}
	detailStyle := m.theme.Pane
	if m.focusedPane == paneDetail {
		detailStyle = m.theme.Active
	}
	return paneWithTitle(detailStyle, m.title(" [2]-"+m.detailTitle()+" "), m.detail.View(), width, height)
}

// mailSidebarWidth is the content width of the Gmail-style label rail. It is
// shared by the renderer, the resize math, and mouse hit-testing so all three
// always agree on where the sidebar ends.
func mailSidebarWidth(width int) int {
	return max(14, min(26, width/5))
}

// defaultMailFolder is the folder the Mail feature opens on.
const defaultMailFolder = "Inbox"

// mailSystemFolderDefs are the fixed Gmail folders pinned at the top of the
// sidebar, in display order. Each carries the resolved query parameters so
// selecting one fetches exactly that folder regardless of how Gmail happens
// to name the underlying label.
var mailSystemFolderDefs = []api.MailLabel{
	{Name: "Inbox", LabelIDs: []string{"INBOX"}},
	{Name: "Starred", LabelIDs: []string{"STARRED"}},
	{Name: "Important", LabelIDs: []string{"IMPORTANT"}},
	{Name: "Sent", LabelIDs: []string{"SENT"}},
	{Name: "Drafts", LabelIDs: []string{"DRAFT"}},
	{Name: "Spam", LabelIDs: []string{"SPAM"}, IncludeSpamTrash: true},
	{Name: "Trash", LabelIDs: []string{"TRASH"}, IncludeSpamTrash: true},
	{Name: "All Mail", Query: "-in:spam -in:trash"},
}

var mailSystemLabelIDs = map[string]bool{
	"INBOX": true, "STARRED": true, "IMPORTANT": true, "SENT": true,
	"DRAFT": true, "DRAFTS": true, "SPAM": true, "TRASH": true,
	"UNREAD": true, "CHAT": true, "CHATS": true, "ALL MAIL": true,
	"SNOOZED": true, "SCHEDULED": true, "OUTBOX": true,
}

// mailIsSystemLabel reports whether a Gmail label belongs to the fixed system
// folders or a CATEGORY_* bucket, so it is not also listed as a user label.
func mailIsSystemLabel(name string, ids []string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if mailSystemLabelIDs[upper] || strings.HasPrefix(upper, "CATEGORY_") {
		return true
	}
	for _, def := range mailSystemFolderDefs {
		if strings.EqualFold(def.Name, name) {
			return true
		}
	}
	for _, id := range ids {
		u := strings.ToUpper(strings.TrimSpace(id))
		if mailSystemLabelIDs[u] || strings.HasPrefix(u, "CATEGORY_") {
			return true
		}
	}
	return false
}

// mailCustomFolders returns the user-created Gmail labels — every fetched
// label that is not a system folder or a CATEGORY_* bucket.
func (m Model) mailCustomFolders() []api.MailLabel {
	var custom []api.MailLabel
	for _, label := range m.mailLabels {
		name := strings.TrimSpace(label.Name)
		if name == "" || mailIsSystemLabel(name, label.LabelIDs) {
			continue
		}
		custom = append(custom, label)
	}
	return custom
}

// mailFolderList is the ordered set of selectable Mail folders: the fixed
// Gmail system folders followed by the user's own labels.
func (m Model) mailFolderList() []api.MailLabel {
	custom := m.mailCustomFolders()
	folders := make([]api.MailLabel, 0, len(mailSystemFolderDefs)+len(custom))
	folders = append(folders, mailSystemFolderDefs...)
	return append(folders, custom...)
}

// renderMailSidebar draws the Gmail-style folder rail: the system folders
// first, then a "Labels" group with the user's own labels. The active folder
// is marked with an accent arrow; while the rail is focused a cursor
// highlight tracks j/k navigation and Enter loads the folder under it.
func (m Model) renderMailSidebar(width, height int) string {
	innerW := max(6, width-m.theme.Pane.GetHorizontalPadding())
	folders := m.mailFolderList()
	systemCount := len(mailSystemFolderDefs)
	focused := m.focusedPane == paneMailSidebar

	cursor := -1
	if focused {
		cursor = clamp(m.mailFolderCursor, len(folders))
	}

	var lines []string
	cursorLine := -1
	for i, folder := range folders {
		if i == systemCount {
			lines = append(lines, "", m.subtle("Labels"))
		}
		text := truncate(folder.Name, innerW-2)
		marker := "  "
		switch {
		case strings.EqualFold(folder.Name, m.mailFolder):
			marker = m.accent(m.icon("▸", ">")) + " "
			text = m.accent(text)
		case i >= systemCount:
			text = m.subtle(text)
		}
		if i == cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, marker+text)
	}
	if cursorLine >= 0 && cursorLine < len(lines) {
		lines[cursorLine] = renderStylePreserve(m.theme.Selected.Width(innerW), truncate(lines[cursorLine], innerW))
	}
	if offset := computeListOffset(len(lines), height, cursorLine, cursorLine); offset > 0 && offset < len(lines) {
		lines = lines[offset:]
	}
	body := fitLines(lines, innerW, height)
	style := m.theme.Pane
	if focused {
		style = m.theme.Active
	}
	return paneWithTitle(style, m.title(" [1]-Mail "), body, width, height)
}

// mailRow renders one inbox entry as a single Gmail-style line: a star
// column, a fixed-width sender column, the subject followed by a dimmed
// snippet, and a right-aligned date. Unread threads are drawn brighter so the
// inbox scans the same way Gmail's list view does. The row is padded to the
// full width so the selection highlight covers it edge to edge.
func (m Model) mailRow(thread api.MailThread, width int) string {
	starGlyph := m.icon("☆", "-")
	if thread.Starred {
		starGlyph = m.icon("★", "*")
	}
	star := padCell(starGlyph, 2)
	if thread.Starred {
		star = m.warn(star)
	} else {
		star = m.subtle(star)
	}

	senderW := 18
	if width < 64 {
		senderW = 13
	}
	dateW := 6
	midW := max(8, width-6-senderW-dateW)

	sender := m.mailEmphasis(padCell(fallback(thread.Sender, "(unknown)"), senderW), thread.Unread)

	subject := strings.TrimSpace(thread.Subject)
	if subject == "" {
		subject = "(no subject)"
	}
	subjectPlain := truncate(subject, midW)
	used := lipgloss.Width(subjectPlain)
	middle := m.mailEmphasis(subjectPlain, thread.Unread)
	if snippet := mailSnippet(thread); snippet != "" && midW-used > 8 {
		snipPlain := truncate(snippet, midW-used-3)
		middle += m.subtle(" - " + snipPlain)
		used += 3 + lipgloss.Width(snipPlain)
	}
	if pad := midW - used; pad > 0 {
		middle += strings.Repeat(" ", pad)
	}

	date := truncate(relative(thread.Date), dateW)
	if pad := dateW - lipgloss.Width(date); pad > 0 {
		date = strings.Repeat(" ", pad) + date
	}

	return " " + star + sender + "  " + middle + " " + m.subtle(date)
}

// mailSnippet returns a one-line preview for a thread, preferring Gmail's own
// snippet and falling back to the first non-quoted line of the body. Runs of
// whitespace are collapsed so the preview stays on a single line.
func mailSnippet(thread api.MailThread) string {
	s := strings.TrimSpace(thread.Snippet)
	if s == "" {
		for _, line := range strings.Split(thread.Body, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, ">") {
				s = line
				break
			}
		}
	}
	return strings.Join(strings.Fields(s), " ")
}

// mailEmphasis brightens unread mail text and dims text that has already been
// read, mirroring Gmail's bold-unread / muted-read inbox styling.
func (m Model) mailEmphasis(value string, unread bool) string {
	if !unread {
		return m.subtle(value)
	}
	style := lipgloss.NewStyle().Bold(true)
	if !m.cfg.NoColor {
		style = style.Foreground(lipgloss.Color(m.theme.Fg))
	}
	return style.Render(displayText(value))
}

func (m *Model) mailDetail() string {
	thread := m.selectedMail()
	if thread.ID == "" {
		return centerText("No mail in this label.", m.detail.Width)
	}
	lines := []string{
		"From: " + thread.Sender + " <" + thread.SenderEmail + ">",
		"Date: " + thread.Date.Format("Mon, 02 Jan 2006 15:04"),
		"Labels: " + strings.Join(thread.Labels, ", "),
		"────────────────────────────────",
		"",
	}
	for _, line := range mailBodyDisplayLines(thread.Body) {
		if strings.HasPrefix(line, ">") {
			lines = append(lines, m.subtle(line))
		} else {
			lines = append(lines, line)
		}
	}
	// Wrap header + body to the viewport width *before* the attachment
	// section, so countDisplayLines below (and the attachment line map) is
	// measured against the final, wrapped line count.
	width := m.detailTextWidth()
	lines = wrapDetailLines(lines, width)
	if attLines, ranges := m.renderAttachmentsTracked(thread.Attachments); len(attLines) > 0 {
		lines = append(lines, "", m.subtle("Attachments"))
		base := countDisplayLines(lines)
		for _, r := range ranges {
			for i := 0; i < r.rows; i++ {
				m.mapDetailAttachmentLine(base+r.start+i, r.attachment)
			}
		}
		lines = append(lines, attLines...)
	}
	tail := []string{}
	if thread.QuotedLines > 0 {
		tail = append(tail, "", m.subtle(fmt.Sprintf("[+ %d lines quoted]", thread.QuotedLines)))
	}
	tail = append(tail, "", m.subtle("R reply · A reply-all · f forward · l label · e archive · # trash · s star · u read/unread"))
	lines = append(lines, wrapDetailLines(tail, width)...)
	return displayText(strings.Join(lines, "\n"))
}

func mailBodyDisplayLines(body string) []string {
	body = mailBodyDisplayText(body)
	if strings.TrimSpace(body) == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

func mailBodyDisplayText(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	if mailLooksHTML(body) {
		body = mailHTMLBreakRe.ReplaceAllString(body, "\n")
		body = mailHTMLBlockRe.ReplaceAllString(body, "\n")
		body = mailHTMLLinkRe.ReplaceAllString(body, "$1")
		body = mailHTMLTagRe.ReplaceAllString(body, "")
	}
	body = html.UnescapeString(body)
	body = displayText(body)

	var lines []string
	blankRun := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, " \t")
		if mailArtifactOnlyLine(line) {
			line = ""
		}
		if strings.TrimSpace(line) == "" {
			blankRun++
			if blankRun > 1 {
				continue
			}
			lines = append(lines, "")
			continue
		}
		blankRun = 0
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func mailLooksHTML(body string) bool {
	return mailHTMLBreakRe.MatchString(body) ||
		mailHTMLBlockRe.MatchString(body) ||
		mailHTMLLinkRe.MatchString(body) ||
		mailHTMLInlineRe.MatchString(body)
}

func mailArtifactOnlyLine(line string) bool {
	line = displayText(html.UnescapeString(line))
	line = strings.TrimSpace(line)
	if line == "" {
		return true
	}
	for _, r := range line {
		if !unicode.IsSpace(r) && r != '\u00a0' {
			return false
		}
	}
	return true
}

var (
	mailHTMLBreakRe  = regexp.MustCompile(`(?i)<br\s*/?>`)
	mailHTMLBlockRe  = regexp.MustCompile(`(?i)</?(p|div|section|article|header|footer|blockquote|tr|table|ul|ol|li|h[1-6])\b[^>]*>`)
	mailHTMLInlineRe = regexp.MustCompile(`(?i)</?(span|font|b|strong|i|em|u|img|style|meta|html|body|head)\b[^>]*>`)
	mailHTMLLinkRe   = regexp.MustCompile(`(?is)<a\s+[^>]*>(.*?)</a>`)
	mailHTMLTagRe    = regexp.MustCompile(`(?s)<[^>]+>`)
)
