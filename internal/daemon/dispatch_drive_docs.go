package daemon

import (
	"context"
	"encoding/json"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func (s *Server) dispatchDriveFiles(ctx context.Context, params json.RawMessage) (api.Page[api.DriveFile], error) {
	var p api.DriveFilesParams
	if err := decode(params, &p); err != nil {
		return api.Page[api.DriveFile]{}, err
	}
	page, err := s.client.DriveFiles(ctx, p.Query)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.DriveFiles = page })
	}
	return page, err
}

func (s *Server) dispatchDocs(ctx context.Context, params json.RawMessage) (api.Page[api.DriveFile], error) {
	var p api.DriveFilesParams
	if err := decode(params, &p); err != nil {
		return api.Page[api.DriveFile]{}, err
	}
	page, err := s.client.Docs(ctx, p.Query)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.DocFiles = page })
	}
	return page, err
}

func (s *Server) dispatchDoc(ctx context.Context, params json.RawMessage) (api.DocDocument, error) {
	var p api.DocIDParams
	if err := decode(params, &p); err != nil {
		return api.DocDocument{}, err
	}
	doc, err := s.client.Doc(ctx, p.DocumentID)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.Doc = doc })
	}
	return doc, err
}
