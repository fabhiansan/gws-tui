package tui

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fabhiansan/gws-tui/internal/api"
	"github.com/mattn/go-runewidth"
)

func (m Model) spaceLabel(space api.Space) string {
	if space.UsesMemberLabels() {
		members, ok := m.membersBySpace[space.Name]
		if ok && len(members) > 0 {
			labels := make([]string, 0, len(members))
			for _, member := range members {
				if member.Type != "" && member.Type != "HUMAN" {
					continue
				}
				if m.isSelfUserID(member.UserID) {
					continue
				}
				labels = append(labels, m.memberLabel(member))
			}
			if len(labels) > 0 {
				return strings.Join(labels, ", ")
			}
		}
		if label := m.stripSelfFromSpaceTitle(space.DisplayName); label != "" {
			return label
		}
		if label := m.stripSelfFromSpaceTitle(space.FormattedName); label != "" {
			return label
		}
	}
	if space.DisplayName != "" {
		return space.DisplayName
	}
	if space.FormattedName != "" {
		return space.FormattedName
	}
	if space.SpaceType == "DIRECT_MESSAGE" {
		return "Direct message"
	}
	if space.SpaceType == "GROUP_CHAT" {
		return "Group chat"
	}
	return space.Title()
}

func (m Model) userLabelOrID(userID string) string {
	key := normalizeUserKey(userID)
	if key != "" {
		if label, ok := m.userLabels[key]; ok && label != "" {
			return label
		}
		if label, ok := m.userLabels["users/"+key]; ok && label != "" {
			return label
		}
	}
	if label, ok := m.userLabels[userID]; ok && label != "" {
		return label
	}
	if key == "" {
		return "unknown"
	}
	return key
}

func (m Model) memberLabel(member api.SpaceMember) string {
	key := normalizeUserKey(member.UserID)
	if key != "" {
		if label, ok := m.userLabels[key]; ok && label != "" && label != key {
			return label
		}
	}
	if member.DisplayName != "" && !strings.HasPrefix(member.DisplayName, "users/") {
		return member.DisplayName
	}
	return m.userLabelOrID(member.UserID)
}

func (m Model) senderLabel(msg api.ChatMessage) string {
	if msg.SenderName != "" && !strings.HasPrefix(msg.SenderName, "users/") {
		return msg.SenderName
	}
	userID := api.UserIDFromName(msg.SenderID)
	if userID == "" {
		userID = api.UserIDFromName(msg.SenderName)
	}
	return m.userLabelOrID(userID)
}

func (m Model) View() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 32
	}

	if m.authRequired {
		return m.theme.Root.Width(width).Height(height).Render(m.authView(width, height))
	}
	if m.loading && !m.cacheLoaded {
		return m.theme.Root.Width(width).Height(height).Render(m.loadingView(width, height))
	}

	body := m.mainView(width, height)
	if m.helpVisible {
		help := m.renderHelp(max(50, width-4), max(10, height-2))
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, help)
	}
	if m.imageViewer != nil {
		return m.renderImageViewer(width, height)
	}
	if m.modal != nil {
		modal := m.renderModal(max(40, width-14))
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
	}
	return body
}

func (m Model) loadingView(width, height int) string {
	total := m.loadTotal
	if total <= 0 {
		total = len(workspaceInitialLoadStages)
	}
	step := m.loadStep
	if step < 0 {
		step = 0
	}
	if step > total {
		step = total
	}
	stage := m.loadStage
	if stage == "" {
		stage = "Starting workspace fetch"
	}

	boxWidth := min(76, max(24, width-4))
	lineWidth := max(8, boxWidth-4)
	progressWidth := max(12, boxWidth-18)
	filled := 0
	if total > 0 {
		filled = progressWidth * step / total
	}
	bar := m.accent(strings.Repeat(m.icon("█", "="), filled)) + m.subtle(strings.Repeat(m.icon("░", "-"), progressWidth-filled))
	header := fmt.Sprintf("%s Loading workspace", m.spinner.View())
	progress := fmt.Sprintf("[%s] %d/%d", bar, step, total)

	lines := []string{
		header,
		m.subtle(truncate("Fetching Google Workspace data through "+m.upstreamHint, lineWidth)),
		"",
		progress,
		m.accent(truncate(stage, lineWidth)),
		"",
	}
	stageLimit := len(workspaceInitialLoadStages)
	if height > 0 {
		stageLimit = min(stageLimit, max(3, height-11))
	}
	stageStart := 0
	if stageLimit < len(workspaceInitialLoadStages) && step > stageLimit {
		stageStart = min(step-stageLimit, len(workspaceInitialLoadStages)-stageLimit)
	}
	stageEnd := min(len(workspaceInitialLoadStages), stageStart+stageLimit)
	if stageStart > 0 {
		lines = append(lines, m.subtle("  ..."))
	}
	for i := stageStart; i < stageEnd; i++ {
		label := workspaceInitialLoadStages[i]
		marker := "  "
		text := label
		switch {
		case i+1 < step:
			marker = m.accent(m.icon("✓", "v") + " ")
			text = m.subtle(label)
		case i+1 == step:
			marker = m.accent(m.icon("→", ">") + " ")
		}
		lines = append(lines, marker+truncate(text, lineWidth-2))
	}
	if stageEnd < len(workspaceInitialLoadStages) {
		lines = append(lines, m.subtle("  ..."))
	}
	lines = append(lines, "", m.subtle(truncate("q quits · the TUI will open as soon as the first workspace snapshot is ready", lineWidth)))

	content := strings.Join(lines, "\n")
	box := paneWithTitle(m.theme.Active, m.title(" gws · startup "), content, boxWidth, lipgloss.Height(content))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) mainView(width, height int) string {
	if m.feature == FeatureMail {
		return m.mailView(width, height)
	}
	if m.feature == FeatureDocs {
		return m.singlePaneView(width, height)
	}
	leftHBorder := m.theme.Pane.GetHorizontalBorderSize()
	leftVBorder := m.theme.Pane.GetVerticalBorderSize()
	detailHBorder := m.theme.Active.GetHorizontalBorderSize()
	detailVBorder := m.theme.Active.GetVerticalBorderSize()
	actionVBorder := m.theme.Input.GetVerticalBorderSize()
	statusH := 1

	leftW := max(20, int(float64(width)*0.30)-leftHBorder)
	rightW := max(20, width-leftW-leftHBorder-detailHBorder)

	actionContentH := max(3, min(8, strings.Count(m.input.Value(), "\n")+3))
	detailContentH := max(5, height-statusH-detailVBorder-actionContentH-actionVBorder)
	leftContentH := max(5, height-statusH-leftVBorder)

	left := m.renderList(leftW, leftContentH)
	detailStyle := m.theme.Pane
	if m.focusedPane == paneDetail {
		detailStyle = m.theme.Active
	}
	detail := paneWithTitle(detailStyle, m.title(" [2]-"+m.detailTitle()+" "), m.detail.View(), rightW, detailContentH)
	action := m.renderAction(rightW, actionContentH)
	right := lipgloss.JoinVertical(lipgloss.Left, detail, action)
	row := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	status := m.renderStatus(width)
	return m.theme.Root.Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left, row, status))
}

