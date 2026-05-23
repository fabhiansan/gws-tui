package daemon

import (
	"context"
	"encoding/json"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func (s *Server) dispatchMeetSpaces(ctx context.Context) (api.Page[api.MeetSpace], error) {
	page, err := s.client.MeetSpaces(ctx)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.MeetSpaces = page.Items })
	}
	return page, err
}

func (s *Server) dispatchCreateMeetSpace(ctx context.Context, params json.RawMessage) (api.MeetSpace, error) {
	var p api.CreateMeetSpaceParams
	if err := decode(params, &p); err != nil {
		return api.MeetSpace{}, err
	}
	space, err := s.client.CreateMeetSpace(ctx, p.Title)
	if err == nil {
		s.applyMeetSpace(space)
		s.broadcast("meet.changed", space)
	}
	return space, err
}

func (s *Server) dispatchEndMeetSpace(ctx context.Context, params json.RawMessage) (any, error) {
	var p api.MeetSpaceNameParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	err := s.client.EndMeetSpace(ctx, p.Name)
	if err == nil {
		s.broadcast("meet.changed", map[string]string{"name": p.Name, "action": "ended"})
	}
	return nil, err
}
