package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) calendarAgendaRows(width int) (string, []string, int, int) {
	title := fmt.Sprintf(" [1]-%s (%d) ", truncate(m.selectedCalendar().Summary, 20), len(m.events))
	today := startOfDay(time.Now())
	lastLabel := ""
	lines := []string{}
	selStart, selEnd := -1, -1
	for i, event := range m.events {
		day := startOfDay(event.Start)
		label := relativeDayLabel(day, today)
		if label != lastLabel {
			header := " " + label
			if sameDay(day, today) {
				header = m.accent(header)
			} else {
				header = m.subtle(header)
			}
			lines = append(lines, header)
			lastLabel = label
		}
		if i == m.selected[FeatureCalendar] {
			selStart = len(lines)
		}
		lines = append(lines, m.calendarAgendaRow(event, width, event.Start.Before(today)))
		if i == m.selected[FeatureCalendar] {
			selEnd = len(lines) - 1
		}
	}
	return title, lines, selStart, selEnd
}

// calendarViewPane gives Calendar a browse-first layout: the month grid owns
// the screen by default, and Enter swaps to an event detail view.
func (m Model) calendarViewPane(width, height int) string {
	hBorder := m.theme.Active.GetHorizontalBorderSize()
	vBorder := m.theme.Active.GetVerticalBorderSize()
	actionVBorder := m.theme.Input.GetVerticalBorderSize()
	statusH := 1
	contentW := max(20, width-hBorder)

	var pane string
	if m.focusedPane == paneDetail {
		contentH := max(5, height-statusH-vBorder)
		style := m.theme.Active
		pane = paneWithTitle(style, m.title(" [2]-"+m.detailTitle()+" "), m.detail.View(), contentW, contentH)
	} else if m.focusedPane == paneAction {
		actionContentH := max(3, min(8, strings.Count(m.input.Value(), "\n")+3))
		calendarContentH := max(8, height-statusH-vBorder-actionContentH-actionVBorder)
		calendar := m.renderCalendarBrowsePane(contentW, calendarContentH)
		action := m.renderAction(contentW, actionContentH)
		pane = lipgloss.JoinVertical(lipgloss.Left, calendar, action)
	} else {
		contentH := max(8, height-statusH-vBorder)
		pane = m.renderCalendarBrowsePane(contentW, contentH)
	}
	status := m.renderStatus(width)
	return m.theme.Root.Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left, pane, status))
}

func (m Model) renderCalendarBrowsePane(width, height int) string {
	if m.calendarView == calViewAgenda {
		return m.renderList(width, height)
	}
	return m.renderCalendarMonthPane(width, height)
}

func (m Model) calendarDetail() string {
	if m.calendarView == calViewMonth && m.focusedPane != paneDetail {
		return m.calendarDayDetail()
	}
	event := m.selectedEvent()
	if event.ID == "" {
		return centerText("No events. Press c to create one, v for the month view.", m.detail.Width)
	}
	lines := []string{
		"When:      " + m.formatEventWhen(event),
		"Location:  " + fallback(event.Location, "-"),
		"Meet:      " + fallback(event.HangoutLink, "-"),
		"RSVP:      " + m.formatRSVP(event.RSVP),
		"Calendar:  " + fallback(m.calendarName(event.CalendarID), m.selectedCalendar().Summary),
	}
	if feedback := m.calendarActivityFeedbackLine(event); feedback != "" {
		lines = append(lines, feedback)
	}
	if event.Recurring {
		lines = append(lines, "Repeats:   "+m.subtle(m.icon("↻ ", "")+"recurring event"))
	}
	lines = append(lines,
		"Attendees: "+fallback(strings.Join(event.Attendees, ", "), "-"),
		"",
		"─── Description ───",
		"",
		fallback(event.Description, "(no description)"),
		"",
		m.subtle("[Y]es [N]o [M]aybe · c new · E edit · d delete · > move · v month · ]/[ calendar"),
	)
	return displayText(strings.Join(wrapDetailLines(lines, m.detailTextWidth()), "\n"))
}