// singlePaneView is used for browse-first features where the list and the
// detail view share the same screen instead of sitting side by side.
func (m Model) singlePaneView(width, height int) string {
	hBorder := m.theme.Active.GetHorizontalBorderSize()
	vBorder := m.theme.Active.GetVerticalBorderSize()
	statusH := 1
	contentW := max(20, width-hBorder)
	contentH := max(5, height-statusH-vBorder)

	var pane string
	if m.singlePaneDetailVisible() {
		style := m.theme.Pane
		if m.focusedPane == paneDetail {
			style = m.theme.Active
		}
		pane = paneWithTitle(style, m.title(" [2]-"+m.detailTitle()+" "), m.detail.View(), contentW, contentH)
	} else {
		pane = m.renderList(contentW, contentH)
	}
	status := m.renderStatus(width)
	return m.theme.Root.Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left, pane, status))
}

func (m Model) singlePaneDetailVisible() bool {
	return m.feature == FeatureDocs && m.focusedPane == paneDetail
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
		lines[cursorLine] = m.theme.Selected.Width(innerW).Render(truncate(lines[cursorLine], innerW))
	}
	if offset := computeListOffset(len(lines), height, cursorLine, cursorLine); offset > 0 && offset < len(lines) {
		lines = lines[offset:]
	}
	body := fitLines(lines, innerW, height)
	style := m.theme.Pane
	if focused {
		style = m.theme.Active
	}
	return paneWithTitle(style, m.title(" Mail "), body, width, height)
}

func (m Model) authView(width, height int) string {
	var lines []string
	lines = append(lines, "Signing you into Google Workspace...", "")
	if m.auth.Valid() {
		lines = append(lines, "✓ Token cache found")
	} else {
		lines = append(lines, "→ Waiting for OAuth credentials")
	}
	if m.auth.ProjectID != "" {
		lines = append(lines, "Project: "+m.auth.ProjectID)
	}
	if m.auth.Error != "" {
		lines = append(lines, "Auth note: "+m.auth.Error)
	}
	lines = append(lines,
		"",
		"If your browser did not open, run:",
		"gws auth login",
		"",
		"Press r to retry · q to quit",
	)
	content := strings.Join(lines, "\n")
	boxWidth := min(74, width-4)
	box := m.theme.Active.Width(boxWidth).Render(content)
	titled := paneWithTitle(m.theme.Active, m.title(" gws · sign in "), content, boxWidth, lipgloss.Height(box))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, titled)
}

