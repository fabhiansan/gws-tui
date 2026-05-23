package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) moveCalendar(delta int) (Model, tea.Cmd) {
	if len(m.calendars) == 0 {
		m.toast = "no calendars"
		return m, nil
	}
	next := clamp(m.calendarIndex+delta, len(m.calendars))
	if next == m.calendarIndex {
		return m, nil
	}
	m.calendarIndex = next
	m.selected[FeatureCalendar] = 0
	if m.calendarView == calViewMonth {
		// The grid is calendar-scoped, so a calendar switch reloads the month.
		m.monthEvents = nil
		m.calendarMonth = time.Time{}
		return m.loadCalendarMonth(m.calendarCursorOrToday())
	}
	return m.loadSelectedCalendar()
}

func (m *Model) applyEvent(event api.CalendarEvent) {
	if event.ID == "" {
		return
	}
	m.events = upsertEvent(m.events, event)
	// Keep the month grid in sync: add the event when it lands in the loaded
	// month, drop it when an edit moved it out of that month.
	if !m.calendarMonth.IsZero() {
		if sameMonth(event.Start, m.calendarMonth) {
			m.monthEvents = upsertEvent(m.monthEvents, event)
		} else {
			m.monthEvents = removeEventByID(m.monthEvents, event.ID)
		}
		m.clampCalendarDayEventCursor()
	}
}

func (m *Model) removeEvent(id string) {
	if id == "" {
		return
	}
	m.events = removeEventByID(m.events, id)
	m.monthEvents = removeEventByID(m.monthEvents, id)
	m.clampCalendarDayEventCursor()
}

// upsertEvent replaces the event with a matching ID or appends it, returning a
// freshly sorted slice so selection indices stay aligned with the rendering.
func upsertEvent(events []api.CalendarEvent, event api.CalendarEvent) []api.CalendarEvent {
	for i := range events {
		if events[i].ID == event.ID {
			events[i] = event
			return sortedEvents(events)
		}
	}
	return sortedEvents(append(events, event))
}

func removeEventByID(events []api.CalendarEvent, id string) []api.CalendarEvent {
	out := make([]api.CalendarEvent, 0, len(events))
	for _, e := range events {
		if e.ID != id {
			out = append(out, e)
		}
	}
	return out
}

// mergeEvents prefers the local event slice over the remote one for any ID
// that already exists locally — this preserves in-flight RSVP/edits when a
// refetch returns before the server's state has converged.
func mergeEvents(local, remote []api.CalendarEvent) []api.CalendarEvent {
	localByID := make(map[string]api.CalendarEvent, len(local))
	for _, e := range local {
		if e.ID != "" {
			localByID[e.ID] = e
		}
	}
	out := make([]api.CalendarEvent, 0, len(remote))
	seen := make(map[string]bool, len(remote))
	for _, e := range remote {
		seen[e.ID] = true
		if localEvent, ok := localByID[e.ID]; ok {
			out = append(out, localEvent)
		} else {
			out = append(out, e)
		}
	}
	for id, e := range localByID {
		if !seen[id] {
			out = append(out, e)
		}
	}
	return out
}

func (m Model) rsvpSelected(response string) (Model, tea.Cmd) {
	event := m.selectedEvent()
	if event.ID == "" {
		return m, nil
	}
	label := "RSVP updated: " + rsvpActionLabel(response)
	return m, func() tea.Msg {
		updated, err := m.client.RSVPEvent(m.ctx, event.ID, response)
		return eventActionMsg{event: updated, err: err, label: label, rsvpResponse: response}
	}
}

func (m Model) deleteSelectedEvent() (Model, tea.Cmd) {
	event := m.selectedEvent()
	if event.ID == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.DeleteEvent(m.ctx, event.ID)
		return eventActionMsg{event: event, err: err, label: "event deleted", deleted: true}
	}
}

