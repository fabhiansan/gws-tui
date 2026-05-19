package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) spaceLabel(space api.Space) string {
	if space.SpaceType == "DIRECT_MESSAGE" || space.SpaceType == "GROUP_CHAT" {
		members, ok := m.membersBySpace[space.Name]
		if ok && len(members) > 0 {
			labels := make([]string, 0, len(members))
			for _, member := range members {
				if member.Type != "" && member.Type != "HUMAN" {
					continue
				}
				if m.selfUserIDs[member.UserID] {
					continue
				}
				labels = append(labels, m.userLabelOrID(member.UserID))
			}
			if len(labels) > 0 {
				return strings.Join(labels, ", ")
			}
		}
		if space.SpaceType == "GROUP_CHAT" && space.DisplayName != "" {
			return space.DisplayName
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
	if label, ok := m.userLabels[userID]; ok && label != "" {
		return label
	}
	if userID == "" {
		return "unknown"
	}
	return userID
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

func (m Model) mainView(width, height int) string {
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
		if strings.TrimSpace(m.search) != "" {
			title = fmt.Sprintf(" [1]-Spaces (%d/%d) /%s ", len(spaces), len(m.spaces), m.search)
		} else {
			title = fmt.Sprintf(" [1]-Spaces (%d) ", len(spaces))
		}
		for i, space := range spaces {
			marker := "  "
			// Unread wins over Live: a subscribed space that has new
			// messages should glow accent, not just look "watched".
			switch {
			case space.Unread:
				marker = m.accent(m.icon("●", "*")) + " "
			case space.Live:
				marker = m.live(m.icon("●", "*")) + " "
			}
			name := truncate(m.spaceLabel(space), width-8)
			if i == m.selected[FeatureChat] {
				selStart = len(lines)
				lines = append(lines, "  "+marker+name)
				selEnd = len(lines) - 1
			} else {
				lines = append(lines, "  "+marker+name)
			}
		}
	case FeatureMail:
		title = fmt.Sprintf(" [1]-Inbox (%d) ", len(m.mailThreads))
		for i, thread := range m.mailThreads {
			marker := "  "
			if thread.Unread {
				marker = m.accent(m.icon("●", "*")) + " "
			}
			if thread.Starred {
				marker = m.warn(m.icon("★", "*")) + " "
			}
			prefix := "  "
			selected := i == m.selected[FeatureMail]
			if selected {
				selStart = len(lines)
			}
			lines = append(lines, prefix+marker+truncate(thread.Sender, width-8))
			subject := truncate(thread.Subject+"  "+relative(thread.Date), width-6)
			lines = append(lines, "    "+subject)
			lines = append(lines, m.subtle("    "+strings.Repeat("─", max(1, width-8))))
			if selected {
				selEnd = len(lines) - 1
			}
		}
		if len(m.mailLabels) > 0 {
			var tabs []string
			for i, label := range m.mailLabels {
				if i >= 9 {
					break
				}
				tabs = append(tabs, fmt.Sprintf("%d:%s", i+1, label.Name))
			}
			lines = append(lines, "", m.subtle("["+truncate(strings.Join(tabs, "  "), width-8)+"]"))
		}
	case FeatureCalendar:
		title = fmt.Sprintf(" [1]-This week (%d) ", len(m.events))
		lastDay := ""
		for i, event := range sortedEvents(m.events) {
			day := event.Start.Format("Mon 02 Jan")
			if day != lastDay {
				lines = append(lines, m.subtle("  "+day))
				lastDay = day
			}
			prefix := "  "
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
			prefix := "  "
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
	}
	if len(lines) == 0 {
		lines = []string{"", "  No items yet."}
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
	style := m.theme.Input
	if !focused {
		style = style.BorderForeground(lipgloss.Color(m.theme.Subtle))
	}
	return paneWithTitle(style, m.title(title), content, width, height)
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
	default:
		return m.icon("◦", "-")
	}
}

func (m Model) renderStatus(width int) string {
	brand := m.theme.StatusBrand.Render(" gws ")

	tabs := make([]string, 0, len(featureOrder))
	tabSep := m.theme.StatusSeparator.Render("│")
	for i, feature := range featureOrder {
		label := fmt.Sprintf("%s %s ^%d", m.featureIcon(feature), m.featureLabel(feature), i+1)
		if feature == m.feature {
			tabs = append(tabs, m.theme.ActiveTab.Render(label))
		} else {
			tabs = append(tabs, m.theme.Tab.Render(label))
		}
	}
	tabsRendered := strings.Join(tabs, tabSep)
	left := brand + tabsRendered

	if m.err != "" {
		errSeg := m.theme.StatusError.Render(fmt.Sprintf(" ! %s · x dismiss ", m.err))
		gap := width - lipgloss.Width(left) - lipgloss.Width(errSeg)
		if gap < 1 {
			errSeg = m.theme.StatusError.Render(" ! " + truncate(m.err, max(10, width-lipgloss.Width(left)-12)) + " · x ")
			gap = width - lipgloss.Width(left) - lipgloss.Width(errSeg)
		}
		filler := m.theme.Status.Render(strings.Repeat(" ", max(1, gap)))
		return m.theme.Status.Width(width).Render(left + filler + errSeg)
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

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		minSegments := []string{}
		if liveCount > 0 {
			minSegments = append(minSegments, m.theme.StatusAccent.Render(" "+liveLabel+" "))
		}
		minSegments = append(minSegments, m.theme.StatusSegment.Render("? q"))
		right = strings.Join(minSegments, rightSep)
		gap = width - lipgloss.Width(left) - lipgloss.Width(right)
	}

	filler := m.theme.Status.Render(strings.Repeat(" ", max(1, gap)))
	return m.theme.Status.Width(width).Render(left + filler + right)
}

func (m Model) renderModal(width int) string {
	if m.modal == nil {
		return ""
	}
	var lines []string
	for i, field := range m.modal.fields {
		cursor := "  "
		if i == m.modal.focus {
			cursor = m.accent(m.icon("▎", "|") + " ")
		}
		value := field.Value
		if value == "" {
			value = m.subtle("(empty)")
		}
		label := fmt.Sprintf("%-11s", field.Label+":")
		if field.Multiline {
			lines = append(lines, cursor+label)
			for _, bodyLine := range strings.Split(value, "\n") {
				lines = append(lines, "  "+truncate(bodyLine, width-6))
			}
		} else {
			lines = append(lines, cursor+label+" "+truncate(value, width-18))
		}
	}
	if !m.modal.savedAt.IsZero() {
		lines = append(lines, "", m.subtle("autosaved "+m.modal.savedAt.Format("15:04:05")))
	}
	modalWidth := min(width, 88)
	content := strings.Join(lines, "\n")
	box := m.theme.Modal.Width(modalWidth).Render(content)
	height := lipgloss.Height(box)
	return paneWithTitle(m.theme.Modal, m.title(" "+m.modal.title+" "), content, modalWidth, height)
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
			{"1 · H · Ctrl+H", "focus list pane"},
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
			{"R", "refresh all workspace data"},
		}},
		{"Mail", []binding{
			{"s", "toggle star"},
			{"c", "compose"},
			{"R", "reply"},
			{"f", "forward"},
			{"e", "archive"},
			{"#", "trash"},
		}},
		{"Calendar", []binding{
			{"c", "new event"},
			{"y / n / M", "RSVP yes / no / maybe"},
			{"d", "delete event"},
			{"t", "jump to today"},
			{"] · [", "next / prev week"},
		}},
		{"Meet", []binding{
			{"n", "new space"},
			{"J", "join (open link)"},
			{"C", "copy link"},
			{"E", "end conference"},
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
		base = m.spaceLabel(m.selectedSpace())
	case FeatureMail:
		base = fallback(m.selectedMail().Subject, "Mail")
	case FeatureCalendar:
		base = fallback(m.selectedEvent().Summary, "Calendar")
	case FeatureMeet:
		base = fallback(lastSegment(m.selectedMeet().Name), "Meet")
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
	default:
		return ""
	}
}

func (m *Model) chatDetail() string {
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
	var lines []string
	lastDay := ""
	for _, msg := range messages {
		day := msg.CreateTime.Format("Mon, 02 Jan 2006")
		if day != lastDay {
			lines = append(lines, m.subtle("─── "+friendlyDay(msg.CreateTime)+" ───"))
			lastDay = day
		}
		name := m.senderLabel(msg)
		isSelf := m.isSelfMessage(msg, name)
		if isSelf {
			name = m.accent(name)
		} else {
			name = m.senderColor(msg.SenderID, name)
		}
		status := ""
		if msg.Pending {
			status = " " + m.subtle("(sending)")
		}
		block := []string{fmt.Sprintf("%s    %s%s", name, msg.CreateTime.Format("15:04"), status)}
		prefix := "  "
		if msg.ParentID != "" {
			prefix = "  " + m.icon("↪", ">") + " "
		}
		textWidth := m.detailTextWidth()
		prefixW := lipgloss.Width(prefix)
		wrapWidth := max(10, textWidth-prefixW)
		for _, line := range strings.Split(msg.Text, "\n") {
			if strings.HasPrefix(line, "```") {
				block = append(block, m.theme.Code.Width(max(12, textWidth-2)).Render(line))
				continue
			}
			for _, sub := range strings.Split(ansi.Wrap(line, wrapWidth, " -"), "\n") {
				block = append(block, prefix+sub)
			}
		}
		if isSelf {
			for _, bl := range block {
				lines = append(lines, rightAlign(bl, textWidth))
			}
		} else {
			lines = append(lines, block...)
		}
		lines = m.appendAttachmentLines(lines, msg.Attachments)
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// appendAttachmentLines renders msg.Attachments and records each image's
// line range in m.detailImageAt so the vim cursor can resolve "which image
// is under me" when Enter is pressed in the detail pane.
func (m *Model) appendAttachmentLines(lines []string, attachments []api.Attachment) []string {
	attLines, ranges := m.renderAttachmentsTracked(attachments)
	base := countDisplayLines(lines)
	for _, r := range ranges {
		for i := 0; i < r.rows; i++ {
			m.detailImageAt[base+r.start+i] = r.attachment
		}
	}
	return append(lines, attLines...)
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
	for _, line := range strings.Split(thread.Body, "\n") {
		if strings.HasPrefix(line, ">") {
			lines = append(lines, m.subtle(line))
		} else {
			lines = append(lines, line)
		}
	}
	if attLines, ranges := m.renderAttachmentsTracked(thread.Attachments); len(attLines) > 0 {
		lines = append(lines, "", m.subtle("Attachments"))
		base := countDisplayLines(lines)
		for _, r := range ranges {
			for i := 0; i < r.rows; i++ {
				m.detailImageAt[base+r.start+i] = r.attachment
			}
		}
		lines = append(lines, attLines...)
	}
	if thread.QuotedLines > 0 {
		lines = append(lines, "", m.subtle(fmt.Sprintf("[+ %d lines quoted]", thread.QuotedLines)))
	}
	lines = append(lines, "", m.subtle("R reply · f forward · e archive · # trash · s star"))
	return strings.Join(lines, "\n")
}

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
		m.subtle("[Y]es  [N]o  [M]aybe · c new event · d delete · ]/[ week"),
	}
	return strings.Join(lines, "\n")
}

func (m Model) meetDetail() string {
	space := m.selectedMeet()
	if space.Name == "" {
		return centerText("No Meet spaces yet. Press n then Enter to create one.", m.detail.Width)
	}
	conference := "none"
	if space.IsActive() {
		conference = "active"
		if space.ActiveParticipants > 0 {
			conference = fmt.Sprintf("active (%d people)", space.ActiveParticipants)
		}
	}
	lines := []string{
		"Link:       " + fallback(space.MeetingURI, "-"),
		"Code:       " + fallback(space.MeetingCode, "-"),
		"Resource:   " + space.Name,
		"Access:     " + fallback(space.AccessType(), "-"),
		"Conference: " + conference,
	}
	if !space.Created.IsZero() {
		lines = append(lines, "Created:    "+space.Created.Format("02 Jan 2006"))
	}
	lines = append(lines,
		"",
		"─── Actions ───",
		"",
		m.subtle("[J]oin  [C]opy link  [E]nd  n new space"),
	)
	return strings.Join(lines, "\n")
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
		return "Enter send  Esc cancel"
	default:
		return "j/k move  Enter open  i compose  / search"
	}
}

func (m Model) actionTitle() string {
	base := ""
	switch m.feature {
	case FeatureChat:
		base = "message · Enter send · Shift+Enter newline"
	case FeatureMail:
		base = "quick reply · c compose · R reply"
	case FeatureCalendar:
		base = "quick add · Enter create"
	case FeatureMeet:
		base = "create space · Enter create"
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
		return "Press i, type a message, Enter to send."
	case FeatureMail:
		return "Press c to compose, R to reply, / to search."
	case FeatureCalendar:
		return "Type quick-add text here with i, or press c for full event."
	case FeatureMeet:
		return "Press n then Enter to create a new Meet space."
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
	return m.theme.SubtleText.Render(value)
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > width-1 {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
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

func centerText(value string, width int) string {
	pad := max(0, (width-lipgloss.Width(value))/2)
	return strings.Repeat(" ", pad) + value
}

func rightAlign(value string, width int) string {
	pad := width - lipgloss.Width(value)
	if pad <= 0 {
		return value
	}
	return strings.Repeat(" ", pad) + value
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