func (m Model) renderList(width, height int) string {
	var title string
	var lines []string
	selStart, selEnd := -1, -1
	switch m.feature {
	case FeatureChat:
		spaces := m.visibleSpaces()
		if m.spaceFilterActive || strings.TrimSpace(m.spaceFilter) != "" {
			title = fmt.Sprintf(" [1]-Spaces (%d/%d) /%s ", len(spaces), len(m.spaces), m.spaceFilter)
		} else {
			title = fmt.Sprintf(" [1]-Spaces (%d) ", len(spaces))
		}
		for i, space := range spaces {
			// The badge column is only emitted when there is a badge, so
			// spaces with nothing to flag sit flush left and a badge on an
			// unread/live space visibly juts out. Unread wins over Live: a
			// space with new messages glows accent, not just "watched".
			marker := ""
			switch {
			case space.Unread:
				marker = m.accent(m.icon("●", "*")) + " "
			case space.Live:
				marker = m.live(m.icon("●", "*")) + " "
			}
			avail := max(1, width-m.theme.Pane.GetHorizontalPadding()-1-lipgloss.Width(marker))
			name := truncate(m.spaceLabel(space), avail)
			if i == m.selected[FeatureChat] {
				selStart = len(lines)
				lines = append(lines, " "+marker+name)
				selEnd = len(lines) - 1
			} else {
				lines = append(lines, " "+marker+name)
			}
		}
	case FeatureMail:
		if strings.TrimSpace(m.search) != "" {
			title = fmt.Sprintf(" [1]-Search /%s (%d) ", m.search, len(m.mailThreads))
		} else {
			title = fmt.Sprintf(" [1]-%s (%d) ", fallback(m.mailFolder, defaultMailFolder), len(m.mailThreads))
		}
		rowW := max(20, width-m.theme.Pane.GetHorizontalPadding())
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
	case FeatureCalendar:
		title = fmt.Sprintf(" [1]-%s (%d) ", truncate(m.selectedCalendar().Summary, 24), len(m.events))
		lastDay := ""
		for i, event := range sortedEvents(m.events) {
			day := event.Start.Format("Mon 02 Jan")
			if day != lastDay {
				lines = append(lines, m.subtle(" "+day))
				lastDay = day
			}
			prefix := " "
			if i == m.selected[FeatureCalendar] {
				selStart = len(lines)
			}
			lines = append(lines, prefix+event.Start.Format("15:04")+"  "+truncate(event.Summary, width-12))
			if i == m.selected[FeatureCalendar] {
				selEnd = len(lines) - 1
			}
		}
	case FeatureMeet:
		title = fmt.Sprintf(" [1]-Meet spaces (%d) ", len(m.meetSpaces))
		for i, space := range m.meetSpaces {
			prefix := " "
			if i == m.selected[FeatureMeet] {
				selStart = len(lines)
			}
			status := ""
			if space.IsActive() {
				status = " " + m.live("active")
			}
			label := lastSegment(space.Name)
			if label == "" {
				label = space.MeetingCode
			}
			lines = append(lines, prefix+truncate(label, width-14)+status)
			if i == m.selected[FeatureMeet] {
				selEnd = len(lines) - 1
			}
		}
	case FeatureTasks:
		list := m.selectedTaskList()
		title = fmt.Sprintf(" [1]-Tasks: %s (%d) ", truncate(fallback(list.Title, "Tasks"), 18), len(m.tasks))
		for i, task := range m.tasks {
			prefix := " "
			if i == m.selected[FeatureTasks] {
				selStart = len(lines)
			}
			marker := m.icon("☐", "-")
			if strings.EqualFold(task.Status, "completed") {
				marker = m.live(m.icon("☑", "x"))
			}
			due := ""
			if !task.Due.IsZero() {
				due = " " + m.subtle(task.Due.Format("Jan 02"))
			}
			lines = append(lines, prefix+marker+" "+truncate(task.Title, width-14)+due)
			if i == m.selected[FeatureTasks] {
				selEnd = len(lines) - 1
			}
		}
		if len(m.taskLists) > 1 {
			lines = append(lines, "", m.subtle("[/[ previous list  ] next list]"))
		}
	case FeatureDrive:
		title = fmt.Sprintf(" [1]-Drive (%d) ", len(m.driveFiles))
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
	case FeatureDocs:
		if strings.TrimSpace(m.search) != "" {
			title = fmt.Sprintf(" [1]-Search /%s (%d) ", m.search, len(m.docFiles))
		} else {
			title = fmt.Sprintf(" [1]-Docs (%d) ", len(m.docFiles))
		}
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
	}
	if len(lines) == 0 {
		if m.feature == FeatureDocs && m.loading {
			label := "Loading docs..."
			if strings.TrimSpace(m.search) != "" {
				label = "Searching docs /" + m.search + "..."
			}
			lines = []string{"", " " + label}
		} else {
			lines = []string{"", " No items yet."}
		}
	}
	innerW := max(1, width-m.theme.Pane.GetHorizontalPadding())
	viewportH := max(1, height)
	if selStart >= 0 && selEnd >= selStart {
		for i := selStart; i <= selEnd && i < len(lines); i++ {
			lines[i] = m.theme.Selected.Width(innerW).Render(truncate(lines[i], innerW))
		}
	}
	offset := computeListOffset(len(lines), viewportH, selStart, selEnd)
	visible := lines
	if offset > 0 && offset < len(lines) {
		visible = lines[offset:]
	}
	body := fitLines(visible, innerW, viewportH)
	style := m.theme.Pane
	if m.focusedPane == paneList {
		style = m.theme.Active
	}
	return paneWithTitle(style, m.title(title), body, width, height)
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

// padCell truncates value to width display cells, then right-pads it with
// spaces so column-aligned rows line up regardless of the text inside.
func padCell(value string, width int) string {
	value = truncate(value, width)
	if pad := width - lipgloss.Width(value); pad > 0 {
		value += strings.Repeat(" ", pad)
	}
	return value
}

func computeListOffset(total, viewportH, selStart, selEnd int) int {
	if total <= viewportH || selEnd < 0 {
		return 0
	}
	offset := 0
	if selEnd >= viewportH {
		offset = selEnd - viewportH + 1
	}
	if selStart >= 0 && selStart < offset {
		offset = selStart
	}
	if offset < 0 {
		offset = 0
	}
	if maxOffset := total - viewportH; offset > maxOffset {
		offset = maxOffset
	}
	return offset
}

func (m Model) renderAction(width, height int) string {
	focused := m.focusedPane == paneAction
	title := " [3]-" + m.actionTitle() + " "
	content := ""
	if focused {
		content = m.input.View()
	} else {
		content = m.subtle(m.actionPlaceholder())
	}
	if chip := m.pendingAttachmentsChip(); chip != "" {
		content = chip + "\n" + content
	}
	style := m.theme.Input
	if !focused {
		style = style.BorderForeground(lipgloss.Color(m.theme.Subtle))
	}
	return paneWithTitle(style, m.title(title), content, width, height)
}

// pendingAttachmentsChip surfaces staged chat uploads above the composer so
// users see what will be sent on Enter even after the success toast fades.
func (m Model) pendingAttachmentsChip() string {
	if m.feature != FeatureChat || len(m.pendingChatAttachments) == 0 {
		return ""
	}
	return m.subtle(fmt.Sprintf("[attach] %d image(s) pending - Enter to send, Ctrl+X to clear", len(m.pendingChatAttachments)))
}

func (m Model) featureLabel(f Feature) string {
	switch f {
	case FeatureChat:
		return "Chat"
	case FeatureMail:
		return "Mail"
	case FeatureCalendar:
		return "Calendar"
	case FeatureMeet:
		return "Meet"
	case FeatureTasks:
		return "Tasks"
	case FeatureDrive:
		return "Drive"
	case FeatureDocs:
		return "Docs"
	default:
		return string(f)
	}
}

func (m Model) featureIcon(f Feature) string {
	switch f {
	case FeatureChat:
		return m.icon("◉", "c")
	case FeatureMail:
		return m.icon("✉", "m")
	case FeatureCalendar:
		return m.icon("◫", "k")
	case FeatureMeet:
		return m.icon("◎", "v")
	case FeatureTasks:
		return m.icon("☑", "t")
	case FeatureDrive:
		return m.icon("◫", "d")
	case FeatureDocs:
		return m.icon("▤", "o")
	default:
		return m.icon("◦", "-")
	}
}

// wideGlyphs lists the symbol runes the status bar draws that many
// terminals render two cells wide even though lipgloss measures them as
// one. statusWidth counts them as 2 so the compact-mode and truncate
// decisions below fire before the row overflows onto a second line.
var wideGlyphs = map[rune]struct{}{
	'◉': {}, '✉': {}, '◫': {}, '◎': {}, '☑': {}, '▤': {}, '●': {}, '◦': {},
}

// statusWidth is a pessimistic estimate of how many terminal columns s
// occupies: lipgloss.Width plus one extra cell per wide glyph. The status
// row budget uses this instead of lipgloss.Width so it stays in sync with
// what the terminal actually draws — lipgloss undercounts the symbol
// icons, which is what lets the row silently overflow onto a second line.
func statusWidth(s string) int {
	w := lipgloss.Width(s)
	for _, r := range ansi.Strip(s) {
		if _, ok := wideGlyphs[r]; ok {
			w++
		}
	}
	return w
}

func (m Model) renderStatus(width int) string {
	brand := m.theme.StatusBrand.Render(" gws ")
	tabSep := m.theme.StatusSeparator.Render("│")

	// renderTabs builds the feature tab strip. The compact form drops the
	// word label and keeps just the icon + Ctrl-number, so a narrow
	// terminal still shows every tab instead of overflowing the row.
	renderTabs := func(compact bool) string {
		tabs := make([]string, 0, len(featureOrder))
		for i, feature := range featureOrder {
			var label string
			if compact {
				label = fmt.Sprintf("%s ^%d", m.featureIcon(feature), i+1)
			} else {
				label = fmt.Sprintf("%s %s ^%d", m.featureIcon(feature), m.featureLabel(feature), i+1)
			}
			if feature == m.feature {
				tabs = append(tabs, m.theme.ActiveTab.Render(label))
			} else {
				tabs = append(tabs, m.theme.Tab.Render(label))
			}
		}
		return strings.Join(tabs, tabSep)
	}

	// finalize stretches left/right to fill the row, then hard-truncates to
	// width. An over-long row *wraps* onto a second line — that extra row
	// pushes the layout past m.height and clips the UI, so this truncate
	// guard is what keeps the status bar exactly one row tall.
	//
	// Every width calculation uses statusWidth, not lipgloss.Width, because
	// the terminal draws the symbol icons wider than lipgloss measures.
	// ansi.Truncate still counts the lipgloss way, so we aim it at
	// width-drift to shave the overflow; the final padding is also done by
	// hand, since lipgloss .Width() would re-pad by its own undercount and
	// re-introduce the overflow for any wide glyph left in the row.
	finalize := func(left, right string) string {
		gap := width - statusWidth(left) - statusWidth(right)
		filler := m.theme.Status.Render(strings.Repeat(" ", max(1, gap)))
		line := left + filler + right
		if statusWidth(line) > width {
			drift := statusWidth(line) - lipgloss.Width(line)
			line = ansi.Truncate(line, max(0, width-drift), "")
		}
		if pad := width - statusWidth(line); pad > 0 {
			line += m.theme.Status.Render(strings.Repeat(" ", pad))
		}
		return line
	}

	left := brand + renderTabs(false)

	if m.err != "" {
		errSeg := m.theme.StatusError.Render(fmt.Sprintf(" ! %s · x dismiss ", m.err))
		if width-statusWidth(left)-statusWidth(errSeg) < 1 {
			errSeg = m.theme.StatusError.Render(" ! " + truncate(m.err, max(10, width-statusWidth(left)-12)) + " · x ")
		}
		if width-statusWidth(left)-statusWidth(errSeg) < 1 {
			left = brand + renderTabs(true)
		}
		return finalize(left, errSeg)
	}

	segments := make([]string, 0, 5)

	if m.loading {
		segments = append(segments, m.theme.StatusSegmentAlt.Render(m.spinner.View()+" loading"))
	}

	if m.toast != "" {
		segments = append(segments, m.theme.StatusAccent.Render(" "+m.toast+" "))
	}

	liveCount := 0
	for _, space := range m.spaces {
		if space.Live {
			liveCount++
		}
	}
	liveLabel := fmt.Sprintf("%s %d live", m.icon("●", "*"), liveCount)
	if liveCount > 0 {
		segments = append(segments, m.theme.StatusAccent.Render(" "+liveLabel+" "))
	} else {
		segments = append(segments, m.theme.StatusSegmentAlt.Render(liveLabel))
	}

	segments = append(segments, m.theme.StatusSegment.Render(m.paneHints()))
	segments = append(segments, m.theme.StatusSegmentAlt.Render("? help"))
	segments = append(segments, m.theme.StatusSegment.Render("q quit"))

	rightSep := m.theme.StatusSeparator.Render(" ")
	right := strings.Join(segments, rightSep)

	// When the row is too tight, shrink the right segments first, then fall
	// back to compact tabs. finalize still truncates as a last resort.
	if width-statusWidth(left)-statusWidth(right) < 1 {
		minSegments := []string{}
		if liveCount > 0 {
			minSegments = append(minSegments, m.theme.StatusAccent.Render(" "+liveLabel+" "))
		}
		minSegments = append(minSegments, m.theme.StatusSegment.Render("? q"))
		right = strings.Join(minSegments, rightSep)
	}
	if width-statusWidth(left)-statusWidth(right) < 1 {
		left = brand + renderTabs(true)
	}

	return finalize(left, right)
}

// renderModal draws the compose modal. Each field renders its own editor
// View() so the real text cursor is visible, and a footer carries the vim
// mode badge plus the keys that act on the modal.
func (m Model) renderModal(width int) string {
	if m.modal == nil {
		return ""
	}
	modalWidth := min(width, 88)
	lines, _ := m.modalContentLines()
	content := strings.Join(lines, "\n")
	// paneWithTitle re-applies the modal style; +2 covers the modal's vertical
	// padding so the content never gets clipped.
	return paneWithTitle(m.theme.Modal, m.title(" "+m.modal.title+" "), content, modalWidth, len(lines)+2)
}

// modalContentLines builds the modal body line by line and, alongside it, a
// parallel slice mapping each line to the field index it belongs to (-1 for
// chrome). The mapping lets the mouse layer hit-test which field a click hit.
func (m Model) modalContentLines() (lines []string, fieldOf []int) {
	for i := range m.modal.fields {
		field := &m.modal.fields[i]
		focused := i == m.modal.focus
		marker := "  "
		if focused {
			marker = m.accent(m.icon("▎", "|")) + " "
		}
		label := fmt.Sprintf("%-9s", field.Label)
		if focused {
			label = m.accent(label)
		}
		if field.Multiline {
			lines = append(lines, marker+label)
			fieldOf = append(fieldOf, i)
			for _, bodyLine := range strings.Split(field.view(), "\n") {
				lines = append(lines, "  "+bodyLine)
				fieldOf = append(fieldOf, i)
			}
		} else {
			lines = append(lines, marker+label+" "+field.view())
			fieldOf = append(fieldOf, i)
		}
	}
	lines = append(lines, "")
	fieldOf = append(fieldOf, -1)
	if !m.modal.savedAt.IsZero() {
		lines = append(lines, m.subtle("autosaved "+m.modal.savedAt.Format("15:04:05")))
		fieldOf = append(fieldOf, -1)
	}
	lines = append(lines, m.modalHintLine())
	fieldOf = append(fieldOf, -1)
	return lines, fieldOf
}

// modalHintLine is the modal footer: a vim mode badge plus the keys that act
// on the modal, spelled out so they stay discoverable.
func (m Model) modalHintLine() string {
	keys := "^s send · ^q cancel · Tab field"
	if m.modal.kind == modalMail {
		keys = "^s send · ^d draft · ^q cancel · Tab field"
	}
	if !m.cfg.VimMode {
		return m.subtle("esc cancel · " + keys)
	}
	badge := m.accent("-- " + m.modal.vimMode.String() + " --")
	if m.modal.vimMode == vimModeNormal {
		return badge + "  " + m.subtle("i insert · hjkl move · dd yy p edit · "+keys)
	}
	return badge + "  " + m.subtle("esc normal mode · "+keys)
}

func (m Model) renderHelp(width, height int) string {
	type binding struct {
		keys string
		desc string
	}
	type section struct {
		name     string
		bindings []binding
	}
	sections := []section{
		{"Global", []binding{
			{"?", "toggle this help"},
			{"q · Ctrl+C", "quit"},
			{"Tab · S-Tab", "cycle features"},
			{"Ctrl+1..4", "chat / mail / calendar / meet"},
			{"r", "refresh current feature"},
			{"Ctrl+R", "reload config"},
			{"x", "dismiss error / toast"},
		}},
		{"Pane focus", []binding{
			{"1 · H · Ctrl+H", "focus list · Mail: folder rail"},
			{"2 · L · Ctrl+L", "focus detail pane"},
			{"3 · i", "focus action (input)"},
			{"Esc", "back to list"},
		}},
		{"List pane", []binding{
			{"j / k", "move selection"},
			{"g / G", "first / last item"},
			{"Enter · o", "open selected"},
			{"/", "search"},
			{"m", "load more"},
		}},
		{"Detail pane", []binding{
			{"h j k l", "move text cursor (vim)"},
			{"w / b / e", "word forward / back / end"},
			{"0 / $", "line start / end"},
			{"Ctrl+D · Ctrl+U", "half-page scroll"},
			{"Ctrl+F · Ctrl+B", "page scroll (vim)"},
			{"PgDn · PgUp", "page scroll"},
			{"gg / G", "top / bottom"},
			{"v / V", "visual char / line mode"},
			{"y / yy", "yank selection / line"},
			{"Enter / o", "open URL / attachment"},
			{"Esc", "exit visual · back to list"},
		}},
		{"Action pane", []binding{
			{"Enter", "submit"},
			{"Shift+Enter", "newline"},
			{"Esc", "vim: insert→normal · plain: cancel"},
			{"Esc (normal)", "exit composer"},
		}},
		{"Vim — composer (normal)", []binding{
			{"i / I / a / A", "insert here / line start / after / end"},
			{"o / O", "open line below / above"},
			{"h j k l", "move char / line"},
			{"w / b", "word forward / back"},
			{"0 / $", "line start / end"},
			{"gg / G", "top / bottom of input"},
			{"x / X", "delete char forward / back"},
			{"dd / cc", "delete line / change line"},
			{"dw de db", "delete word forward / to end / back"},
			{"cw ce / yw ye", "change / yank word"},
			{"d$ D / d0", "delete to line end / start"},
			{"yy / p / P", "yank / paste after / before"},
			{"Enter", "send message"},
		}},
		{"Vim — yank & paste", []binding{
			{"y (list)", "yank selected item to clipboard"},
			{"p (list)", "paste clipboard into composer"},
			{"a (list)", "append to composer in insert mode"},
		}},
		{"Chat", []binding{
			{"s", "toggle live subscription"},
			{"Ctrl+V", "attach image from clipboard"},
			{"Ctrl+X", "clear pending attachments"},
			{"R", "refresh all workspace data"},
		}},
		{"Mail", []binding{
			{"H", "focus folder sidebar"},
			{"j / k · Enter", "move · open folder (sidebar)"},
			{"s", "toggle star"},
			{"c", "compose"},
			{"R", "reply"},
			{"A", "reply all"},
			{"f", "forward"},
			{"l", "toggle label"},
			{"u", "mark read / unread"},
			{"e", "archive"},
			{"#", "trash"},
		}},
		{"Calendar", []binding{
			{"c", "new event"},
			{"E", "edit event"},
			{">", "move event"},
			{"y / n / M", "RSVP yes / no / maybe"},
			{"d", "delete event"},
			{"t", "jump to today"},
			{"] · [", "next / prev calendar"},
		}},
		{"Meet", []binding{
			{"n", "new space"},
			{"J", "join (open link)"},
			{"C", "copy link"},
			{"E", "end conference"},
		}},
		{"Tasks", []binding{
			{"Space", "complete / uncomplete"},
			{"d", "delete task"},
			{"] · [", "next / prev task list"},
			{"m", "load more"},
		}},
	}

	keyW := 0
	for _, sec := range sections {
		for _, b := range sec.bindings {
			if w := lipgloss.Width(b.keys); w > keyW {
				keyW = w
			}
		}
	}

	renderSection := func(sec section) []string {
		out := make([]string, 0, len(sec.bindings)+1)
		out = append(out, m.accent(sec.name))
		for _, b := range sec.bindings {
			pad := strings.Repeat(" ", max(1, keyW-lipgloss.Width(b.keys)+2))
			out = append(out, "  "+b.keys+pad+m.subtle(b.desc))
		}
		return out
	}

	rendered := make([][]string, len(sections))
	sectionWidth := 0
	for i, sec := range sections {
		rendered[i] = renderSection(sec)
		for _, line := range rendered[i] {
			if w := lipgloss.Width(line); w > sectionWidth {
				sectionWidth = w
			}
		}
	}

	title := m.title(" gws · keybindings ")
	footer := m.subtle("Press ? or Esc to close")

	chrome := m.theme.Modal.GetVerticalFrameSize() + 2
	contentHeight := max(1, height-chrome)

	columnGap := 3
	hFrame := m.theme.Modal.GetHorizontalFrameSize()
	maxColumns := (width - hFrame + columnGap) / (sectionWidth + columnGap)
	if maxColumns < 1 {
		maxColumns = 1
	}

	columns := 1
	chosenSpacer := 1
	bestHeight := 0
	for _, sp := range []int{1, 0} {
		fit := false
		for c := 1; c <= maxColumns; c++ {
			h := balancedHeight(rendered, c, sp)
			if h <= contentHeight {
				columns = c
				chosenSpacer = sp
				bestHeight = h
				fit = true
				break
			}
		}
		if fit {
			break
		}
		columns = maxColumns
		chosenSpacer = sp
		bestHeight = balancedHeight(rendered, columns, sp)
	}

	cols := distributeSections(rendered, columns, chosenSpacer)
	colStrs := make([]string, 0, len(cols)*2-1)
	colStyle := lipgloss.NewStyle().Width(sectionWidth)
	for i, col := range cols {
		if i > 0 {
			colStrs = append(colStrs, strings.Repeat(" ", columnGap))
		}
		var colLines []string
		for j, sec := range col {
			if j > 0 {
				for s := 0; s < chosenSpacer; s++ {
					colLines = append(colLines, "")
				}
			}
			colLines = append(colLines, sec...)
		}
		colStrs = append(colStrs, colStyle.Render(strings.Join(colLines, "\n")))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, colStrs...)

	if bestHeight > contentHeight {
		bodyLines := strings.Split(body, "\n")
		keep := max(1, contentHeight-1)
		if keep < len(bodyLines) {
			bodyLines = bodyLines[:keep]
			bodyLines = append(bodyLines, m.subtle("  … resize terminal to see all"))
		}
		body = strings.Join(bodyLines, "\n")
	}

	content := strings.Join([]string{body, "", footer}, "\n")

	modalWidth := min(width, 88)
	want := sectionWidth*columns + columnGap*(columns-1) + hFrame
	if want > modalWidth {
		modalWidth = min(width, want)
	}
	box := m.theme.Modal.Width(modalWidth).Render(content)
	return paneWithTitle(m.theme.Modal, title, content, modalWidth, lipgloss.Height(box))
}

func balancedHeight(sections [][]string, columns, spacer int) int {
	if columns < 1 {
		columns = 1
	}
	heights := make([]int, columns)
	for _, sec := range sections {
		idx := 0
		for i := 1; i < columns; i++ {
			if heights[i] < heights[idx] {
				idx = i
			}
		}
		if heights[idx] > 0 {
			heights[idx] += spacer
		}
		heights[idx] += len(sec)
	}
	maxH := 0
	for _, h := range heights {
		if h > maxH {
			maxH = h
		}
	}
	return maxH
}

func distributeSections(sections [][]string, columns, spacer int) [][][]string {
	if columns < 1 {
		columns = 1
	}
	cols := make([][][]string, columns)
	heights := make([]int, columns)
	for _, sec := range sections {
		idx := 0
		for i := 1; i < columns; i++ {
			if heights[i] < heights[idx] {
				idx = i
			}
		}
		if heights[idx] > 0 {
			heights[idx] += spacer
		}
		heights[idx] += len(sec)
		cols[idx] = append(cols[idx], sec)
	}
	return cols
}

func (m Model) detailTitle() string {
	var base string
	switch m.feature {
	case FeatureChat:
		if m.spaceFilterActive {
			base = "Spaces filter"
		} else {
			base = m.spaceLabel(m.selectedSpace())
		}
	case FeatureMail:
		base = fallback(m.selectedMail().Subject, "Mail")
	case FeatureCalendar:
		base = fallback(m.selectedEvent().Summary, "Calendar")
	case FeatureMeet:
		base = fallback(lastSegment(m.selectedMeet().Name), "Meet")
	case FeatureTasks:
		base = fallback(m.selectedTask().Title, "Tasks")
	case FeatureDrive:
		base = fallback(m.selectedDriveFile().Name, "Drive")
	case FeatureDocs:
		base = fallback(m.selectedDocFile().Name, "Docs")
	default:
		base = "gws"
	}
	if m.cfg.VimMode && m.focusedPane == paneDetail && m.detailVisual {
		if m.detailVisualLine {
			base += "  -- VISUAL LINE --"
		} else {
			base += "  -- VISUAL --"
		}
	}
	return base
}

func (m *Model) detailContent() string {
	switch m.feature {
	case FeatureChat:
		return m.chatDetail()
	case FeatureMail:
		return m.mailDetail()
	case FeatureCalendar:
		return m.calendarDetail()
	case FeatureMeet:
		return m.meetDetail()
	case FeatureTasks:
		return m.taskDetail()
	case FeatureDrive:
		return m.driveDetail()
	case FeatureDocs:
		return m.docsDetail()
	default:
		return ""
	}
}

func (m *Model) chatDetail() string {
	if m.spaceFilterActive {
		space := m.selectedSpace()
		if space.Name == "" {
			return centerText(fmt.Sprintf("No spaces match /%s", m.spaceFilter), m.detail.Width)
		}
		label := m.spaceLabel(space)
		if strings.TrimSpace(m.spaceFilter) == "" {
			return centerText("Type to filter spaces. Enter opens the selected space.", m.detail.Width)
		}
		return centerText(fmt.Sprintf("Filtering spaces /%s · Enter opens %s", m.spaceFilter, label), m.detail.Width)
	}
	if m.chatLoading && m.chatLoadSpace == m.selectedSpace().Name {
		return centerText("Loading messages...", m.detail.Width)
	}
	messages := m.visibleChatMessages()
	if len(messages) == 0 {
		if strings.TrimSpace(m.search) != "" && len(m.chatMessages) > 0 {
			return centerText(fmt.Sprintf("No messages match /%s", m.search), m.detail.Width)
		}
		return centerText("No messages in this space yet. Press i to write.", m.detail.Width)
	}
	textWidth := m.detailTextWidth()
	// Threaded grouping: each thread starter is followed by its reply
	// bursts in chronological order, so the chat reads as a nested
	// conversation rather than a flat timeline. The day separator still
	// fires on date boundaries inside the (now thread-grouped) stream.
	bursts := m.groupThreadedBursts(messages)
	var lines []string
	lastDay := ""
	for _, burst := range bursts {
		first := burst.messages[0]
		day := first.CreateTime.Format("Mon, 02 Jan 2006")
		if day != lastDay {
			lines = append(lines, m.subtle("─── "+friendlyDay(first.CreateTime)+" ───"))
			lastDay = day
		}
		burstStart := countDisplayLines(lines)
		if bubble := m.renderBubble(burst, textWidth); len(bubble) > 0 {
			lines = append(lines, bubble...)
		}
		// Attachments and cards render outside the bubble — they read
		// better as their own visual element, and bubbles with embedded
		// images would have to fight inline-image sizing. Indent them in
		// step with the bubble so they stay visually attached to it.
		for _, msg := range burst.messages {
			lines = m.appendAttachmentLines(lines, msg.Attachments, burst.indent)
			if cardLines := m.renderCards(msg.Cards, textWidth-burst.indent); len(cardLines) > 0 {
				if burst.indent > 0 && !burst.isSelf {
					pad := strings.Repeat(" ", burst.indent)
					for i := range cardLines {
						cardLines[i] = pad + cardLines[i]
					}
				}
				lines = append(lines, cardLines...)
			}
		}
		// Bind every line covered by this burst to its last message so the
		// `r` reply binding can resolve "which message is the cursor on".
		// Using the last message means a reply to a multi-message burst
		// lands on the freshest one — matches user intent in practice.
		burstEnd := countDisplayLines(lines)
		target := burst.messages[len(burst.messages)-1].ID
		for ln := burstStart; ln < burstEnd; ln++ {
			m.detailMessageAt[ln] = target
		}
		lines = append(lines, "")
	}
	return displayText(strings.Join(lines, "\n"))
}

// appendAttachmentLines renders msg.Attachments and records each attachment's
// line range so the vim cursor can resolve "which attachment is under me"
// when Enter is pressed in the detail pane. The indent argument shifts the
// rendered lines right so attachments under a reply bubble stay visually
// attached to it.
func (m *Model) appendAttachmentLines(lines []string, attachments []api.Attachment, indent int) []string {
	attLines, ranges := m.renderAttachmentsTracked(attachments)
	if indent > 0 {
		pad := strings.Repeat(" ", indent)
		for i := range attLines {
			attLines[i] = pad + attLines[i]
		}
	}
	base := countDisplayLines(lines)
	for _, r := range ranges {
		for i := 0; i < r.rows; i++ {
			m.mapDetailAttachmentLine(base+r.start+i, r.attachment)
		}
	}
	return append(lines, attLines...)
}

func (m *Model) mapDetailAttachmentLine(line int, attachment api.Attachment) {
	if m.detailAttachmentAt == nil {
		m.detailAttachmentAt = map[int]api.Attachment{}
	}
	m.detailAttachmentAt[line] = attachment
	if attachment.IsImage() {
		if m.detailImageAt == nil {
			m.detailImageAt = map[int]api.Attachment{}
		}
		m.detailImageAt[line] = attachment
	}
}

func (m Model) isSelfMessage(msg api.ChatMessage, displayName string) bool {
	if msg.SenderID == "users/me" {
		return true
	}
	if displayName == "You" {
		return true
	}
	if userID := api.UserIDFromName(msg.SenderID); userID != "" && m.selfUserIDs[userID] {
		return true
	}
	return false
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

func (m Model) calendarDetail() string {
	event := m.selectedEvent()
	if event.ID == "" {
		return centerText("No events. Press c to create one.", m.detail.Width)
	}
	lines := []string{
		"Time:      " + event.Start.Format("Mon, 02 Jan 2006 · 15:04") + " – " + event.End.Format("15:04"),
		"Location:  " + fallback(event.Location, "-"),
		"Meet:      " + fallback(event.HangoutLink, "-"),
		"RSVP:      " + fallback(event.RSVP, "needsAction"),
		"Attendees: " + strings.Join(event.Attendees, ", "),
		"",
		"─── Description ───",
		"",
		fallback(event.Description, "(no description)"),
		"",
		m.subtle("[Y]es  [N]o  [M]aybe · c new · E edit · d delete · > move · ]/[ calendar"),
	}
	return displayText(strings.Join(wrapDetailLines(lines, m.detailTextWidth()), "\n"))
}

func (m Model) meetDetail() string {
	space := m.selectedMeet()
	if space.Name == "" {
		return centerText("No Meet spaces yet. Press n to create one.", m.detail.Width)
	}
	conference := "none"
	if space.IsActive() {
		conference = "active"
		if space.ActiveParticipants > 0 {
			conference = fmt.Sprintf("active (%d people)", space.ActiveParticipants)
		}
	}
	joinURL := space.JoinURL()
	lines := []string{
		"Link:       " + fallback(joinURL, "-"),
		"Code:       " + fallback(space.MeetingCode, "-"),
		"Resource:   " + space.Name,
	}
	if spaceName := space.SpaceResourceName(); spaceName != "" && spaceName != space.Name {
		lines = append(lines, "Space:      "+spaceName)
	}
	lines = append(lines,
		"Access:     "+fallback(space.AccessType(), "-"),
		"Conference: "+conference,
	)
	if !space.Created.IsZero() {
		lines = append(lines, "Created:    "+space.Created.Format("02 Jan 2006"))
	}
	if !space.StartTime.IsZero() {
		lines = append(lines, "Started:    "+space.StartTime.Format("02 Jan 2006 15:04"))
	}
	if !space.EndTime.IsZero() {
		lines = append(lines, "Ended:      "+space.EndTime.Format("02 Jan 2006 15:04"))
	}
	if len(space.Participants) > 0 {
		lines = append(lines, "", "─── Participants ───")
		for _, participant := range space.Participants {
			label := fallback(participant.DisplayName, fallback(participant.User, participant.Name))
			lines = append(lines, "• "+label)
		}
	}
	if len(space.Recordings) > 0 {
		lines = append(lines, "", "─── Recordings ───")
		for _, recording := range space.Recordings {
			lines = append(lines, "• "+fallback(recording.File, recording.Name)+" "+fallback(recording.State, ""))
		}
	}
	if len(space.Transcripts) > 0 {
		lines = append(lines, "", "─── Transcripts ───")
		for _, transcript := range space.Transcripts {
			lines = append(lines, "• "+fallback(transcript.File, transcript.Name)+" "+fallback(transcript.State, ""))
		}
	}
	lines = append(lines,
		"",
		"─── Actions ───",
		"",
		m.subtle("[J]oin  [C]opy link  [E]nd  n new space"),
	)
	return displayText(strings.Join(wrapDetailLines(lines, m.detailTextWidth()), "\n"))
}

func (m Model) taskDetail() string {
	list := m.selectedTaskList()
	if list.ID == "" {
		return centerText("No task lists found.", m.detail.Width)
	}
	task := m.selectedTask()
	if task.ID == "" {
		return centerText("No tasks in "+list.Title+".", m.detail.Width)
	}
	lines := []string{
		"List:      " + fallback(list.Title, list.ID),
		"Status:    " + fallback(task.Status, "needsAction"),
		"Due:       " + formatOptionalTime(task.Due, "Mon, 02 Jan 2006"),
		"Completed: " + formatOptionalTime(task.Completed, "Mon, 02 Jan 2006 15:04"),
		"Updated:   " + formatOptionalTime(task.Updated, "Mon, 02 Jan 2006 15:04"),
		"Resource:  " + task.ID,
		"",
		"─── Notes ───",
		"",
		fallback(task.Notes, "(no notes)"),
		"",
		m.subtle("[Space] complete/uncomplete · d delete · [/] switch task list · m more"),
	}
	return displayText(strings.Join(wrapDetailLines(lines, m.detailTextWidth()), "\n"))
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

func (m Model) paneHints() string {
	switch m.focusedPane {
	case paneDetail:
		if m.cfg.VimMode {
			if m.detailVisual {
				return "motions extend  y yank  Esc cancel"
			}
			return "h/j/k/l cursor  w/b/e word  v visual  V line"
		}
		return "j/k scroll  g/G top/bot  i compose"
	case paneAction:
		if m.feature == FeatureChat && m.createSpaceMode {
			return "Enter create  Esc cancel"
		}
		if m.feature == FeatureChat && m.editMessageName != "" {
			return "Enter save  Esc cancel"
		}
		return "Enter send  Esc cancel"
	default:
		if m.spaceFilterActive && m.feature == FeatureChat {
			return "type filter  ↑/↓ move  Enter open  Esc clear"
		}
		return "j/k move  Enter open  i compose  / search"
	}
}

func (m Model) actionTitle() string {
	base := ""
	switch m.feature {
	case FeatureChat:
		if m.createSpaceMode {
			base = "new chat space · Enter create"
			break
		}
		if m.editMessageName != "" {
			base = "edit message · Enter save"
			break
		}
		base = "message · Enter send · Shift+Enter newline"
	case FeatureMail:
		base = "quick reply · c compose · R reply · A reply-all · l label · u read/unread"
	case FeatureCalendar:
		base = "quick add · Enter create"
	case FeatureMeet:
		base = "create space · Enter create"
	case FeatureTasks:
		base = "tasks · Space complete · d delete · [/] switch list · m more"
	case FeatureDrive:
		base = "drive · / search · m more"
	case FeatureDocs:
		base = "docs · / search · m more"
	default:
		base = "action"
	}
	if m.cfg.VimMode && m.focusedPane == paneAction {
		return "-- " + m.vimComposer.String() + " -- · " + base
	}
	return base
}

func (m Model) actionPlaceholder() string {
	switch m.feature {
	case FeatureChat:
		if m.createSpaceMode {
			return "Type a space name, optionally followed by | user emails, Enter to create."
		}
		if m.editMessageName != "" {
			return "Edit the message text, Enter to save."
		}
		return "Press i, type a message, Enter to send."
	case FeatureMail:
		return "Press c to compose, R to reply, u to toggle read state."
	case FeatureCalendar:
		return "Type quick-add text here with i, or press c for full event."
	case FeatureMeet:
		return "Press n to create a new Meet space."
	case FeatureTasks:
		return "Use Space to complete tasks, d to delete, [ and ] to switch lists."
	case FeatureDrive:
		return "Use / to search Drive, m to load more files."
	case FeatureDocs:
		return "Use / to search Docs, m to load more documents."
	default:
		return ""
	}
}

func (m Model) title(value string) string {
	return m.theme.Title.Render(value)
}

func paneWithTitle(style lipgloss.Style, title, content string, width, height int) string {
	box := style.Width(width).Height(height).Render(content)
	if title == "" {
		return box
	}
	nl := strings.Index(box, "\n")
	if nl == -1 {
		return box
	}
	topLine := box[:nl]
	rest := box[nl:]

	plainTop := ansi.Strip(topLine)
	plainRunes := []rune(plainTop)
	plainWidth := lipgloss.Width(plainTop)
	titleWidth := lipgloss.Width(title)
	if len(plainRunes) < 4 || plainWidth < titleWidth+4 {
		return box
	}

	leftCorner := string(plainRunes[0])
	rightCorner := string(plainRunes[len(plainRunes)-1])
	borderChar := string(plainRunes[1])
	innerWidth := len(plainRunes) - 2

	titlePos := (innerWidth - titleWidth) / 2
	if titlePos < 1 {
		titlePos = 1
	}
	rightPad := innerWidth - titlePos - titleWidth
	if rightPad < 1 {
		rightPad = 1
		titlePos = innerWidth - titleWidth - rightPad
		if titlePos < 1 {
			return box
		}
	}

	borderStyle := lipgloss.NewStyle().Foreground(style.GetBorderTopForeground())
	leftPart := borderStyle.Render(leftCorner + strings.Repeat(borderChar, titlePos))
	rightPart := borderStyle.Render(strings.Repeat(borderChar, rightPad) + rightCorner)

	return leftPart + title + rightPart + rest
}

func (m Model) accent(value string) string {
	if m.cfg.NoColor {
		return value
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Accent)).Render(value)
}

func (m Model) live(value string) string {
	if m.cfg.NoColor {
		return value
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Live)).Render(value)
}

func (m Model) warn(value string) string {
	if m.cfg.NoColor {
		return value
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Warn)).Render(value)
}