// calendarDayDetail lists the events on the month grid's focused day.
func (m Model) calendarDayDetail() string {
	day := m.calendarCursorOrToday()
	lines := []string{m.accent(day.Format("Monday, 02 January 2006")), ""}
	dayEvents := eventsOnDay(m.monthEvents, day)
	switch {
	case m.loading:
		lines = append(lines, m.subtle("Loading…"))
	case len(dayEvents) == 0:
		lines = append(lines, m.subtle("No events on this day."))
	default:
		selected := clamp(m.calendarDayEventCursor, len(dayEvents))
		for i, e := range dayEvents {
			when := e.Start.Format("15:04") + "–" + e.End.Format("15:04")
			if e.AllDay {
				when = "all-day"
			}
			summary := e.Summary
			if e.Recurring {
				summary += " " + m.icon("↻", "(r)")
			}
			marker := "  "
			if i == selected {
				marker = m.icon("›", ">") + " "
			}
			lines = append(lines, marker+m.rsvpDot(e.RSVP, false)+" "+m.subtle(fmt.Sprintf("%-13s", when))+summary)
			if e.Location != "" {
				lines = append(lines, m.subtle("                  "+e.Location))
			}
		}
	}
	lines = append(lines, "", m.subtle("J/K choose event · Enter open · c new event · {/} month · t today"))
	return displayText(strings.Join(wrapDetailLines(lines, m.detailTextWidth()), "\n"))
}

// formatEventWhen renders an event's time span, handling all-day and
// multi-day events distinctly from ordinary timed events.
func (m Model) formatEventWhen(event api.CalendarEvent) string {
	if event.AllDay {
		// Google's all-day end date is exclusive (the morning after).
		last := event.End.AddDate(0, 0, -1)
		if !last.After(event.Start) || sameDay(last, event.Start) {
			return event.Start.Format("Mon, 02 Jan 2006") + "  (all day)"
		}
		return event.Start.Format("Mon, 02 Jan") + " – " + last.Format("Mon, 02 Jan 2006") + "  (all day)"
	}
	if sameDay(event.Start, event.End) {
		return event.Start.Format("Mon, 02 Jan 2006 · 15:04") + " – " + event.End.Format("15:04") +
			"  (" + formatDuration(event.End.Sub(event.Start)) + ")"
	}
	return event.Start.Format("Mon, 02 Jan 2006 · 15:04") + " – " + event.End.Format("Mon, 02 Jan 2006 · 15:04")
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0m"
	}
	h := int(d.Hours())
	mn := int(d.Minutes()) % 60
	switch {
	case h > 0 && mn > 0:
		return fmt.Sprintf("%dh%dm", h, mn)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", mn)
	}
}

func (m Model) formatRSVP(rsvp string) string {
	switch strings.ToLower(rsvp) {
	case "accepted":
		return m.live("● You're attending")
	case "declined":
		return m.danger("● You rejected this event")
	case "tentative":
		return m.warn("● Maybe attending")
	case "", "needsaction":
		return m.subtle("○ Not yet responded")
	default:
		return rsvp
	}
}

func (m Model) calendarActivityFeedbackLine(event api.CalendarEvent) string {
	if event.ID == "" || m.calendarFeedback.eventID != event.ID || m.calendarFeedback.response == "" {
		return ""
	}
	return "Update:    " + m.rsvpFeedbackText(m.calendarFeedback.response)
}

func (m Model) rsvpFeedbackText(response string) string {
	switch strings.ToLower(response) {
	case "accepted":
		return m.live("You're attending")
	case "declined":
		return m.danger("You rejected this event")
	case "tentative":
		return m.warn("Maybe attending")
	default:
		return "RSVP: " + response
	}
}

func rsvpActionLabel(response string) string {
	switch strings.ToLower(response) {
	case "accepted":
		return "attending"
	case "declined":
		return "no"
	case "tentative":
		return "maybe"
	default:
		return response
	}
}

func (m Model) calendarName(id string) string {
	for _, c := range m.calendars {
		if c.ID == id {
			return c.Summary
		}
	}
	return id
}

// relativeDayLabel turns a day into an agenda header, naming the days around
// today so the list reads "Today / Tomorrow / Yesterday" at a glance.
func relativeDayLabel(day, today time.Time) string {
	switch {
	case sameDay(day, today):
		return "Today · " + day.Format("Mon 02 Jan")
	case sameDay(day, today.AddDate(0, 0, 1)):
		return "Tomorrow · " + day.Format("Mon 02 Jan")
	case sameDay(day, today.AddDate(0, 0, -1)):
		return "Yesterday · " + day.Format("Mon 02 Jan")
	default:
		return day.Format("Mon 02 Jan")
	}
}

