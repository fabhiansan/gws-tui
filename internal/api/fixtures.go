package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type FixtureClient struct {
	mu       sync.Mutex
	reason   string
	spaces   []Space
	messages map[string][]ChatMessage
	labels   []MailLabel
	threads  []MailThread
	events   []CalendarEvent
	meet     []MeetSpace
	seq      int
	closed   bool
}

func NewFixtureClient() *FixtureClient {
	fixtureTZ := time.FixedZone("WIB", 7*60*60)
	now := time.Date(2026, 5, 18, 9, 30, 0, 0, fixtureTZ)
	spaces := []Space{
		{Name: "spaces/engineering", DisplayName: "#engineering", SpaceType: "SPACE", Unread: true, Live: true, Members: []Member{
			{ID: "users/alice", DisplayName: "Alice", Email: "alice@example.com"},
			{ID: "users/bob", DisplayName: "Bob", Email: "bob@example.com"},
		}},
		{Name: "spaces/design", DisplayName: "#design", SpaceType: "SPACE", Unread: true, Members: []Member{
			{ID: "users/chen", DisplayName: "Chen", Email: "chen@example.com"},
		}},
		{Name: "spaces/random", DisplayName: "#random", SpaceType: "SPACE"},
		{Name: "spaces/alice", DisplayName: "@alice", SpaceType: "DIRECT_MESSAGE", Live: true},
	}
	return &FixtureClient{
		reason: "fixture data",
		spaces: spaces,
		messages: map[string][]ChatMessage{
			"spaces/engineering": {
				{ID: "msg-1", Name: "spaces/engineering/messages/msg-1", Space: "spaces/engineering", SenderID: "users/alice", SenderName: "Alice", Text: "anyone seen the latest design?", CreateTime: now.Add(-2 * time.Hour), ThreadID: "thread-1"},
				{ID: "msg-2", Name: "spaces/engineering/messages/msg-2", Space: "spaces/engineering", SenderID: "users/bob", SenderName: "Bob", Text: "yeah, looks great\n\n```go\nfmt.Println(\"ship it\")\n```", CreateTime: now.Add(-110 * time.Minute), ThreadID: "thread-1", ParentID: "msg-1"},
				{ID: "msg-3", Name: "spaces/engineering/messages/msg-3", Space: "spaces/engineering", SenderID: "users/me", SenderName: "You", Text: "shipping today", CreateTime: now.Add(-100 * time.Minute)},
			},
			"spaces/design": {
				{ID: "msg-4", Name: "spaces/design/messages/msg-4", Space: "spaces/design", SenderID: "users/chen", SenderName: "Chen", Text: "Mock review moved to Friday.", CreateTime: now.Add(-26 * time.Hour)},
			},
			"spaces/random": {},
			"spaces/alice": {
				{ID: "msg-5", Name: "spaces/alice/messages/msg-5", Space: "spaces/alice", SenderID: "users/alice", SenderName: "Alice", Text: "Can you join the 1:1 early?", CreateTime: now.Add(-35 * time.Minute)},
			},
		},
		labels: []MailLabel{
			{Name: "Inbox", LabelIDs: []string{"INBOX"}},
			{Name: "Unread", LabelIDs: []string{"UNREAD"}},
			{Name: "Starred", LabelIDs: []string{"STARRED"}},
			{Name: "Important", LabelIDs: []string{"IMPORTANT"}},
			{Name: "Sent", LabelIDs: []string{"SENT"}},
			{Name: "Drafts", LabelIDs: []string{"DRAFT"}},
			{Name: "Spam", LabelIDs: []string{"SPAM"}, IncludeSpamTrash: true},
			{Name: "Trash", LabelIDs: []string{"TRASH"}, IncludeSpamTrash: true},
			{Name: "All Mail", Query: "-in:spam -in:trash"},
		},
		threads: []MailThread{
			{ID: "mail-1", Sender: "Alice", SenderEmail: "alice@example.com", Subject: "Re: Q4 planning", Snippet: "Sending the latest deck for Q4.", Date: now.Add(-2 * time.Hour), Body: "Hi team,\n\nSending the latest deck for Q4. Let me know your thoughts by Friday.\n\n— Alice", Unread: true, Starred: false, Labels: []string{"INBOX", "UNREAD"}, QuotedLines: 23},
			{ID: "mail-2", Sender: "Bob", SenderEmail: "bob@example.com", Subject: "Lunch?", Snippet: "Want to grab lunch after standup?", Date: now.Add(-5 * time.Hour), Body: "Want to grab lunch after standup?\n\nBob", Labels: []string{"INBOX"}},
			{ID: "mail-3", Sender: "GitHub", SenderEmail: "noreply@github.com", Subject: "PR review requested", Snippet: "Review requested on google-workspace/tui.", Date: now.Add(-25 * time.Hour), Body: "A review was requested for the TUI release workflow.", Starred: true, Labels: []string{"INBOX", "STARRED"}},
		},
		events: []CalendarEvent{
			{ID: "event-1", Summary: "1:1 with Alice", Start: now.Add(4*time.Hour + 30*time.Minute), End: now.Add(5 * time.Hour), Location: "Google Meet", HangoutLink: "https://meet.google.com/abc-defg-hij", Attendees: []string{"alice@example.com", "you@example.com"}, Description: "Weekly sync.", RSVP: "needsAction", Type: "1:1"},
			{ID: "event-2", Summary: "Eng review", Start: now.Add(7 * time.Hour), End: now.Add(8 * time.Hour), Attendees: []string{"eng@example.com"}, RSVP: "accepted", Type: "meeting"},
			{ID: "event-3", Summary: "Planning", Start: now.Add(26 * time.Hour), End: now.Add(27 * time.Hour), Location: "Room 4A", RSVP: "tentative", Type: "meeting"},
		},
		meet: []MeetSpace{
			{Name: "spaces/standup-daily", MeetingURI: "https://meet.google.com/abc-defg-hij", MeetingCode: "abc-defg-hij", Created: now.AddDate(0, 0, -6), Type: "open", ActiveParticipants: 5, Recording: false, Active: true},
			{Name: "spaces/demo-thursday", MeetingURI: "https://meet.google.com/xyz-demo-thu", MeetingCode: "xyz-demo-thu", Created: now.AddDate(0, 0, -3), Type: "open"},
			{Name: "spaces/q4-planning", MeetingURI: "https://meet.google.com/q4-plan-2026", MeetingCode: "q4-plan-2026", Created: now.AddDate(0, 0, -1), Type: "restricted"},
		},
		seq: 100,
	}
}