func (m Model) icon(unicode, ascii string) string {
	if m.cfg.NoIcons {
		return ascii
	}
	return unicode
}

var senderPalette = []string{
	"#06B6D4", // cyan
	"#22C55E", // green
	"#F59E0B", // amber
	"#EC4899", // pink
	"#8B5CF6", // violet
	"#14B8A6", // teal
	"#EF4444", // red
	"#3B82F6", // blue
	"#84CC16", // lime
	"#F97316", // orange
	"#A855F7", // purple
	"#0EA5E9", // sky
	"#F43F5E", // rose
	"#10B981", // emerald
	"#EAB308", // yellow
	"#6366F1", // indigo
}

func (m *Model) senderColor(key, value string) string {
	value = displayText(value)
	if m.cfg.NoColor {
		return value
	}
	spaceName := m.selectedSpace().Name
	if m.senderColorSpace != spaceName {
		m.senderColorSpace = spaceName
		m.senderColorIdx = map[string]int{}
		m.senderColorNext = 0
	}
	if m.senderColorIdx == nil {
		m.senderColorIdx = map[string]int{}
	}
	idx, ok := m.senderColorIdx[key]
	if !ok {
		idx = m.senderColorNext % len(senderPalette)
		m.senderColorIdx[key] = idx
		m.senderColorNext++
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(senderPalette[idx])).Render(value)
}