func (m Model) moveSelectedEventToNextCalendar() (Model, tea.Cmd) {
	event := m.selectedEvent()
	if event.ID == "" {
		return m, nil
	}
	if len(m.calendars) < 2 {
		m.toast = "no other calendar"
		return m, nil
	}
	source := event.CalendarID
	if source == "" {
		source = m.selectedCalendar().ID
	}
	current := indexOfCalendar(m.calendars, source)
	destination := m.calendars[(current+1)%len(m.calendars)]
	if destination.ID == source {
		m.toast = "no other calendar"
		return m, nil
	}
	return m, func() tea.Msg {
		updated, err := m.client.MoveEvent(m.ctx, event.ID, source, destination.ID)
		return eventActionMsg{event: updated, err: err, label: "event moved to " + destination.Summary}
	}
}

func (m Model) toggleCalendarView() (Model, tea.Cmd) {
	if m.calendarView == calViewMonth {
		m.calendarView = calViewAgenda
		m.toast = "agenda view"
		return m, nil
	}
	m.calendarView = calViewMonth
	if m.calendarCursor.IsZero() {
		m.calendarCursor = startOfDay(time.Now())
	}
	m.toast = "month view"
	return m.ensureCalendarMonth(m.calendarCursor)
}

// calendarShiftCursor moves the month-grid cursor by days and/or months and
// loads the new month when the cursor crosses a month boundary.
func (m Model) calendarShiftCursor(days, months int) (Model, tea.Cmd) {
	cursor := m.calendarCursorOrToday()
	if months != 0 {
		cursor = cursor.AddDate(0, months, 0)
	}
	if days != 0 {
		cursor = cursor.AddDate(0, 0, days)
	}
	m.calendarCursor = startOfDay(cursor)
	m.calendarDayEventCursor = 0
	return m.ensureCalendarMonth(m.calendarCursor)
}

// ensureCalendarMonth loads the cursor's month only when it is not already the
// month held in monthEvents, so plain in-month navigation never refetches.
func (m Model) ensureCalendarMonth(cursor time.Time) (Model, tea.Cmd) {
	if monthStart(cursor).Equal(m.calendarMonth) {
		m.events = m.monthEvents
		m.calendarNext = ""
		return m, nil
	}
	return m.loadCalendarMonth(cursor)
}

// openCalendarFocusedEvent opens the highlighted activity on the focused day.
func (m Model) openCalendarFocusedEvent() (Model, tea.Cmd) {
	day := m.calendarCursorOrToday()
	event, ok := m.selectedCalendarDayEvent()
	if !ok {
		m.toast = "no events on " + day.Format("Mon 02 Jan")
		return m, nil
	}
	m.events = m.monthEvents
	idx, ok := eventIndexByID(m.events, event.ID)
	if !ok {
		idx = m.nthEventOnDayIndex(day, m.calendarDayEventCursor)
	}
	m.selected[FeatureCalendar] = idx
	m.focusedPane = paneDetail
	m.toast = "event opened"
	return m, nil
}

func (m Model) shiftCalendarDayEventCursor(delta int) Model {
	events := eventsOnDay(m.monthEvents, m.calendarCursorOrToday())
	if len(events) == 0 {
		m.calendarDayEventCursor = 0
		m.toast = "no events on " + m.calendarCursorOrToday().Format("Mon 02 Jan")
		return m
	}
	before := m.calendarDayEventCursor
	m.calendarDayEventCursor = clamp(m.calendarDayEventCursor+delta, len(events))
	if m.calendarDayEventCursor == before {
		if delta > 0 {
			m.toast = "last event on " + m.calendarCursorOrToday().Format("Mon 02 Jan")
		} else {
			m.toast = "first event on " + m.calendarCursorOrToday().Format("Mon 02 Jan")
		}
		return m
	}
	m.toast = fmt.Sprintf("%d/%d %s", m.calendarDayEventCursor+1, len(events), m.calendarCursorOrToday().Format("Mon 02 Jan"))
	return m
}

func (m *Model) clampCalendarDayEventCursor() {
	events := eventsOnDay(m.monthEvents, m.calendarCursorOrToday())
	m.calendarDayEventCursor = clamp(m.calendarDayEventCursor, len(events))
}

