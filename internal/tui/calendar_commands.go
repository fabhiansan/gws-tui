package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) loadCalendarSectionCmd() tea.Cmd {
	calendarIndex := m.calendarIndex
	cursor := m.calendarCursorOrToday()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		calendars, calendarsErr := m.client.CalendarLists(ctx)
		calendarID := selectedCalendarID(calendars.Items, calendarIndex)
		month := monthStart(cursor)
		events, eventsErr := m.client.CalendarEvents(ctx, api.CalendarQuery{CalendarID: calendarID, TimeMin: month, TimeMax: month.AddDate(0, 1, 0)})
		return featureRefreshedMsg{feature: FeatureCalendar, calendars: calendars, calendarID: calendarID, calendarMonth: month, events: events, startup: true, err: firstErr(calendarsErr, eventsErr)}
	}
}

func (m Model) loadSelectedCalendar() (Model, tea.Cmd) {
	calendar := m.selectedCalendar()
	if calendar.ID == "" {
		m.events = nil
		m.calendarNext = ""
		return m, nil
	}
	m.loading = true
	m.events = nil
	m.calendarNext = ""
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()

		page, err := m.client.CalendarEvents(ctx, api.CalendarQuery{CalendarID: calendar.ID, Search: m.search})
		return featureRefreshedMsg{
			feature:    FeatureCalendar,
			calendars:  api.Page[api.CalendarListItem]{Items: m.calendars},
			calendarID: calendar.ID,
			events:     page,
			err:        err,
		}
	}
}

// loadCalendarMonth fetches every event in the month containing cursor for the
// selected calendar. The result lands in monthEvents via calendarMonthLoadedMsg.
func (m Model) loadCalendarMonth(cursor time.Time) (Model, tea.Cmd) {
	m.loading = true
	return m, m.calendarMonthCmd(cursor)
}

func (m Model) calendarMonthCmd(cursor time.Time) tea.Cmd {
	month := monthStart(cursor)
	calendarID := m.selectedCalendar().ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()
		page, err := m.client.CalendarEvents(ctx, api.CalendarQuery{
			CalendarID: calendarID,
			TimeMin:    month,
			TimeMax:    month.AddDate(0, 1, 0),
		})
		return calendarMonthLoadedMsg{month: month, events: page.Items, err: err}
	}
}
