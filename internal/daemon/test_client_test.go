package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/fabhiansan/gws-tui/internal/api"
)

type testWorkspaceClient struct {
	now time.Time
}

func newTestWorkspaceClient() *testWorkspaceClient {
	return &testWorkspaceClient{now: time.Date(2026, 5, 18, 9, 30, 0, 0, time.FixedZone("WIB", 7*60*60))}
}

func (c *testWorkspaceClient) AuthStatus(context.Context) (api.AuthStatus, error) {
	return api.AuthStatus{AuthMethod: "test", EncryptionValid: true, TokenCacheExists: true}, nil
}

func (c *testWorkspaceClient) ChatSpaces(context.Context) (api.Page[api.Space], error) {
	return api.Page[api.Space]{Items: []api.Space{
		{Name: "spaces/alice", DisplayName: "Alice", SpaceType: "DM"},
		{Name: "spaces/engineering", DisplayName: "#engineering", SpaceType: "SPACE"},
		{Name: "spaces/design", DisplayName: "#design", SpaceType: "SPACE"},
	}}, nil
}

func (c *testWorkspaceClient) ChatMessages(_ context.Context, spaceName, pageToken string) (api.Page[api.ChatMessage], error) {
	if pageToken != "" {
		return api.Page[api.ChatMessage]{}, nil
	}
	age := 35 * time.Minute
	if spaceName == "spaces/engineering" {
		age = 100 * time.Minute
	}
	if spaceName == "spaces/design" {
		age = 4 * time.Hour
	}
	return api.Page[api.ChatMessage]{Items: []api.ChatMessage{{
		ID:         spaceName + "/latest",
		Name:       spaceName + "/messages/latest",
		Space:      spaceName,
		SenderID:   "users/alice",
		SenderName: "Alice",
		Text:       "latest message",
		CreateTime: c.now.Add(-age),
	}}}, nil
}

func (c *testWorkspaceClient) SendChatMessage(_ context.Context, spaceName, text string) (api.ChatMessage, error) {
	return api.ChatMessage{ID: "sent-1", Space: spaceName, Text: text, SenderName: "Me", CreateTime: c.now}, nil
}

func (c *testWorkspaceClient) SubscribeChat(context.Context, string) (<-chan api.ChatMessage, error) {
	return make(chan api.ChatMessage), nil
}

func (c *testWorkspaceClient) ChatMembers(context.Context, string) ([]api.SpaceMember, error) {
	return []api.SpaceMember{{UserID: "alice", Type: "HUMAN"}}, nil
}

func (c *testWorkspaceClient) PeopleGet(context.Context, string) (api.Person, error) {
	return api.Person{UserID: "alice", DisplayName: "Alice"}, nil
}

func (c *testWorkspaceClient) DownloadAttachment(context.Context, api.Attachment, string) error {
	return errors.New("test client does not download attachments")
}

func (c *testWorkspaceClient) MailLabels(context.Context) ([]api.MailLabel, error) {
	return []api.MailLabel{{Name: "Inbox"}}, nil
}

func (c *testWorkspaceClient) MailThreads(context.Context, api.MailQuery) (api.Page[api.MailThread], error) {
	return api.Page[api.MailThread]{Items: []api.MailThread{{
		ID:      "mail-1",
		Sender:  "Alice",
		Subject: "Launch notes",
		Date:    c.now,
		Body:    "Ready",
	}}}, nil
}

func (c *testWorkspaceClient) SendMail(_ context.Context, draft api.MailDraft) (api.MailThread, error) {
	return api.MailThread{ID: "mail-sent", Subject: draft.Subject, Body: draft.Body, Date: c.now}, nil
}

func (c *testWorkspaceClient) ArchiveMail(context.Context, string) error {
	return nil
}

func (c *testWorkspaceClient) TrashMail(context.Context, string) error {
	return nil
}

func (c *testWorkspaceClient) ToggleStar(context.Context, string) (api.MailThread, error) {
	return api.MailThread{ID: "mail-1", Subject: "Launch notes", Starred: true, Date: c.now}, nil
}

func (c *testWorkspaceClient) CalendarEvents(context.Context, api.CalendarQuery) (api.Page[api.CalendarEvent], error) {
	return api.Page[api.CalendarEvent]{Items: []api.CalendarEvent{{
		ID:      "event-1",
		Summary: "Planning",
		Start:   c.now,
		End:     c.now.Add(time.Hour),
	}}}, nil
}

func (c *testWorkspaceClient) QuickAddEvent(_ context.Context, text string) (api.CalendarEvent, error) {
	return api.CalendarEvent{ID: "event-quick", Summary: text, Start: c.now, End: c.now.Add(time.Hour)}, nil
}

func (c *testWorkspaceClient) CreateEvent(_ context.Context, draft api.EventDraft) (api.CalendarEvent, error) {
	return api.CalendarEvent{ID: "event-created", Summary: draft.Summary, Start: draft.Start, End: draft.End}, nil
}

func (c *testWorkspaceClient) RSVPEvent(_ context.Context, eventID, response string) (api.CalendarEvent, error) {
	return api.CalendarEvent{ID: eventID, Summary: "Planning", RSVP: response, Start: c.now, End: c.now.Add(time.Hour)}, nil
}

func (c *testWorkspaceClient) DeleteEvent(context.Context, string) error {
	return nil
}

func (c *testWorkspaceClient) MeetSpaces(context.Context) (api.Page[api.MeetSpace], error) {
	return api.Page[api.MeetSpace]{Items: []api.MeetSpace{{
		Name:       "spaces/meet-1",
		MeetingURI: "https://meet.google.com/abc-defg-hij",
		Created:    c.now,
	}}}, nil
}

func (c *testWorkspaceClient) CreateMeetSpace(_ context.Context, title string) (api.MeetSpace, error) {
	return api.MeetSpace{Name: "spaces/meet-created", MeetingURI: "https://meet.google.com/" + title, Created: c.now}, nil
}

func (c *testWorkspaceClient) EndMeetSpace(context.Context, string) error {
	return nil
}

func (c *testWorkspaceClient) Close() error {
	return nil
}
