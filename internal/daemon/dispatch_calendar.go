package daemon

import (
	"context"
	"encoding/json"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func (s *Server) dispatchCalendarLists(ctx context.Context) (api.Page[api.CalendarListItem], error) {
	page, err := s.client.CalendarLists(ctx)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.CalendarLists = page.Items })
	}
	return page, err
}

func (s *Server) dispatchCalendarEvents(ctx context.Context, params json.RawMessage) (api.Page[api.CalendarEvent], error) {
	var p api.CalendarEventsParams
	if err := decode(params, &p); err != nil {
		return api.Page[api.CalendarEvent]{}, err
	}
	page, err := s.client.CalendarEvents(ctx, p.Query)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
			snapshot.Events = page
			snapshot.CalendarID = p.Query.CalendarID
			snapshot.CalendarMonth = calendarSnapshotMonth(p.Query)
		})
	}
	return page, err
}

func (s *Server) dispatchQuickAddEvent(ctx context.Context, params json.RawMessage) (api.CalendarEvent, error) {
	var p api.QuickAddEventParams
	if err := decode(params, &p); err != nil {
		return api.CalendarEvent{}, err
	}
	event, err := s.client.QuickAddEvent(ctx, p.Text)
	if err == nil {
		s.applyEvent(event)
		s.broadcast("calendar.changed", event)
	}
	return event, err
}

func (s *Server) dispatchCreateEvent(ctx context.Context, params json.RawMessage) (api.CalendarEvent, error) {
	var p api.CreateEventParams
	if err := decode(params, &p); err != nil {
		return api.CalendarEvent{}, err
	}
	event, err := s.client.CreateEvent(ctx, p.Draft)
	if err == nil {
		s.applyEvent(event)
		s.broadcast("calendar.changed", event)
	}
	return event, err
}

func (s *Server) dispatchUpdateEvent(ctx context.Context, params json.RawMessage) (api.CalendarEvent, error) {
	var p api.UpdateEventParams
	if err := decode(params, &p); err != nil {
		return api.CalendarEvent{}, err
	}
	event, err := s.client.UpdateEvent(ctx, p.EventID, p.Draft)
	if err == nil {
		s.applyEvent(event)
		s.broadcast("calendar.changed", event)
	}
	return event, err
}

func (s *Server) dispatchMoveEvent(ctx context.Context, params json.RawMessage) (api.CalendarEvent, error) {
	var p api.MoveEventParams
	if err := decode(params, &p); err != nil {
		return api.CalendarEvent{}, err
	}
	event, err := s.client.MoveEvent(ctx, p.EventID, p.SourceCalendarID, p.DestinationCalendarID)
	if err == nil {
		s.applyEvent(event)
		s.broadcast("calendar.changed", event)
	}
	return event, err
}

func (s *Server) dispatchRSVPEvent(ctx context.Context, params json.RawMessage) (api.CalendarEvent, error) {
	var p api.RSVPEventParams
	if err := decode(params, &p); err != nil {
		return api.CalendarEvent{}, err
	}
	event, err := s.client.RSVPEvent(ctx, p.EventID, p.Response)
	if err == nil {
		s.applyEvent(event)
		s.broadcast("calendar.changed", event)
	}
	return event, err
}

func (s *Server) dispatchDeleteEvent(ctx context.Context, params json.RawMessage) (any, error) {
	var p api.EventIDParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	err := s.client.DeleteEvent(ctx, p.EventID)
	if err == nil {
		s.broadcast("calendar.changed", map[string]string{"event_id": p.EventID, "action": "deleted"})
	}
	return nil, err
}
