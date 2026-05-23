package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) chatListRows(width int) (string, []string, int, int) {
	spaces := m.visibleSpaces()
	title := ""
	if m.spaceFilterActive || strings.TrimSpace(m.spaceFilter) != "" {
		title = fmt.Sprintf(" [1]-Spaces (%d/%d) /%s ", len(spaces), len(m.spaces), m.spaceFilter)
	} else {
		title = fmt.Sprintf(" [1]-Spaces (%d) ", len(spaces))
	}
	lines := []string{}
	selStart, selEnd := -1, -1
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
	return title, lines, selStart, selEnd
}

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
	return lipgloss.NewStyle().Foreground(lipgloss.Color(m.senderColorValue(key))).Render(value)
}

func (m *Model) senderColorValue(key string) string {
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
	return senderPalette[idx]
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
