package api

import (
	"context"
	"log/slog"
)

type HybridClient struct {
	primary  WorkspaceClient
	fallback WorkspaceClient
}

func (c *HybridClient) AuthStatus(ctx context.Context) (AuthStatus, error) {
	status, err := c.primary.AuthStatus(ctx)
	if err == nil && status.Valid() && status.EncryptionValid {
		return status, nil
	}
	fallback, fallbackErr := c.fallback.AuthStatus(ctx)
	if fallbackErr != nil {
		if err != nil {
			return AuthStatus{}, err
		}
		return status, nil
	}
	if err != nil {
		fallback.Error = err.Error()
	}
	if !status.EncryptionValid && status.Error != "" {
		fallback.Error = status.Error
	}
	return fallback, nil
}

func (c *HybridClient) ChatSpaces(ctx context.Context) (Page[Space], error) {
	return withFallback(ctx, c.primary.ChatSpaces, c.fallback.ChatSpaces)
}

func (c *HybridClient) ChatMessages(ctx context.Context, spaceName, pageToken string) (Page[ChatMessage], error) {
	return withFallbackArg2(ctx, c.primary.ChatMessages, c.fallback.ChatMessages, spaceName, pageToken)
}

func (c *HybridClient) SendChatMessage(ctx context.Context, spaceName, text string) (ChatMessage, error) {
	return withFallbackArg2(ctx, c.primary.SendChatMessage, c.fallback.SendChatMessage, spaceName, text)
}

func (c *HybridClient) SubscribeChat(ctx context.Context, spaceName string) (<-chan ChatMessage, error) {
	ch, err := c.primary.SubscribeChat(ctx, spaceName)
	if err == nil && ch != nil {
		return ch, nil
	}
	return c.fallback.SubscribeChat(ctx, spaceName)
}

func (c *HybridClient) ChatMembers(ctx context.Context, spaceName string) ([]SpaceMember, error) {
	return c.primary.ChatMembers(ctx, spaceName)
}

func (c *HybridClient) PeopleGet(ctx context.Context, userID string) (Person, error) {
	return c.primary.PeopleGet(ctx, userID)
}

func (c *HybridClient) MailLabels(ctx context.Context) ([]MailLabel, error) {
	return withFallback(ctx, c.primary.MailLabels, c.fallback.MailLabels)
}

func (c *HybridClient) MailThreads(ctx context.Context, q MailQuery) (Page[MailThread], error) {
	return withFallbackArg(ctx, c.primary.MailThreads, c.fallback.MailThreads, q)
}

func (c *HybridClient) SendMail(ctx context.Context, draft MailDraft) (MailThread, error) {
	return withFallbackArg(ctx, c.primary.SendMail, c.fallback.SendMail, draft)
}

func (c *HybridClient) ArchiveMail(ctx context.Context, id string) error {
	return fallbackErr(c.primary.ArchiveMail(ctx, id), func() error { return c.fallback.ArchiveMail(ctx, id) })
}

func (c *HybridClient) TrashMail(ctx context.Context, id string) error {
	return fallbackErr(c.primary.TrashMail(ctx, id), func() error { return c.fallback.TrashMail(ctx, id) })
}

func (c *HybridClient) ToggleStar(ctx context.Context, id string) (MailThread, error) {
	return withFallbackArg(ctx, c.primary.ToggleStar, c.fallback.ToggleStar, id)
}

func (c *HybridClient) CalendarEvents(ctx context.Context, q CalendarQuery) (Page[CalendarEvent], error) {
	return withFallbackArg(ctx, c.primary.CalendarEvents, c.fallback.CalendarEvents, q)
}

func (c *HybridClient) QuickAddEvent(ctx context.Context, text string) (CalendarEvent, error) {
	return withFallbackArg(ctx, c.primary.QuickAddEvent, c.fallback.QuickAddEvent, text)
}

func (c *HybridClient) CreateEvent(ctx context.Context, draft EventDraft) (CalendarEvent, error) {
	return withFallbackArg(ctx, c.primary.CreateEvent, c.fallback.CreateEvent, draft)
}

func (c *HybridClient) RSVPEvent(ctx context.Context, id, response string) (CalendarEvent, error) {
	return withFallbackArg2(ctx, c.primary.RSVPEvent, c.fallback.RSVPEvent, id, response)
}

func (c *HybridClient) DeleteEvent(ctx context.Context, id string) error {
	return fallbackErr(c.primary.DeleteEvent(ctx, id), func() error { return c.fallback.DeleteEvent(ctx, id) })
}

func (c *HybridClient) MeetSpaces(ctx context.Context) (Page[MeetSpace], error) {
	return withFallback(ctx, c.primary.MeetSpaces, c.fallback.MeetSpaces)
}

func (c *HybridClient) CreateMeetSpace(ctx context.Context, title string) (MeetSpace, error) {
	return withFallbackArg(ctx, c.primary.CreateMeetSpace, c.fallback.CreateMeetSpace, title)
}

func (c *HybridClient) EndMeetSpace(ctx context.Context, name string) error {
	return fallbackErr(c.primary.EndMeetSpace(ctx, name), func() error { return c.fallback.EndMeetSpace(ctx, name) })
}

func (c *HybridClient) Close() error {
	_ = c.primary.Close()
	return c.fallback.Close()
}

func withFallback[T any](ctx context.Context, primary, fallback func(context.Context) (T, error)) (T, error) {
	out, err := primary(ctx)
	if err == nil {
		return out, nil
	}
	slog.Debug("gws primary client failed; using fixture fallback", "error", err)
	return fallback(ctx)
}

func withFallbackArg[A, T any](ctx context.Context, primary, fallback func(context.Context, A) (T, error), arg A) (T, error) {
	out, err := primary(ctx, arg)
	if err == nil {
		return out, nil
	}
	slog.Debug("gws primary client failed; using fixture fallback", "error", err)
	return fallback(ctx, arg)
}

func withFallbackArg2[A, B, T any](ctx context.Context, primary, fallback func(context.Context, A, B) (T, error), a A, b B) (T, error) {
	out, err := primary(ctx, a, b)
	if err == nil {
		return out, nil
	}
	slog.Debug("gws primary client failed; using fixture fallback", "error", err)
	return fallback(ctx, a, b)
}

func fallbackErr(err error, fallback func() error) error {
	if err == nil {
		return nil
	}
	slog.Debug("gws primary client failed; using fixture fallback", "error", err)
	return fallback()
}