func (c *FixtureClient) AuthStatus(context.Context) (AuthStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return AuthStatus{
		AuthMethod:                 "fixture",
		ClientConfigExists:         true,
		CredentialSource:           c.reason,
		EncryptedCredentialsExists: false,
		EncryptionValid:            true,
		KeyringBackend:             "fixture",
		ProjectID:                  "fixture-project",
		Storage:                    "memory",
		TokenCacheExists:           true,
	}, nil
}

func (c *FixtureClient) ChatSpaces(context.Context) (Page[Space], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Page[Space]{Items: append([]Space(nil), c.spaces...)}, nil
}

func (c *FixtureClient) ChatMessages(_ context.Context, spaceName, pageToken string) (Page[ChatMessage], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	items := append([]ChatMessage(nil), c.messages[spaceName]...)
	if pageToken == "older" {
		return Page[ChatMessage]{Items: []ChatMessage{
			{ID: "msg-old-1", Name: spaceName + "/messages/msg-old-1", Space: spaceName, SenderID: "users/system", SenderName: "Workspace", Text: "Older fixture message loaded by pagination.", CreateTime: time.Date(2026, 5, 16, 8, 0, 0, 0, time.FixedZone("WIB", 7*60*60))},
		}}, nil
	}
	token := ""
	if len(items) > 0 {
		token = "older"
	}
	return Page[ChatMessage]{Items: items, NextPageToken: token}, nil
}

