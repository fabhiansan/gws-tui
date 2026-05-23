package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fabhiansan/gws-tui/internal/api"
)

// newCalendarModel returns a loaded model parked on the calendar feature so
// calendar-specific rendering and key handling can be exercised directly.
func newCalendarModel(t *testing.T) Model {
	t.Helper()
	model := New(Options{
		Client: newTestWorkspaceClient(),
		Config: Config{
			InitialFeature: "calendar",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
		Version: "test",
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	model = updated.(Model)
	msg := model.loadAllCmd()().(loadedMsg)
	updated, _ = model.Update(msg)
	model = updated.(Model)
	model.feature = FeatureCalendar
	return model
}

func TestCalendarAgendaShowsTodayAllDayAndRecurring(t *testing.T) {
	model := newCalendarModel(t)
	model.calendarView = calViewAgenda
	now := time.Now()
	model.events = sortedEvents([]api.CalendarEvent{
		{ID: "e1", Summary: "Standup meeting", Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour), RSVP: "accepted"},
		{ID: "e2", Summary: "Sprint week", Start: startOfDay(now), End: startOfDay(now).AddDate(0, 0, 1), AllDay: true},
		{ID: "e3", Summary: "Weekly sync", Start: startOfDay(now).AddDate(0, 0, 3).Add(10 * time.Hour), End: startOfDay(now).AddDate(0, 0, 3).Add(11 * time.Hour), Recurring: true},
	})
	model.selected[FeatureCalendar] = 0

	view := stripANSI(model.View())
	for _, want := range []string{"Today", "all-day", "Standup", "↻"} {
		if !strings.Contains(view, want) {
			t.Fatalf("agenda view missing %q:\n%s", want, view)
		}
	}
}

func TestCalendarDefaultsToMonthGrid(t *testing.T) {
	model := newCalendarModel(t)
	if model.calendarView != calViewMonth {
		t.Fatalf("calendar should default to month view, got %v", model.calendarView)
	}
	if model.calendarCursor.IsZero() {
		t.Fatal("month view should seed the day cursor by default")
	}
	if model.calendarMonth.IsZero() || len(model.monthEvents) == 0 {
		t.Fatalf("month view should be populated by the initial calendar fetch: month=%s events=%d", model.calendarMonth, len(model.monthEvents))
	}
}

func TestCalendarToggleSwitchesToAgendaAndBack(t *testing.T) {
	model := newCalendarModel(t)

	agenda, cmd := model.toggleCalendarView()
	if agenda.calendarView != calViewAgenda {
		t.Fatalf("toggle did not enter agenda view: %v", agenda.calendarView)
	}
	if cmd != nil {
		t.Fatal("entering agenda view should not fetch")
	}

	back, cmd := agenda.toggleCalendarView()
	if back.calendarView != calViewMonth {
		t.Fatalf("toggling again should return to month view, got %v", back.calendarView)
	}
	if cmd != nil {
		t.Fatal("returning to an already loaded month should not refetch")
	}
}

func TestCalendarMonthGridRendersMonth(t *testing.T) {
	model := newCalendarModel(t)
	model.calendarView = calViewMonth
	model.calendarCursor = startOfDay(time.Date(2026, time.May, 21, 0, 0, 0, 0, time.Local))
	model.calendarMonth = monthStart(model.calendarCursor)
	model.monthEvents = []api.CalendarEvent{{ID: "p1", Summary: "Planning review", Start: model.calendarCursor.Add(9 * time.Hour), End: model.calendarCursor.Add(10 * time.Hour)}}

	view := stripANSI(model.View())
	for _, want := range []string{"May 2026", "Mon", "Sun", " 1", "21", "31", "Planning"} {
		if !strings.Contains(view, want) {
			t.Fatalf("month grid missing %q:\n%s", want, view)
		}
	}
}

func TestCalendarMonthNavigationCrossesMonths(t *testing.T) {
	model := newCalendarModel(t)
	model.calendarView = calViewMonth
	model.calendarCursor = startOfDay(time.Date(2026, time.May, 31, 0, 0, 0, 0, time.Local))

	// One day past the 31st must roll into June and trigger a month load.
	next, cmd := model.calendarShiftCursor(1, 0)
	if next.calendarCursor.Month() != time.June || next.calendarCursor.Day() != 1 {
		t.Fatalf("expected Jun 1, got %s", next.calendarCursor.Format("2006-01-02"))
	}
	if cmd == nil {
		t.Fatal("crossing a month boundary should load the new month")
	}

	// Movement inside the loaded month must not refetch.
	next.calendarMonth = monthStart(next.calendarCursor)
	same, cmd := next.calendarShiftCursor(1, 0)
	if cmd != nil {
		t.Fatalf("in-month navigation should not refetch, got cmd for %s", same.calendarCursor.Format("2006-01-02"))
	}
}

func TestCalendarMonthKeyRemapsNavigation(t *testing.T) {
	model := newCalendarModel(t)
	model.calendarView = calViewMonth
	model.calendarCursor = startOfDay(time.Date(2026, time.May, 15, 0, 0, 0, 0, time.Local))

	next, _, handled := model.updateCalendarMonthKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if !handled {
		t.Fatal("'j' should be handled by the month grid")
	}
	if got := next.calendarCursor.Day(); got != 22 {
		t.Fatalf("'j' should move one week forward to the 22nd, got %d", got)
	}

	if _, _, handled := next.updateCalendarMonthKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Z")}); handled {
		t.Fatal("unrelated keys must fall through to the global handler")
	}
}

func TestCalendarEnterOpensFocusedEventDetail(t *testing.T) {
	model := newCalendarModel(t)
	day := startOfDay(time.Date(2026, time.May, 21, 0, 0, 0, 0, time.Local))
	model.calendarView = calViewMonth
	model.calendarCursor = day
	model.calendarMonth = monthStart(day)
	model.events = []api.CalendarEvent{
		{ID: "other", Summary: "Other day", Start: day.AddDate(0, 0, 1).Add(9 * time.Hour), End: day.AddDate(0, 0, 1).Add(10 * time.Hour)},
		{ID: "focus", Summary: "Focused agenda", Start: day.Add(11 * time.Hour), End: day.Add(12 * time.Hour), Location: "Room 1"},
	}
	model.monthEvents = model.events

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("opening local event detail should not return a command, got %v", cmd)
	}
	m := updated.(Model)
	if m.focusedPane != paneDetail {
		t.Fatalf("Enter should open detail pane, got focus %v", m.focusedPane)
	}
	if got := m.selectedEvent().ID; got != "focus" {
		t.Fatalf("selected event = %q, want focus", got)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Focused agenda", "Room 1"} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail view missing %q:\n%s", want, view)
		}
	}
}