// calendarMonthBlocksEventAction reports whether an event-level action (RSVP,
// edit, delete, move) should be refused because the month grid has no event
// cursor; it leaves a toast telling the user how to proceed.
func (m *Model) calendarMonthBlocksEventAction() bool {
	if m.feature == FeatureCalendar && m.calendarView == calViewMonth && m.focusedPane != paneDetail {
		m.toast = "open an event with Enter first"
		return true
	}
	return false
}

// updateCalendarMonthKey remaps list-pane keys while the month grid is shown.
// It returns handled=false for anything it does not claim so global shortcuts
// continue to work.
func (m Model) updateCalendarMonthKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	switch msg.String() {
	case "h", "left":
		next, cmd := m.calendarShiftCursor(-1, 0)
		return next, cmd, true
	case "l", "right":
		next, cmd := m.calendarShiftCursor(1, 0)
		return next, cmd, true
	case "k", "up":
		next, cmd := m.calendarShiftCursor(-7, 0)
		return next, cmd, true
	case "j", "down":
		next, cmd := m.calendarShiftCursor(7, 0)
		return next, cmd, true
	case "}":
		next, cmd := m.calendarShiftCursor(0, 1)
		return next, cmd, true
	case "{":
		next, cmd := m.calendarShiftCursor(0, -1)
		return next, cmd, true
	case "t":
		m.calendarCursor = startOfDay(time.Now())
		m.calendarDayEventCursor = 0
		next, cmd := m.ensureCalendarMonth(m.calendarCursor)
		return next, cmd, true
	case "g":
		m.calendarCursor = monthStart(m.calendarCursorOrToday())
		m.calendarDayEventCursor = 0
		return m, nil, true
	case "G":
		cursor := m.calendarCursorOrToday()
		last := daysInMonth(monthStart(cursor))
		m.calendarCursor = time.Date(cursor.Year(), cursor.Month(), last, 0, 0, 0, 0, time.Local)
		m.calendarDayEventCursor = 0
		return m, nil, true
	case "J":
		return m.shiftCalendarDayEventCursor(1), nil, true
	case "K":
		return m.shiftCalendarDayEventCursor(-1), nil, true
	case "enter", "o":
		next, cmd := m.openCalendarFocusedEvent()
		return next, cmd, true
	}
	return m, nil, false
}

func (m *Model) handleEventAction(msg eventActionMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		m.err = msg.err.Error()
	} else {
		m.toast = msg.label
		if msg.deleted {
			m.removeEvent(msg.event.ID)
			if m.calendarFeedback.eventID == msg.event.ID {
				m.calendarFeedback = calendarActivityFeedback{}
			}
		} else {
			m.applyEvent(msg.event)
			if msg.rsvpResponse != "" && msg.event.ID != "" {
				m.calendarFeedback = calendarActivityFeedback{eventID: msg.event.ID, response: msg.rsvpResponse}
			} else if msg.event.ID != "" && m.calendarFeedback.eventID == msg.event.ID {
				m.calendarFeedback = calendarActivityFeedback{}
			}
		}
		m.clampSelections()
		m.persistWorkspaceCache()
	}

	if msg.rsvpResponse != "" && msg.event.ID != "" {
		_, cmd := m.refreshCurrentFeature()
		cmds = append(cmds, cmd)
	}

	return cmds
}

func (m *Model) handleCalendarMonthLoaded(msg calendarMonthLoadedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.loading = false
	if msg.err != nil {
		m.err = msg.err.Error()
		return cmds
	}
	m.calendarMonth = msg.month
	if m.calendarFeedback.eventID != "" {
		m.monthEvents = sortedEvents(mergeEvents(m.monthEvents, msg.events))
	} else {
		m.monthEvents = sortedEvents(msg.events)
	}
	m.events = m.monthEvents
	m.calendarNext = ""
	m.calendarFeedback = calendarActivityFeedback{}
	m.clampCalendarDayEventCursor()
	m.toast = msg.month.Format("January 2006")
	m.persistWorkspaceCache()

	return cmds
}
