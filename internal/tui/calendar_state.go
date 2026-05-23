package tui

import (
	"sort"
	"time"

	"github.com/fabhiansan/gws-tui/internal/api"
)

// calViewMode switches the calendar's left pane between the upcoming-events
// agenda list and the month grid.
type calViewMode int

const (
	calViewAgenda calViewMode = iota
	calViewMonth
)

func (m Model) selectedEvent() api.CalendarEvent {
	if len(m.events) == 0 {
		return api.CalendarEvent{}
	}
	return m.events[clamp(m.selected[FeatureCalendar], len(m.events))]
}

func (m Model) selectedCalendar() api.CalendarListItem {
	if len(m.calendars) == 0 {
		return api.CalendarListItem{ID: "primary", Summary: "Primary", Primary: true}
	}
	return m.calendars[clamp(m.calendarIndex, len(m.calendars))]
}

func indexOfCalendar(calendars []api.CalendarListItem, id string) int {
	if id == "" {
		return 0
	}
	for i, calendar := range calendars {
		if calendar.ID == id {
			return i
		}
	}
	return 0
}

func selectedCalendarID(calendars []api.CalendarListItem, index int) string {
	if len(calendars) == 0 {
		return "primary"
	}
	return calendars[clamp(index, len(calendars))].ID
}

func sortedEvents(events []api.CalendarEvent) []api.CalendarEvent {
	out := append([]api.CalendarEvent(nil), events...)
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

func eventIndexByID(events []api.CalendarEvent, id string) (int, bool) {
	if id == "" {
		return 0, false
	}
	for i, event := range events {
		if event.ID == id {
			return i, true
		}
	}
	return 0, false
}

// --- calendar date helpers -------------------------------------------------

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

func monthStart(t time.Time) time.Time {
	if t.IsZero() {
		t = time.Now()
	}
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local)
}

func daysInMonth(first time.Time) int {
	return first.AddDate(0, 1, -1).Day()
}

func sameMonth(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month()
}

func eventsOnDay(events []api.CalendarEvent, day time.Time) []api.CalendarEvent {
	var out []api.CalendarEvent
	for _, e := range events {
		if sameDay(e.Start, day) {
			out = append(out, e)
		}
	}
	return out
}

// --- calendar view state ---------------------------------------------------

// calendarCursorOrToday returns the focused month-grid day, defaulting to
// today so callers never have to special-case a zero cursor.
func (m Model) calendarCursorOrToday() time.Time {
	if m.calendarCursor.IsZero() {
		return startOfDay(time.Now())
	}
	return m.calendarCursor
}

// firstEventOnOrAfter returns the agenda index of the first event that starts
// on or after day, or the last event when none qualify.
func (m Model) firstEventOnOrAfter(day time.Time) int {
	for i, e := range m.events {
		if !startOfDay(e.Start).Before(day) {
			return i
		}
	}
	if len(m.events) == 0 {
		return 0
	}
	return len(m.events) - 1
}

func (m Model) selectedCalendarDayEvent() (api.CalendarEvent, bool) {
	events := eventsOnDay(m.monthEvents, m.calendarCursorOrToday())
	if len(events) == 0 {
		return api.CalendarEvent{}, false
	}
	return events[clamp(m.calendarDayEventCursor, len(events))], true
}

func (m Model) nthEventOnDayIndex(day time.Time, dayIndex int) int {
	seen := 0
	for i, e := range m.events {
		if sameDay(e.Start, day) {
			if seen == dayIndex {
				return i
			}
			seen++
		}
	}
	return 0
}