func (m Model) subtle(value string) string {
	return m.theme.SubtleText.Render(displayText(value))
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(displayText(value), width, "…")
}

func fitLines(lines []string, width, height int) string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		lines[i] = truncate(line, width)
	}
	return strings.Join(lines, "\n")
}

// narrowCond and wideCond measure string width with East Asian "ambiguous"
// glyphs (·, arrows, …) counted as one and two cells respectively. The
// EastAsianWidth field is pinned explicitly so the result does not depend on
// the host locale that runewidth.NewCondition would otherwise inherit.
var (
	narrowCond = newAmbiguousCond(false)
	wideCond   = newAmbiguousCond(true)
)

func newAmbiguousCond(eastAsian bool) *runewidth.Condition {
	c := runewidth.NewCondition()
	c.EastAsianWidth = eastAsian
	return c
}

// ambiguousDrift is how many extra columns s occupies on a terminal that
// renders East Asian "ambiguous" glyphs two cells wide. lipgloss and
// ansi.Wrap count those glyphs as one cell, so wrapping has to budget for
// this drift or the wrapped line still overflows the viewport and clips.
func ambiguousDrift(s string) int {
	plain := ansi.Strip(s)
	return wideCond.StringWidth(plain) - narrowCond.StringWidth(plain)
}

