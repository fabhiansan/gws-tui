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

type calendarErrorClient struct {
	*testWorkspaceClient
}

func (c *calendarErrorClient) CalendarEvents(context.Context, api.CalendarQuery) (api.Page[api.CalendarEvent], error) {
	return api.Page[api.CalendarEvent]{}, context.DeadlineExceeded
}

type delayedChatMessagesClient struct {
	*testWorkspaceClient
	started chan string
	release chan struct{}
}

func (c *delayedChatMessagesClient) ChatMessages(ctx context.Context, spaceName, pageToken string) (api.Page[api.ChatMessage], error) {
	if pageToken != "" {
		return c.testWorkspaceClient.ChatMessages(ctx, spaceName, pageToken)
	}
	select {
	case c.started <- spaceName:
	default:
	}
	select {
	case <-c.release:
		return c.testWorkspaceClient.ChatMessages(ctx, spaceName, pageToken)
	case <-ctx.Done():
		return api.Page[api.ChatMessage]{}, ctx.Err()
	}
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

func (c *testWorkspaceClient) SendChatMessage(_ context.Context, spaceName, text, threadID string, _ []api.LocalAttachment) (api.ChatMessage, error) {
	return api.ChatMessage{ID: "sent-1", Space: spaceName, Text: text, SenderName: "Me", CreateTime: c.now, ThreadID: threadID}, nil
}

func (c *testWorkspaceClient) EditChatMessage(_ context.Context, messageName, text string) (api.ChatMessage, error) {
	return api.ChatMessage{ID: "latest", Name: messageName, Space: "spaces/alice", Text: text, SenderName: "Me", CreateTime: c.now}, nil
}

func (c *testWorkspaceClient) DeleteChatMessage(context.Context, string) error {
	return nil
}

func (c *testWorkspaceClient) CreateChatSpace(_ context.Context, displayName string) (api.Space, error) {
	return api.Space{Name: "spaces/created", DisplayName: displayName, SpaceType: "SPACE"}, nil
}

func (c *testWorkspaceClient) SetupChatSpace(_ context.Context, displayName string, _ []string) (api.Space, error) {
	return api.Space{Name: "spaces/setup", DisplayName: displayName, SpaceType: "SPACE"}, nil
}

func (c *testWorkspaceClient) AddChatReaction(_ context.Context, messageName, _ string) (string, error) {
	return messageName + "/reactions/reaction-1", nil
}

func (c *testWorkspaceClient) DeleteChatReaction(context.Context, string) error {
	return nil
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

func (c *testWorkspaceClient) MailDrafts(context.Context, string) (api.Page[api.MailDraftItem], error) {
	return api.Page[api.MailDraftItem]{Items: []api.MailDraftItem{{ID: "draft-1", Subject: "Draft", Date: c.now}}}, nil
}

func (c *testWorkspaceClient) CreateMailDraft(_ context.Context, draft api.MailDraft) (api.MailDraftItem, error) {
	return api.MailDraftItem{ID: "draft-created", To: draft.To, Subject: draft.Subject, Snippet: draft.Body, Date: c.now}, nil
}

func (c *testWorkspaceClient) SendMailDraft(context.Context, string) (api.MailThread, error) {
	return api.MailThread{ID: "mail-draft-sent", Subject: "Draft", Date: c.now}, nil
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

func (c *testWorkspaceClient) SetMailUnread(_ context.Context, threadID string, unread bool) (api.MailThread, error) {
	return api.MailThread{ID: threadID, Subject: "Launch notes", Unread: unread, Date: c.now}, nil
}

func (c *testWorkspaceClient) ToggleMailLabel(_ context.Context, threadID, labelID string) (api.MailThread, error) {
	return api.MailThread{ID: threadID, Subject: "Launch notes", Labels: []string{labelID}, Date: c.now}, nil
}

func (c *testWorkspaceClient) CalendarLists(context.Context) (api.Page[api.CalendarListItem], error) {
	return api.Page[api.CalendarListItem]{Items: []api.CalendarListItem{
		{ID: "primary", Summary: "Primary", Primary: true},
		{ID: "team@example.com", Summary: "Team"},
	}}, nil
}

func (c *testWorkspaceClient) CalendarEvents(_ context.Context, query api.CalendarQuery) (api.Page[api.CalendarEvent], error) {
	calendarID := query.CalendarID
	if calendarID == "" {
		calendarID = "primary"
	}
	return api.Page[api.CalendarEvent]{Items: []api.CalendarEvent{{
		ID:         "event-1",
		CalendarID: calendarID,
		Summary:    "Planning",
		Start:      c.now,
		End:        c.now.Add(time.Hour),
	}}}, nil
}

func (c *testWorkspaceClient) QuickAddEvent(_ context.Context, text string) (api.CalendarEvent, error) {
	return api.CalendarEvent{ID: "event-quick", Summary: text, Start: c.now, End: c.now.Add(time.Hour)}, nil
}

func (c *testWorkspaceClient) CreateEvent(_ context.Context, draft api.EventDraft) (api.CalendarEvent, error) {
	return api.CalendarEvent{ID: "event-created", CalendarID: draft.CalendarID, Summary: draft.Summary, Start: draft.Start, End: draft.End}, nil
}

func (c *testWorkspaceClient) UpdateEvent(_ context.Context, eventID string, draft api.EventDraft) (api.CalendarEvent, error) {
	return api.CalendarEvent{ID: eventID, CalendarID: draft.CalendarID, Summary: draft.Summary, Start: draft.Start, End: draft.End}, nil
}

func (c *testWorkspaceClient) MoveEvent(_ context.Context, eventID, _, destinationCalendarID string) (api.CalendarEvent, error) {
	return api.CalendarEvent{ID: eventID, CalendarID: destinationCalendarID, Summary: "Planning", Start: c.now, End: c.now.Add(time.Hour)}, nil
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

func (c *testWorkspaceClient) TaskLists(context.Context) (api.Page[api.TaskList], error) {
	return api.Page[api.TaskList]{Items: []api.TaskList{
		{ID: "tasks-default", Title: "My Tasks", Updated: c.now.Add(-time.Hour)},
		{ID: "tasks-work", Title: "Work", Updated: c.now.Add(-30 * time.Minute)},
	}}, nil
}

func (c *testWorkspaceClient) Tasks(_ context.Context, query api.TaskQuery) (api.Page[api.TaskItem], error) {
	return api.Page[api.TaskItem]{Items: []api.TaskItem{{
		ID:         query.TaskListID + "-task-1",
		TaskListID: query.TaskListID,
		Title:      "Review launch checklist",
		Notes:      "Confirm release docs and install script.",
		Status:     "needsAction",
		Due:        c.now.Add(24 * time.Hour),
		Updated:    c.now.Add(-10 * time.Minute),
	}}}, nil
}

func (c *testWorkspaceClient) SetTaskCompleted(_ context.Context, taskListID, taskID string, completed bool) (api.TaskItem, error) {
	status := "needsAction"
	completedAt := time.Time{}
	if completed {
		status = "completed"
		completedAt = c.now
	}
	return api.TaskItem{
		ID:         taskID,
		TaskListID: taskListID,
		Title:      "Review launch checklist",
		Notes:      "Confirm release docs and install script.",
		Status:     status,
		Completed:  completedAt,
		Updated:    c.now,
	}, nil
}

func (c *testWorkspaceClient) DeleteTask(context.Context, string, string) error {
	return nil
}

func (c *testWorkspaceClient) DriveFiles(context.Context, api.DriveQuery) (api.Page[api.DriveFile], error) {
	return api.Page[api.DriveFile]{Items: []api.DriveFile{{
		ID:           "drive-1",
		Name:         "Release checklist.pdf",
		MimeType:     "application/pdf",
		ModifiedTime: c.now.Add(-2 * time.Hour),
		WebViewLink:  "https://drive.google.com/file/d/drive-1",
		Size:         2048,
	}}}, nil
}

func (c *testWorkspaceClient) Docs(context.Context, api.DriveQuery) (api.Page[api.DriveFile], error) {
	return api.Page[api.DriveFile]{Items: []api.DriveFile{{
		ID:           "doc-1",
		Name:         "Launch notes",
		MimeType:     "application/vnd.google-apps.document",
		ModifiedTime: c.now.Add(-time.Hour),
		WebViewLink:  "https://docs.google.com/document/d/doc-1",
	}}}, nil
}

func (c *testWorkspaceClient) Doc(_ context.Context, documentID string) (api.DocDocument, error) {
	return api.DocDocument{ID: documentID, Title: "Launch notes", Body: "Launch plan\nShip checklist"}, nil
}

func (c *testWorkspaceClient) Close() error {
	return nil
}
