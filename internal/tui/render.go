package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/fabhiantomaoludyo/gws-tui/internal/api"
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
	detail := m.theme.Active.Width(rightW).Height(detailContentH).Render(m.title(" "+m.detailTitle()+" ") + "\n" + m.detail.View())
	action := m.renderAction(rightW, actionContentH)
	right := lipgloss.JoinVertical(lipgloss.Left, detail, action)
	row := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	status := m.renderStatus(width)
	return m.theme.Root.Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left, row, status))
}

func (m Model) authView(width, height int) string {
	lines := []string{
		m.title("gws · sign in"),
		"",
		"Signing you into Google Workspace...",
		"",
	}
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
	box := m.theme.Active.Width(min(74, width-4)).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) renderList(width, height int) string {
	var title string
	var lines []string
	switch m.feature {
	case FeatureChat:
		title = fmt.Sprintf(" Spaces (%d) ", len(m.spaces))
		for i, space := range m.spaces {
			marker := "  "
			if space.Live {
				marker = m.live(m.icon("●", "*")) + " "
			} else if space.Unread {
				marker = m.accent(m.icon("●", "*")) + " "
			}
			name := truncate(m.spaceLabel(space), width-8)
			if i == m.selected[FeatureChat] {
				lines = append(lines, m.accent(m.icon("▎", "|")+" ")+marker+name)
			} else {
				lines = append(lines, "  "+marker+name)
			}
		}
	case FeatureMail:
		title = fmt.Sprintf(" Inbox (%d) ", len(m.mailThreads))
		for i, thread := range m.mailThreads {
			marker := "  "
			if thread.Unread {
				marker = m.accent(m.icon("●", "*")) + " "
			}
			if thread.Starred {
				marker = m.warn(m.icon("★", "*")) + " "
			}
			prefix := "  "
			if i == m.selected[FeatureMail] {
				prefix = m.accent(m.icon("▎", "|") + " ")
			}
			lines = append(lines, prefix+marker+truncate(thread.Sender, width-8))
			subject := truncate(thread.Subject+"  "+relative(thread.Date), width-6)
			lines = append(lines, "    "+subject)
			lines = append(lines, m.subtle("    "+strings.Repeat("─", max(1, width-8))))
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
		title = fmt.Sprintf(" This week (%d) ", len(m.events))
		lastDay := ""
		for i, event := range sortedEvents(m.events) {
			day := event.Start.Format("Mon 02 Jan")
			if day != lastDay {
				lines = append(lines, m.subtle("  "+day))
				lastDay = day
			}
			prefix := "  "
			if i == m.selected[FeatureCalendar] {
				prefix = m.accent(m.icon("▎", "|") + " ")
			}
			lines = append(lines, prefix+event.Start.Format("15:04")+"  "+truncate(event.Summary, width-12))
		}
	case FeatureMeet:
		title = fmt.Sprintf(" Meet spaces (%d) ", len(m.meetSpaces))
		for i, space := range m.meetSpaces {
			prefix := "  "
			if i == m.selected[FeatureMeet] {
				prefix = m.accent(m.icon("▎", "|") + " ")
			}
			status := ""
			if space.Active {
				status = " " + m.live("active")
			}
			lines = append(lines, prefix+truncate(lastSegment(space.Name), width-14)+status)
		}
	}
	if len(lines) == 0 {
		lines = []string{"", "  No items yet."}
	}
	innerW := max(1, width-m.theme.Pane.GetHorizontalPadding())
	body := m.title(title) + "\n" + fitLines(lines, innerW, max(1, height-1))
	style := m.theme.Pane
	if !m.actionFocus {
		style = m.theme.Active
	}
	return style.Width(width).Height(height).Render(body)
}

func (m Model) renderAction(width, height int) string {
	title := " " + m.actionTitle() + " "
	content := ""
	if m.actionFocus {
		content = m.input.View()
	} else {
		content = m.subtle(m.actionPlaceholder())
	}
	style := m.theme.Input.Width(width).Height(height)
	if m.actionFocus {
		style = style.BorderForeground(lipgloss.Color(m.theme.Accent))
	}
	return style.Render(m.title(title) + "\n" + content)
}

func (m Model) renderStatus(width int) string {
	tabs := make([]string, 0, len(featureOrder))
	for i, feature := range featureOrder {
		label := fmt.Sprintf("%d %s", i+1, strings.Title(string(feature)))
		if feature == m.feature {
			tabs = append(tabs, m.theme.ActiveTab.Render(label))
		} else {
			tabs = append(tabs, m.theme.Tab.Render(label))
		}
	}
	left := strings.Join(tabs, "")
	liveCount := 0
	for _, space := range m.spaces {
		if space.Live {
			liveCount++
		}
	}
	right := fmt.Sprintf("%s %d live   j/k move  Enter open  / search  q quit", m.live(m.icon("●", "*")), liveCount)
	if m.loading {
		right = m.spinner.View() + " loading   " + right
	}
	if m.toast != "" {
		right = m.toast + "   " + right
	}
	if m.err != "" {
		right = "error: " + m.err + "   x dismiss"
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		right = truncate(right, max(10, width-lipgloss.Width(left)-2))
		gap = width - lipgloss.Width(left) - lipgloss.Width(right)
	}
	return m.theme.Status.Width(width).Render(left + strings.Repeat(" ", max(1, gap)) + right)
}

func (m Model) renderModal(width int) string {
	if m.modal == nil {
		return ""
	}
	lines := []string{m.title(" " + m.modal.title + " "), ""}
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
	return m.theme.Modal.Width(min(width, 88)).Render(strings.Join(lines, "\n"))
}

func (m Model) detailTitle() string {
	switch m.feature {
	case FeatureChat:
		return m.spaceLabel(m.selectedSpace())
	case FeatureMail:
		return fallback(m.selectedMail().Subject, "Mail")
	case FeatureCalendar:
		return fallback(m.selectedEvent().Summary, "Calendar")
	case FeatureMeet:
		return fallback(lastSegment(m.selectedMeet().Name), "Meet")
	default:
		return "gws"
	}
}

func (m Model) detailContent() string {
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

func (m Model) chatDetail() string {
	if m.chatLoading && m.chatLoadSpace == m.selectedSpace().Name {
		return centerText("Loading messages...", m.detail.Width)
	}
	if len(m.chatMessages) == 0 {
		return centerText("No messages in this space yet. Press i to write.", m.detail.Width)
	}
	var lines []string
	lastDay := ""
	for _, msg := range m.chatMessages {
		day := msg.CreateTime.Format("Mon, 02 Jan 2006")
		if day != lastDay {
			lines = append(lines, m.subtle("─── "+friendlyDay(msg.CreateTime)+" ───"))
			lastDay = day
		}
		name := m.senderLabel(msg)
		if msg.SenderID == "users/me" || name == "You" {
			name = m.accent(name)
		} else {
			name = m.senderColor(msg.SenderID, name)
		}
		status := ""
		if msg.Pending {
			status = " " + m.subtle("(sending)")
		}
		lines = append(lines, fmt.Sprintf("%s    %s%s", name, msg.CreateTime.Format("15:04"), status))
		prefix := "  "
		if msg.ParentID != "" {
			prefix = "  " + m.icon("↪", ">") + " "
		}
		for _, line := range strings.Split(msg.Text, "\n") {
			if strings.HasPrefix(line, "```") {
				lines = append(lines, m.theme.Code.Width(max(12, m.detail.Width-4)).Render(line))
				continue
			}
			lines = append(lines, prefix+line)
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m Model) mailDetail() string {
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
		return centerText("No Meet spaces. Press n to create.", m.detail.Width)
	}
	recording := "off"
	if space.Recording {
		recording = "on"
	}
	lines := []string{
		"Link:       " + space.MeetingURI,
		"People:     " + fmt.Sprintf("%d active", space.ActiveParticipants),
		"Created:    " + space.Created.Format("02 Jan 2006"),
		"Type:       " + fallback(space.Type, "open"),
		"Recording:  " + recording,
		"",
		"─── Actions ───",
		"",
		m.subtle("[J]oin  [C]opy link  [E]nd  n new space"),
	}
	return strings.Join(lines, "\n")
}

func (m Model) actionTitle() string {
	switch m.feature {
	case FeatureChat:
		return "message · Enter send · Shift+Enter newline"
	case FeatureMail:
		return "quick reply · c compose · R reply"
	case FeatureCalendar:
		return "quick add · Enter create"
	case FeatureMeet:
		return "create space · Enter create"
	default:
		return "action"
	}
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
		return "Press n, type a space name, Enter to create."
	default:
		return ""
	}
}

func (m Model) title(value string) string {
	return m.theme.Title.Render(value)
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

func (m Model) senderColor(key, value string) string {
	if m.cfg.NoColor {
		return value
	}
	palette := []string{"#06B6D4", "#22C55E", "#F59E0B", "#EC4899", "#8B5CF6", "#14B8A6"}
	hash := 0
	for _, r := range key {
		hash = (hash*33 + int(r)) % len(palette)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(palette[hash])).Render(value)
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