// wrapDetailLines folds each line to width display cells so long detail
// content (mail bodies, action hints, URLs) wraps onto extra rows instead of
// being clipped at the viewport's right edge. Blank lines are kept as-is so
// the vertical spacing of the detail panes is preserved.
func wrapDetailLines(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			out = append(out, line)
			continue
		}
		// Shrink the wrap target by the ambiguous-width drift: on a
		// terminal that draws ·/arrows two cells wide a line ansi.Wrap
		// thinks fits would still spill past the viewport edge and clip.
		target := max(1, width-ambiguousDrift(line))
		wrapped := wrapAnsi(line, target)
		if len(wrapped) == 0 {
			out = append(out, line)
			continue
		}
		out = append(out, wrapped...)
	}
	return out
}

func centerText(value string, width int) string {
	value = displayText(value)
	pad := max(0, (width-lipgloss.Width(value))/2)
	return strings.Repeat(" ", pad) + value
}

func rightAlign(value string, width int) string {
	value = displayText(value)
	pad := width - lipgloss.Width(value)
	if pad <= 0 {
		return value
	}
	return strings.Repeat(" ", pad) + value
}

func displayText(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	changed := false
	for _, r := range value {
		if isInvisibleFormatControl(r) {
			changed = true
			continue
		}
		b.WriteRune(r)
	}
	if !changed {
		return value
	}
	return b.String()
}

func isInvisibleFormatControl(r rune) bool {
	return unicode.In(r, unicode.Cf)
}

func friendlyDay(t time.Time) string {
	now := time.Now()
	if sameDay(now, t) {
		return "Today"
	}
	if sameDay(now.AddDate(0, 0, -1), t) {
		return "Yesterday"
	}
	return t.Format("Mon 02 Jan")
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func relative(t time.Time) string {
	diff := time.Since(t)
	if diff < time.Hour {
		return fmt.Sprintf("%dm", max(1, int(diff.Minutes())))
	}
	if diff < 24*time.Hour {
		return fmt.Sprintf("%dh", int(diff.Hours()))
	}
	if diff < 7*24*time.Hour {
		return fmt.Sprintf("%dd", int(diff.Hours()/24))
	}
	return t.Format("02 Jan")
}

func formatOptionalTime(t time.Time, layout string) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(layout)
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

func fallback(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func lastSegment(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "/")
	return parts[len(parts)-1]
}
