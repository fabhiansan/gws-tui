package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fabhiansan/gws-tui/internal/api"
)

// burstWindow is the maximum gap between two messages from the same sender
// before they're treated as separate bursts. Matches Google Chat's grouping —
// long enough that quick back-and-forth coalesces, short enough that a midday
// follow-up doesn't get glued onto a morning message.
const burstWindow = 5 * time.Minute

// minBubbleWidth keeps narrow terminals from rendering a useless 4-cell box.
// Below this threshold chatDetail falls back to plain inline rendering.
const minBubbleWidth = 24

// messageBurst is a run of consecutive messages from the same sender within
// burstWindow. The chat detail pane renders one bubble per burst rather than
// per message — that's the trick that makes "bubble everything" cheaper than
// the previous inline rendering for chatty conversations.
type messageBurst struct {
	senderID   string
	senderName string
	isSelf     bool
	messages   []api.ChatMessage
}

func (m *Model) groupBursts(messages []api.ChatMessage) []messageBurst {
	var bursts []messageBurst
	for _, msg := range messages {
		name := displayText(m.senderLabel(msg))
		isSelf := m.isSelfMessage(msg, name)
		key := msg.SenderID
		if isSelf {
			key = "__self__"
		}
		if n := len(bursts); n > 0 {
			last := &bursts[n-1]
			lastMsg := last.messages[len(last.messages)-1]
			lastKey := lastMsg.SenderID
			if last.isSelf {
				lastKey = "__self__"
			}
			if lastKey == key && msg.CreateTime.Sub(lastMsg.CreateTime) <= burstWindow {
				last.messages = append(last.messages, msg)
				continue
			}
		}
		bursts = append(bursts, messageBurst{
			senderID:   msg.SenderID,
			senderName: name,
			isSelf:     isSelf,
			messages:   []api.ChatMessage{msg},
		})
	}
	return bursts
}

// burstHasText reports whether any message in the burst has non-empty text.
// Card-only messages (Braga Bot etc.) have empty Text and only Cards; we
// suppress the bubble for those and let the card renderer handle the visual.
func burstHasText(burst messageBurst) bool {
	for _, msg := range burst.messages {
		if strings.TrimSpace(msg.Text) != "" {
			return true
		}
	}
	return false
}

// renderBubble produces the lines for one burst, ready to splice into
// chatDetail's output. Returns nil when the burst has no text content (the
// caller still renders attachments/cards in that case).
func (m *Model) renderBubble(burst messageBurst, textWidth int) []string {
	if !burstHasText(burst) {
		return nil
	}
	if textWidth < minBubbleWidth {
		return m.renderBubblePlain(burst, textWidth)
	}

	maxBubbleW := textWidth
	if maxBubbleW < minBubbleWidth {
		maxBubbleW = minBubbleWidth
	}
	// Box chrome = 2 border cells + 2 padding cells.
	innerCap := maxBubbleW - 4
	if innerCap < 10 {
		innerCap = 10
	}

	contentLines := m.bubbleContentLines(burst, innerCap)
	if len(contentLines) == 0 {
		return nil
	}

	contentW := 0
	for _, line := range contentLines {
		if w := lipgloss.Width(line); w > contentW {
			contentW = w
		}
	}

	label := m.bubbleHeaderLabel(burst)
	labelW := lipgloss.Width(label)

	bubbleW := contentW + 4
	if labelW+4 > bubbleW {
		bubbleW = labelW + 4
	}
	if bubbleW > maxBubbleW {
		bubbleW = maxBubbleW
	}
	if bubbleW < minBubbleWidth {
		bubbleW = minBubbleWidth
	}

	style := m.bubbleStyle(burst).Width(bubbleW)
	content := strings.Join(contentLines, "\n")
	box := style.Render(content)
	box = injectBubbleHeader(box, label, m.bubbleBorderColor(burst))

	boxLines := strings.Split(box, "\n")
	if burst.isSelf {
		for i, line := range boxLines {
			boxLines[i] = rightAlign(line, textWidth)
		}
	}
	return boxLines
}

