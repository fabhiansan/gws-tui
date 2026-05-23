package daemon

import (
	"context"
	"encoding/json"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func (s *Server) dispatchMailLabels(ctx context.Context) ([]api.MailLabel, error) {
	labels, err := s.client.MailLabels(ctx)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.MailLabels = labels })
	}
	return labels, err
}

func (s *Server) dispatchMailThreads(ctx context.Context, params json.RawMessage) (api.Page[api.MailThread], error) {
	var p api.MailThreadsParams
	if err := decode(params, &p); err != nil {
		return api.Page[api.MailThread]{}, err
	}
	page, err := s.client.MailThreads(ctx, p.Query)
	if err == nil && p.Query.Search == "" && p.Query.Label != "" {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
			api.ApplyMailPage(snapshot, p.Query.Label, page)
		})
	}
	return page, err
}

func (s *Server) dispatchSendMail(ctx context.Context, params json.RawMessage) (api.MailThread, error) {
	var p api.SendMailParams
	if err := decode(params, &p); err != nil {
		return api.MailThread{}, err
	}
	thread, err := s.client.SendMail(ctx, p.Draft)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
			api.ApplyMailThread(snapshot, thread)
		})
		s.broadcast("mail.changed", thread)
	}
	return thread, err
}

func (s *Server) dispatchMailDrafts(ctx context.Context, params json.RawMessage) (api.Page[api.MailDraftItem], error) {
	var p api.MailDraftsParams
	if err := decode(params, &p); err != nil {
		return api.Page[api.MailDraftItem]{}, err
	}
	page, err := s.client.MailDrafts(ctx, p.PageToken)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.MailDrafts = page })
	}
	return page, err
}

func (s *Server) dispatchCreateMailDraft(ctx context.Context, params json.RawMessage) (api.MailDraftItem, error) {
	var p api.SendMailParams
	if err := decode(params, &p); err != nil {
		return api.MailDraftItem{}, err
	}
	draft, err := s.client.CreateMailDraft(ctx, p.Draft)
	if err == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
			snapshot.MailDrafts.Items = append([]api.MailDraftItem{draft}, snapshot.MailDrafts.Items...)
		})
		s.broadcast("mail.changed", map[string]string{"draft_id": draft.ID, "action": "draft_created"})
	}
	return draft, err
}

func (s *Server) dispatchSendMailDraft(ctx context.Context, params json.RawMessage) (api.MailThread, error) {
	var p api.MailDraftIDParams
	if err := decode(params, &p); err != nil {
		return api.MailThread{}, err
	}
	thread, err := s.client.SendMailDraft(ctx, p.DraftID)
	if err == nil {
		s.applyMailThread(thread)
		s.broadcast("mail.changed", thread)
	}
	return thread, err
}

func (s *Server) dispatchArchiveMail(ctx context.Context, params json.RawMessage) (any, error) {
	var p api.ThreadIDParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	err := s.client.ArchiveMail(ctx, p.ThreadID)
	if err == nil {
		s.broadcast("mail.changed", map[string]string{"thread_id": p.ThreadID, "action": "archived"})
	}
	return nil, err
}

func (s *Server) dispatchTrashMail(ctx context.Context, params json.RawMessage) (any, error) {
	var p api.ThreadIDParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	err := s.client.TrashMail(ctx, p.ThreadID)
	if err == nil {
		s.broadcast("mail.changed", map[string]string{"thread_id": p.ThreadID, "action": "trashed"})
	}
	return nil, err
}

func (s *Server) dispatchToggleStar(ctx context.Context, params json.RawMessage) (api.MailThread, error) {
	var p api.ThreadIDParams
	if err := decode(params, &p); err != nil {
		return api.MailThread{}, err
	}
	thread, err := s.client.ToggleStar(ctx, p.ThreadID)
	if err == nil {
		s.applyMailThread(thread)
		s.broadcast("mail.changed", thread)
	}
	return thread, err
}

func (s *Server) dispatchSetMailUnread(ctx context.Context, params json.RawMessage) (api.MailThread, error) {
	var p api.SetMailUnreadParams
	if err := decode(params, &p); err != nil {
		return api.MailThread{}, err
	}
	thread, err := s.client.SetMailUnread(ctx, p.ThreadID, p.Unread)
	if err == nil {
		s.applyMailThread(thread)
		s.broadcast("mail.changed", thread)
	}
	return thread, err
}

func (s *Server) dispatchToggleMailLabel(ctx context.Context, params json.RawMessage) (api.MailThread, error) {
	var p api.ToggleMailLabelParams
	if err := decode(params, &p); err != nil {
		return api.MailThread{}, err
	}
	thread, err := s.client.ToggleMailLabel(ctx, p.ThreadID, p.LabelID)
	if err == nil {
		s.applyMailThread(thread)
		s.broadcast("mail.changed", thread)
	}
	return thread, err
}