// calendarAgendaRow renders one agenda line: an RSVP status dot, the start
// time (or "all-day"), and the summary with a ↻ marker for recurring events.
// Past events are dimmed.
func (m Model) calendarAgendaRow(event api.CalendarEvent, width int, past bool) string {
	when := event.Start.Format("15:04")
	if event.AllDay {
		when = "all-day"
	}
	summary := event.Summary
	if event.Recurring {
		summary += " " + m.icon("↻", "(r)")
	}
	avail := max(1, width-m.theme.Pane.GetHorizontalPadding()-12)
	text := fmt.Sprintf(" %-7s  %s", when, truncate(summary, avail))
	if past {
		text = m.subtle(text)
	}
	return m.rsvpDot(event.RSVP, past) + text
}

// rsvpDot returns a one-cell glyph colored by the viewer's RSVP status.
func (m Model) rsvpDot(rsvp string, dim bool) string {
	switch strings.ToLower(rsvp) {
	case "accepted":
		if dim {
			return m.subtle(m.icon("●", "*"))
		}
		return m.live(m.icon("●", "*"))
	case "declined":
		if dim {
			return m.subtle(m.icon("●", "x"))
		}
		return m.danger(m.icon("●", "x"))
	case "tentative":
		if dim {
			return m.subtle(m.icon("●", "?"))
		}
		return m.warn(m.icon("●", "?"))
	case "needsaction":
		return m.subtle(m.icon("○", "-"))
	default:
		return " "
	}
}

// renderCalendarMonthPane draws the full-screen month grid.
func (m Model) renderCalendarMonthPane(width, height int) string {
	cursor := m.calendarCursorOrToday()
	innerW := max(7, width-m.theme.Pane.GetHorizontalPadding())
	gridH := max(6, height-3)
	lines := m.calendarMonthGrid(innerW, gridH)
	lines = append(lines, "")
	if m.loading {
		lines = append(lines, m.subtle(" loading "+cursor.Format("Jan 2006")+"…"))
	} else {
		lines = append(lines, m.subtle(fmt.Sprintf(" %d events this month", len(m.monthEvents))))
	}
	lines = append(lines, m.subtle(" h/l day · j/k week · J/K activity · Enter detail · c new"))
	title := fmt.Sprintf(" [1]-%s · %s ", truncate(m.selectedCalendar().Summary, 14), cursor.Format("January 2006"))
	style := m.theme.Pane
	if m.focusedPane == paneList {
		style = m.theme.Active
	}
	body := fitLines(lines, innerW, max(1, height))
	return paneWithTitle(style, m.title(title), body, width, height)
}

// calendarMonthGrid builds the Monday-first weekday header and week rows for
// the cursor's month. Each day cell owns multiple terminal rows so activity
// labels can sit inside the date, closer to a full calendar app than an agenda
// list.
func (m Model) calendarMonthGrid(innerW, maxH int) []string {
	cursor := m.calendarCursorOrToday()
	today := startOfDay(time.Now())
	first := monthStart(cursor)
	total := daysInMonth(first)
	lead := (int(first.Weekday()) + 6) % 7 // Monday-first column of the 1st

	cellW := innerW / 7
	if cellW < 7 {
		cellW = 7
	}
	pad := strings.Repeat(" ", max(0, (innerW-cellW*7)/2))

	header := strings.Builder{}
	header.WriteString(pad)
	for _, wd := range []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"} {
		header.WriteString(m.subtle(centerCell(wd, cellW)))
	}
	lines := []string{header.String()}

	var weeks [][]time.Time
	week := make([]time.Time, 7)
	col := lead
	for d := 1; d <= total; d++ {
		date := time.Date(first.Year(), first.Month(), d, 0, 0, 0, 0, time.Local)
		week[col] = date
		col++
		if col == 7 {
			weeks = append(weeks, week)
			week = make([]time.Time, 7)
			col = 0
		}
	}
	for col != 0 {
		weeks = append(weeks, week)
		break
	}
	if len(weeks) == 0 {
		weeks = append(weeks, make([]time.Time, 7))
	}

	cellH := max(2, (maxH-1)/len(weeks))
	cellH = min(cellH, 8)
	for _, week := range weeks {
		cells := make([][]string, 7)
		for i, date := range week {
			cells[i] = m.calendarDayCellLines(date, cellW, cellH, cursor, today)
		}
		for line := 0; line < cellH; line++ {
			row := strings.Builder{}
			row.WriteString(pad)
			for col := 0; col < 7; col++ {
				row.WriteString(cells[col][line])
			}
			lines = append(lines, row.String())
		}
	}
	return lines
}