func (c *FixtureClient) SendChatMessage(_ context.Context, spaceName, text string) (ChatMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(text) == "" {
		return ChatMessage{}, errors.New("message is empty")
	}
	c.seq++
	msg := ChatMessage{
		ID:         fmt.Sprintf("msg-%d", c.seq),
		Name:       fmt.Sprintf("%s/messages/msg-%d", spaceName, c.seq),
		Space:      spaceName,
		SenderID:   "users/me",
		SenderName: "You",
		Text:       text,
		CreateTime: time.Now(),
	}
	c.messages[spaceName] = append(c.messages[spaceName], msg)
	return msg, nil
}

func (c *FixtureClient) SubscribeChat(ctx context.Context, spaceName string) (<-chan ChatMessage, error) {
	out := make(chan ChatMessage)
	go func() {
		defer close(out)
		timer := time.NewTimer(900 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			c.mu.Lock()
			c.seq++
			msg := ChatMessage{
				ID:           fmt.Sprintf("live-%d", c.seq),
				Name:         fmt.Sprintf("%s/messages/live-%d", spaceName, c.seq),
				Space:        spaceName,
				SenderID:     "users/alice",
				SenderName:   "Alice",
				Text:         "Realtime fixture update from another terminal.",
				CreateTime:   time.Now(),
				FromRealtime: true,
			}
			c.messages[spaceName] = append(c.messages[spaceName], msg)
			c.mu.Unlock()
			select {
			case out <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (c *FixtureClient) ChatMembers(_ context.Context, spaceName string) ([]SpaceMember, error) {
	return nil, nil
}

func (c *FixtureClient) PeopleGet(_ context.Context, userID string) (Person, error) {
	return Person{UserID: userID}, nil
}

func (c *FixtureClient) MailLabels(context.Context) ([]MailLabel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]MailLabel(nil), c.labels...), nil
}

func (c *FixtureClient) MailThreads(_ context.Context, q MailQuery) (Page[MailThread], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	items := make([]MailThread, 0, len(c.threads))
	needle := strings.ToLower(strings.TrimSpace(q.Search))
	for _, thread := range c.threads {
		if needle != "" && !strings.Contains(strings.ToLower(thread.Subject+" "+thread.Body+" "+thread.Sender), needle) {
			continue
		}
		if q.Label != "" && q.Label != "All Mail" && !threadHasLabel(thread, q.Label) {
			continue
		}
		items = append(items, thread)
	}
	return Page[MailThread]{Items: items, NextPageToken: nextToken(items, q.PageToken)}, nil
}

func (c *FixtureClient) SendMail(_ context.Context, draft MailDraft) (MailThread, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(draft.To) == "" || strings.TrimSpace(draft.Subject) == "" {
		return MailThread{}, errors.New("to and subject are required")
	}
	c.seq++
	thread := MailThread{
		ID:          fmt.Sprintf("mail-%d", c.seq),
		Sender:      "You",
		SenderEmail: "you@example.com",
		Subject:     draft.Subject,
		Snippet:     firstLine(draft.Body),
		Date:        time.Now(),
		Body:        draft.Body,
		Labels:      []string{"SENT"},
	}
	c.threads = append([]MailThread{thread}, c.threads...)
	return thread, nil
}

func (c *FixtureClient) ArchiveMail(_ context.Context, id string) error {
	return c.removeLabel(id, "INBOX")
}

func (c *FixtureClient) TrashMail(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.threads {
		if c.threads[i].ID == id {
			c.threads[i].Labels = append(withoutLabel(c.threads[i].Labels, "INBOX"), "TRASH")
			return nil
		}
	}
	return errors.New("thread not found")
}

func (c *FixtureClient) ToggleStar(_ context.Context, id string) (MailThread, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.threads {
		if c.threads[i].ID == id {
			c.threads[i].Starred = !c.threads[i].Starred
			if c.threads[i].Starred {
				c.threads[i].Labels = append(c.threads[i].Labels, "STARRED")
			} else {
				c.threads[i].Labels = withoutLabel(c.threads[i].Labels, "STARRED")
			}
			return c.threads[i], nil
		}
	}
	return MailThread{}, errors.New("thread not found")
}

func (c *FixtureClient) CalendarEvents(_ context.Context, q CalendarQuery) (Page[CalendarEvent], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	items := make([]CalendarEvent, 0, len(c.events))
	needle := strings.ToLower(strings.TrimSpace(q.Search))
	for _, event := range c.events {
		if needle != "" && !strings.Contains(strings.ToLower(event.Summary+" "+event.Description+" "+event.Location), needle) {
			continue
		}
		items = append(items, event)
	}
	return Page[CalendarEvent]{Items: items, NextPageToken: nextToken(items, q.PageToken)}, nil
}

func (c *FixtureClient) QuickAddEvent(ctx context.Context, text string) (CalendarEvent, error) {
	start := time.Now().Add(24 * time.Hour).Truncate(time.Hour)
	return c.CreateEvent(ctx, EventDraft{
		Summary: strings.TrimSpace(text),
		Start:   start,
		End:     start.Add(time.Hour),
	})
}

func (c *FixtureClient) CreateEvent(_ context.Context, draft EventDraft) (CalendarEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(draft.Summary) == "" {
		return CalendarEvent{}, errors.New("summary is required")
	}
	c.seq++
	event := CalendarEvent{
		ID:          fmt.Sprintf("event-%d", c.seq),
		Summary:     draft.Summary,
		Start:       draft.Start,
		End:         draft.End,
		Location:    draft.Location,
		Attendees:   append([]string(nil), draft.Attendees...),
		Description: draft.Description,
		RSVP:        "accepted",
		Type:        "meeting",
	}
	c.events = append(c.events, event)
	return event, nil
}

func (c *FixtureClient) RSVPEvent(_ context.Context, eventID, response string) (CalendarEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.events {
		if c.events[i].ID == eventID {
			c.events[i].RSVP = response
			return c.events[i], nil
		}
	}
	return CalendarEvent{}, errors.New("event not found")
}

func (c *FixtureClient) DeleteEvent(_ context.Context, eventID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.events {
		if c.events[i].ID == eventID {
			c.events = append(c.events[:i], c.events[i+1:]...)
			return nil
		}
	}
	return errors.New("event not found")
}

func (c *FixtureClient) MeetSpaces(context.Context) (Page[MeetSpace], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Page[MeetSpace]{Items: append([]MeetSpace(nil), c.meet...)}, nil
}

func (c *FixtureClient) CreateMeetSpace(_ context.Context, title string) (MeetSpace, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	slug := strings.ToLower(strings.NewReplacer(" ", "-", "_", "-").Replace(strings.TrimSpace(title)))
	if slug == "" {
		slug = fmt.Sprintf("meet-%d", c.seq)
	}
	space := MeetSpace{
		Name:        "spaces/" + slug,
		MeetingURI:  "https://meet.google.com/" + slug,
		MeetingCode: slug,
		Created:     time.Now(),
		Type:        "open",
		Active:      true,
	}
	c.meet = append([]MeetSpace{space}, c.meet...)
	return space, nil
}

func (c *FixtureClient) EndMeetSpace(_ context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.meet {
		if c.meet[i].Name == name {
			c.meet[i].Active = false
			c.meet[i].ActiveParticipants = 0
			return nil
		}
	}
	return errors.New("meet space not found")
}

func (c *FixtureClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *FixtureClient) removeLabel(id, label string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.threads {
		if c.threads[i].ID == id {
			c.threads[i].Labels = withoutLabel(c.threads[i].Labels, label)
			return nil
		}
	}
	return errors.New("thread not found")
}

func threadHasLabel(thread MailThread, labelName string) bool {
	wanted := strings.ToUpper(strings.ReplaceAll(labelName, " ", "_"))
	for _, label := range thread.Labels {
		if label == wanted || strings.EqualFold(label, labelName) {
			return true
		}
	}
	return labelName == "Inbox" && contains(thread.Labels, "INBOX")
}

func withoutLabel(labels []string, remove string) []string {
	out := labels[:0]
	for _, label := range labels {
		if label != remove {
			out = append(out, label)
		}
	}
	return out
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func nextToken[T any](items []T, current string) string {
	if current == "" && len(items) > 2 {
		return "next"
	}
	return ""
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		return value[:idx]
	}
	return value
}