func TestCalendarDayEventCursorChoosesActivityBeforeEnter(t *testing.T) {
	model := newCalendarModel(t)
	day := startOfDay(time.Date(2026, time.May, 21, 0, 0, 0, 0, time.Local))
	model.calendarView = calViewMonth
	model.calendarCursor = day
	model.calendarMonth = monthStart(day)
	model.monthEvents = sortedEvents([]api.CalendarEvent{
		{ID: "early", Summary: "Early standup", Start: day.Add(9 * time.Hour), End: day.Add(10 * time.Hour), RSVP: "needsAction"},
		{ID: "later", Summary: "Later planning", Start: day.Add(13 * time.Hour), End: day.Add(14 * time.Hour), Location: "Room 2", RSVP: "needsAction"},
	})
	model.events = model.monthEvents

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("J")})
	if cmd != nil {
		t.Fatalf("local day-event navigation should not return a command, got %v", cmd)
	}
	model = updated.(Model)
	if model.calendarDayEventCursor != 1 {
		t.Fatalf("J should choose the second activity, got cursor %d", model.calendarDayEventCursor)
	}
	view := stripANSI(model.View())
	if !strings.Contains(view, "›○ 13:00 Later") {
		t.Fatalf("focused activity should be marked in the month grid:\n%s", view)
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("opening local event detail should not return a command, got %v", cmd)
	}
	model = updated.(Model)
	if got := model.selectedEvent().ID; got != "later" {
		t.Fatalf("Enter opened %q, want later", got)
	}
	if model.focusedPane != paneDetail {
		t.Fatalf("Enter should open detail pane, got focus %v", model.focusedPane)
	}
	view = stripANSI(model.View())
	for _, want := range []string{"Later planning", "Room 2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail view missing %q:\n%s", want, view)
		}
	}
}