func (m Model) calendarDayCellLines(date time.Time, cellW, cellH int, cursor, today time.Time) []string {
	blank := padCell("", cellW)
	lines := make([]string, 0, cellH)
	if date.IsZero() {
		for len(lines) < cellH {
			lines = append(lines, blank)
		}
		return lines
	}

	dayLabel := fmt.Sprintf(" %2d", date.Day())
	if sameDay(date, cursor) {
		dayLabel = ">" + fmt.Sprintf("%2d", date.Day())
	} else if sameDay(date, today) {
		dayLabel = "*" + fmt.Sprintf("%2d", date.Day())
	}
	header := padCell(dayLabel, cellW)
	switch {
	case sameDay(date, cursor):
		header = renderStylePreserve(m.theme.Selected.Width(cellW), truncate(header, cellW))
	case sameDay(date, today):
		header = m.calendarTodayStyle().Width(cellW).Render(truncate(header, cellW))
	case m.dayHasEvents(date):
		header = m.accent(header)
	}
	lines = append(lines, header)

	events := eventsOnDay(m.monthEvents, date)
	eventRows := max(0, cellH-1)
	limit := min(len(events), eventRows)
	if len(events) > eventRows && eventRows > 0 {
		limit = eventRows - 1
	}
	selectedEventCursor := clamp(m.calendarDayEventCursor, len(events))
	for i := 0; i < limit; i++ {
		lines = append(lines, m.calendarCellEventLine(events[i], cellW, sameDay(date, cursor) && i == selectedEventCursor))
	}
	if len(events) > eventRows && eventRows > 0 {
		lines = append(lines, m.subtle(padCell(fmt.Sprintf(" +%d more", len(events)-limit), cellW)))
	}
	for len(lines) < cellH {
		lines = append(lines, blank)
	}
	return lines
}

func (m Model) calendarCellEventLine(event api.CalendarEvent, cellW int, selected bool) string {
	when := event.Start.Format("15:04")
	if event.AllDay {
		when = "all-day"
	}
	summary := strings.TrimSpace(event.Summary)
	if summary == "" {
		summary = "(no title)"
	}
	if event.Recurring {
		summary += " " + m.icon("↻", "(r)")
	}
	marker := m.icon("•", "*")
	if selected {
		marker = m.icon("›", ">")
	}
	text := padCell(" "+marker+m.rsvpDot(event.RSVP, false)+" "+when+" "+summary, cellW)
	if selected {
		return renderStylePreserve(m.theme.Selected.Width(cellW), truncate(text, cellW))
	}
	if event.Start.Before(startOfDay(time.Now())) {
		return m.subtle(text)
	}
	return text
}

// calendarDayCell formats and styles a single day cell of the month grid.
func (m Model) calendarDayCell(day, cellW int, isToday, isCursor, hasEvents bool) string {
	num := fmt.Sprintf("%d", day)
	var text string
	if cellW >= 4 {
		marker := " "
		if hasEvents {
			marker = m.icon("•", "*")
		}
		text = fmt.Sprintf("%3s%s", num, marker)
		for lipgloss.Width(text) < cellW {
			text += " "
		}
	} else {
		text = centerCell(num, cellW)
	}
	switch {
	case isCursor:
		return renderStylePreserve(m.theme.Selected, text)
	case isToday:
		return m.calendarTodayStyle().Render(text)
	case hasEvents:
		return m.accent(text)
	default:
		return text
	}
}

func (m Model) calendarTodayStyle() lipgloss.Style {
	if m.cfg.NoColor {
		return lipgloss.NewStyle().Underline(true).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Info)).Bold(true).Underline(true)
}

func (m Model) dayHasEvents(day time.Time) bool {
	for _, e := range m.monthEvents {
		if sameDay(e.Start, day) {
			return true
		}
	}
	return false
}

// centerCell pads s with spaces so it occupies exactly width display cells.
func centerCell(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	left := (width - w) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", width-w-left)
}