// renderBubblePlain is the narrow-terminal fallback: no box, just the
// original inline format. Keeps the TUI usable when someone shrinks the
// window below ~24 columns.
func (m *Model) renderBubblePlain(burst messageBurst, textWidth int) []string {
	var lines []string
	for i, msg := range burst.messages {
		if i == 0 || burst.messages[i-1].CreateTime.Day() != msg.CreateTime.Day() {
			name := burst.senderName
			if burst.isSelf {
				name = m.accent(name)
			} else {
				name = m.senderColor(burst.senderID, name)
			}
			header := name + "    " + msg.CreateTime.Format("15:04")
			if burst.isSelf {
				header = rightAlign(header, textWidth)
			}
			lines = append(lines, header)
		}
		prefix := "  "
		if msg.ParentID != "" {
			prefix = "  " + m.icon("↪", ">") + " "
		}
		prefixW := lipgloss.Width(prefix)
		wrapW := max(8, textWidth-prefixW)
		for _, line := range strings.Split(msg.Text, "\n") {
			for _, sub := range strings.Split(ansi.Wrap(displayText(line), wrapW, " -"), "\n") {
				out := prefix + sub
				if burst.isSelf {
					out = rightAlign(out, textWidth)
				}
				lines = append(lines, out)
			}
		}
	}
	return lines
}

func (m *Model) bubbleContentLines(burst messageBurst, innerW int) []string {
	var lines []string
	for i, msg := range burst.messages {
		// Between messages in a burst, show a faint timestamp marker so
		// you can still see when each message in the run was sent. Cheap
		// (one line) and easier to scan than a full divider.
		if i > 0 {
			marker := m.subtle(msg.CreateTime.Format("15:04"))
			lines = append(lines, marker)
		}
		prefix := ""
		continuation := ""
		if msg.ParentID != "" {
			prefix = m.icon("↪", ">") + " "
			continuation = strings.Repeat(" ", lipgloss.Width(prefix))
		}
		wrapW := max(8, innerW-lipgloss.Width(prefix))
		first := true
		for _, line := range strings.Split(msg.Text, "\n") {
			line = displayText(line)
			if line == "" {
				lines = append(lines, "")
				first = false
				continue
			}
			for _, sub := range strings.Split(ansi.Wrap(line, wrapW, " -"), "\n") {
				if first {
					lines = append(lines, prefix+sub)
					first = false
				} else {
					lines = append(lines, continuation+sub)
				}
			}
		}
		if msg.Pending {
			lines = append(lines, m.subtle("(sending…)"))
		}
	}
	return lines
}

func (m *Model) bubbleHeaderLabel(burst messageBurst) string {
	name := burst.senderName
	if burst.isSelf {
		name = m.accent(name)
	} else {
		// senderColor populates m.senderColorIdx, which bubbleBorderColor
		// then reads — call this first so the border matches the name.
		name = m.senderColor(burst.senderID, name)
	}
	timestamp := m.subtle(burst.messages[0].CreateTime.Format("15:04"))
	return " " + name + "  " + timestamp + " "
}

func (m Model) bubbleStyle(burst messageBurst) lipgloss.Style {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)
	if m.cfg.NoColor {
		return style
	}
	color := m.bubbleBorderColor(burst)
	return style.BorderForeground(color).Foreground(lipgloss.Color(m.theme.Fg))
}

func (m Model) bubbleBorderColor(burst messageBurst) lipgloss.Color {
	if m.cfg.NoColor {
		return lipgloss.Color(m.theme.Border)
	}
	if burst.isSelf {
		return lipgloss.Color(m.theme.Accent)
	}
	idx, ok := m.senderColorIdx[burst.senderID]
	if !ok {
		return lipgloss.Color(m.theme.Border)
	}
	return lipgloss.Color(senderPalette[idx%len(senderPalette)])
}

// injectBubbleHeader rewrites the top border line of a rendered box to embed
// the sender label, left-aligned after the rounded corner. lipgloss doesn't
// expose this directly, but a rendered box always starts with "╭<border…>╮"
// followed by "\n", so we can splice into the plain bytes safely.
func injectBubbleHeader(box, label string, borderColor lipgloss.Color) string {
	nl := strings.Index(box, "\n")
	if nl == -1 {
		return box
	}
	topLine := box[:nl]
	rest := box[nl:]

	plainTop := ansi.Strip(topLine)
	runes := []rune(plainTop)
	if len(runes) < 6 {
		return box
	}
	leftCorner := string(runes[0])
	rightCorner := string(runes[len(runes)-1])
	borderChar := string(runes[1])
	innerW := len(runes) - 2

	labelW := lipgloss.Width(label)
	leftPad := 1
	rightPad := innerW - leftPad - labelW
	if rightPad < 1 {
		// Label too long for the bubble width — truncate so we always
		// leave at least one border cell on the right.
		labelW = innerW - leftPad - 1
		if labelW < 1 {
			return box
		}
		label = ansi.Truncate(label, labelW, "…")
		rightPad = 1
	}

	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	left := borderStyle.Render(leftCorner + strings.Repeat(borderChar, leftPad))
	right := borderStyle.Render(strings.Repeat(borderChar, rightPad) + rightCorner)
	return left + label + right + rest
}