func TestCalendarRSVPFeedbackAppearsInEventDetail(t *testing.T) {
	model := newCalendarModel(t)
	model.calendarView = calViewAgenda
	model.focusedPane = paneDetail
	event := model.selectedEvent()
	event.RSVP = "accepted"

	updated, _ := model.Update(eventActionMsg{
		event:        event,
		label:        "RSVP updated: attending",
		rsvpResponse: "accepted",
	})
	model = updated.(Model)

	view := stripANSI(model.View())
	for _, want := range []string{"● You're attending", "You're attending"} {
		if !strings.Contains(view, want) {
			t.Fatalf("event detail missing RSVP feedback %q:\n%s", want, view)
		}
	}
}

func TestCalendarMonthHydratesFromWorkspaceCache(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache.json")
	today := startOfDay(time.Now())
	month := monthStart(today)
	if err := saveWorkspaceCache(cachePath, workspaceCache{
		CalendarLists: []api.CalendarListItem{{ID: "primary", Summary: "Primary", Primary: true}},
		CalendarID:    "primary",
		CalendarMonth: month,
		Events: api.Page[api.CalendarEvent]{
			Items: []api.CalendarEvent{{ID: "cached", Summary: "Cached planning", Start: today.Add(9 * time.Hour), End: today.Add(10 * time.Hour)}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	client := &countingWorkspaceClient{WorkspaceClient: newTestWorkspaceClient()}
	model := New(Options{
		Client: client,
		Config: Config{
			InitialFeature: "calendar",
			StatePath:      filepath.Join(dir, "state.json"),
			CachePath:      cachePath,
			DraftDir:       dir,
		},
	})
	if !model.cacheLoaded {
		t.Fatal("calendar cache should be loaded")
	}
	if !model.calendarMonth.Equal(month) {
		t.Fatalf("calendarMonth = %s, want %s", model.calendarMonth, month)
	}
	if len(model.monthEvents) != 1 || model.monthEvents[0].ID != "cached" {
		t.Fatalf("month events were not hydrated from cache: %#v", model.monthEvents)
	}
	_, cmd := model.ensureCalendarMonth(model.calendarCursorOrToday())
	if cmd != nil {
		t.Fatal("cached current month should not refetch on calendar open")
	}
	if client.calendarEventsCalls != 0 {
		t.Fatalf("cache hydration should not call CalendarEvents, got %d", client.calendarEventsCalls)
	}
}

func TestCalendarRRefreshesMonthView(t *testing.T) {
	client := &countingWorkspaceClient{WorkspaceClient: newTestWorkspaceClient()}
	model := New(Options{
		Client: client,
		Config: Config{
			InitialFeature: "calendar",
			StatePath:      t.TempDir() + "/state.json",
			DraftDir:       t.TempDir(),
		},
	})
	model.feature = FeatureCalendar
	model.calendarView = calViewMonth
	model.calendarCursor = startOfDay(time.Date(2026, time.May, 21, 0, 0, 0, 0, time.Local))
	model.calendarMonth = monthStart(model.calendarCursor)
	model.monthEvents = []api.CalendarEvent{{ID: "old", Summary: "Old", Start: model.calendarCursor.Add(9 * time.Hour)}}
	model.events = model.monthEvents

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("r should return a calendar refresh command")
	}
	msg := cmd()
	if _, ok := msg.(calendarMonthLoadedMsg); !ok {
		t.Fatalf("r should load the current calendar month, got %T", msg)
	}
	if client.calendarEventsCalls != 1 {
		t.Fatalf("r should fetch CalendarEvents once, got %d", client.calendarEventsCalls)
	}
	if !model.loading {
		t.Fatal("calendar refresh should set loading")
	}
}

func TestCalendarDeleteRemovesEvent(t *testing.T) {
	model := newCalendarModel(t)
	model.events = []api.CalendarEvent{
		{ID: "keep", Summary: "Keep me", Start: time.Now()},
		{ID: "drop", Summary: "Delete me", Start: time.Now().Add(time.Hour)},
	}

	updated, _ := model.Update(eventActionMsg{event: api.CalendarEvent{ID: "drop"}, label: "event deleted", deleted: true})
	m := updated.(Model)
	if len(m.events) != 1 || m.events[0].ID != "keep" {
		t.Fatalf("delete should drop only the targeted event, got %+v", m.events)
	}
}

func TestCalendarMonthEventsLoadIntoSeparateSlice(t *testing.T) {
	model := newCalendarModel(t)
	month := monthStart(time.Date(2026, time.May, 1, 0, 0, 0, 0, time.Local))
	updated, _ := model.Update(calendarMonthLoadedMsg{
		month: month,
		events: []api.CalendarEvent{
			{ID: "m2", Summary: "Late", Start: month.AddDate(0, 0, 20)},
			{ID: "m1", Summary: "Early", Start: month.AddDate(0, 0, 2)},
		},
	})
	m := updated.(Model)
	if !m.calendarMonth.Equal(month) {
		t.Fatalf("calendarMonth not recorded: %s", m.calendarMonth)
	}
	if len(m.monthEvents) != 2 || m.monthEvents[0].ID != "m1" {
		t.Fatalf("month events should be sorted by start, got %+v", m.monthEvents)
	}
}

func TestCalendarViewsStayWithinBounds(t *testing.T) {
	model := newCalendarModel(t)
	model.calendarCursor = startOfDay(time.Date(2026, time.May, 21, 0, 0, 0, 0, time.Local))
	model.monthEvents = []api.CalendarEvent{{ID: "x", Summary: "Demo", Start: model.calendarCursor}}

	for _, view := range []calViewMode{calViewAgenda, calViewMonth} {
		model.calendarView = view
		for _, size := range []struct{ w, h int }{{80, 24}, {100, 32}, {140, 40}} {
			updated, _ := model.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			m := updated.(Model)
			for i, line := range strings.Split(m.View(), "\n") {
				if w := lipgloss.Width(line); w > size.w {
					t.Fatalf("view %d at %dx%d: line %d width %d exceeds %d", view, size.w, size.h, i, w, size.w)
				}
			}
		}
	}
}

func TestRelativeDayLabel(t *testing.T) {
	today := startOfDay(time.Date(2026, time.May, 21, 0, 0, 0, 0, time.Local))
	cases := []struct {
		day  time.Time
		want string
	}{
		{today, "Today"},
		{today.AddDate(0, 0, 1), "Tomorrow"},
		{today.AddDate(0, 0, -1), "Yesterday"},
		{today.AddDate(0, 0, 5), "Tue 26 May"},
	}
	for _, tc := range cases {
		if got := relativeDayLabel(tc.day, today); !strings.Contains(got, tc.want) {
			t.Errorf("relativeDayLabel(%s) = %q, want it to contain %q", tc.day.Format("2006-01-02"), got, tc.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{time.Hour, "1h"},
		{90 * time.Minute, "1h30m"},
		{0, "0m"},
	}
	for _, tc := range cases {
		if got := formatDuration(tc.d); got != tc.want {
			t.Errorf("formatDuration(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
