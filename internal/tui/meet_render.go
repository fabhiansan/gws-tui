package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/fabhiansan/gws-tui/internal/api"
)

// meetView gives Meet a single-pane, browse-first layout: every conference
// lives in one scrollable list grouped by date, and the selected one expands
// inline to show its full detail instead of using a separate side pane.
func (m Model) meetView(width, height int) string {
	hBorder := m.theme.Active.GetHorizontalBorderSize()
	vBorder := m.theme.Active.GetVerticalBorderSize()
	statusH := 1
	contentW := max(20, width-hBorder)
	contentH := max(5, height-statusH-vBorder)
	pane := m.renderList(contentW, contentH)
	status := m.renderStatus(width)
	return m.theme.Root.Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left, pane, status))
}

// meetListRows builds the single-pane Meet body: conferences sorted newest
// first, split under date headers, each shown as a one-line summary. The
// selected conference expands inline with its full detail.
func (m Model) meetListRows(width int) (string, []string, int, int) {
	title := fmt.Sprintf(" [1]-Meet (%d) ", len(m.meetSpaces))
	lines := []string{}
	selStart, selEnd := -1, -1
	if len(m.meetSpaces) == 0 {
		return title, lines, selStart, selEnd
	}
	selected := clamp(m.selected[FeatureMeet], len(m.meetSpaces))
	detailW := max(10, width-6)
	var lastDay time.Time
	for i, space := range m.meetSpaces {
		day := startOfDay(space.StartTime)
		if i == 0 || !day.Equal(lastDay) {
			if i != 0 {
				lines = append(lines, "")
			}
			lines = append(lines, m.accent(meetDayLabel(space.StartTime)))
			lastDay = day
		}

		marker := "○"
		if space.IsActive() {
			marker = m.live("●")
		}
		row := " " + marker + " " + space.DisplayTitle()
		if meta := meetRowMeta(space); meta != "" {
			row += "  " + m.subtle("· "+meta)
		}

		if i == selected {
			selStart = len(lines)
		}
		lines = append(lines, row)
		if i == selected {
			selEnd = len(lines) - 1
			for _, dl := range wrapDetailLines(m.meetExpandedLines(space), detailW) {
				if strings.TrimSpace(dl) == "" {
					lines = append(lines, "")
				} else {
					lines = append(lines, "   "+dl)
				}
			}
		}
	}
	return title, lines, selStart, selEnd
}

// meetExpandedLines is the detail body shown under the selected conference. It
// deliberately omits the raw resource IDs and leans on the calendar-derived
// title, the people who joined, and the invited attendee emails.
func (m Model) meetExpandedLines(space api.MeetSpace) []string {
	lines := []string{"Status:  " + meetStatusText(space)}
	if url := space.JoinURL(); url != "" {
		lines = append(lines, "Link:    "+strings.TrimPrefix(url, "https://"))
	}
	if access := space.AccessType(); access != "" {
		lines = append(lines, "Access:  "+access)
	}

	if len(space.Participants) > 0 {
		lines = append(lines, "", m.accent(fmt.Sprintf("Joined (%d)", len(space.Participants))))
		for _, p := range space.Participants {
			name := fallback(p.DisplayName, fallback(lastSegment(p.User), lastSegment(p.Name)))
			entry := "  • " + name
			if when := meetParticipantWhen(p); when != "" {
				entry += "  " + m.subtle(when)
			}
			lines = append(lines, entry)
		}
	}
	if len(space.InvitedEmails) > 0 {
		lines = append(lines, "", m.accent(fmt.Sprintf("Invited (%d)", len(space.InvitedEmails))))
		for _, email := range space.InvitedEmails {
			lines = append(lines, "  • "+email)
		}
	}
	if len(space.Recordings) > 0 {
		lines = append(lines, "", m.accent("Recordings"))
		for _, r := range space.Recordings {
			lines = append(lines, "  • "+strings.TrimSpace(fallback(r.File, r.Name)+" "+m.subtle(r.State)))
		}
	}
	if len(space.Transcripts) > 0 {
		lines = append(lines, "", m.accent("Transcripts"))
		for _, t := range space.Transcripts {
			lines = append(lines, "  • "+strings.TrimSpace(fallback(t.File, t.Name)+" "+m.subtle(t.State)))
		}
	}
	lines = append(lines, "", m.subtle("[J]oin · [C]opy link · [E]nd · n new space"))
	return lines
}

// meetDetail keeps the side-pane detail working for detailContent(); the Meet
// feature renders single-pane, so this is only a fallback rendering path.
func (m Model) meetDetail() string {
	space := m.selectedMeet()
	if space.Name == "" {
		return centerText("No Meet spaces yet. Press n to create one.", m.detail.Width)
	}
	lines := append([]string{m.accent(space.DisplayTitle()), ""}, m.meetExpandedLines(space)...)
	return displayText(strings.Join(wrapDetailLines(lines, m.detailTextWidth()), "\n"))
}

// meetDayLabel formats the date header that groups conferences in the list.
func meetDayLabel(t time.Time) string {
	if t.IsZero() {
		return "No date"
	}
	return t.Format("Monday, 02 January 2006")
}

// meetRowMeta is the dimmed suffix on a conference row: time range, headcount
// and a recording marker, joined with middots.
func meetRowMeta(space api.MeetSpace) string {
	var parts []string
	if when := meetTimeRange(space); when != "" {
		parts = append(parts, when)
	}
	if n := len(space.Participants); n > 0 {
		parts = append(parts, fmt.Sprintf("%d joined", n))
	}
	if space.Recording || len(space.Recordings) > 0 {
		parts = append(parts, "rec")
	}
	return strings.Join(parts, " · ")
}

// meetTimeRange renders a conference's clock span, e.g. "09:00–10:00".
func meetTimeRange(space api.MeetSpace) string {
	if space.StartTime.IsZero() {
		return ""
	}
	start := space.StartTime.Format("15:04")
	if space.EndTime.IsZero() {
		return start
	}
	return start + "–" + space.EndTime.Format("15:04")
}

// meetStatusText describes whether a conference is live, finished, or never
// hosted a call.
func meetStatusText(space api.MeetSpace) string {
	if space.IsActive() {
		if space.ActiveParticipants > 0 {
			return fmt.Sprintf("active · %d in call", space.ActiveParticipants)
		}
		return "active"
	}
	if !space.EndTime.IsZero() {
		return "ended " + space.EndTime.Format("02 Jan 2006 15:04")
	}
	return "no conference"
}

// meetParticipantWhen renders a participant's join/leave window.
func meetParticipantWhen(p api.MeetParticipant) string {
	switch {
	case !p.JoinTime.IsZero() && !p.LeaveTime.IsZero():
		return p.JoinTime.Format("15:04") + "–" + p.LeaveTime.Format("15:04")
	case !p.JoinTime.IsZero():
		return "joined " + p.JoinTime.Format("15:04")
	default:
		return ""
	}
}
