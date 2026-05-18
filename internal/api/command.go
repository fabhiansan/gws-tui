package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type CommandClient struct {
	path string
}

func NewCommandClient(path string) *CommandClient {
	return &CommandClient{path: path}
}

func (c *CommandClient) AuthStatus(ctx context.Context) (AuthStatus, error) {
	var out AuthStatus
	err := c.runJSON(ctx, &out, "auth", "status")
	return out, err
}

func (c *CommandClient) ChatSpaces(ctx context.Context) (Page[Space], error) {
	var raw struct {
		Spaces        []Space `json:"spaces"`
		Items         []Space `json:"items"`
		NextPageToken string  `json:"nextPageToken"`
	}
	err := c.runJSON(ctx, &raw, "chat", "spaces", "list", "--params", `{"pageSize":100}`, "--format", "json")
	if err != nil {
		return Page[Space]{}, err
	}
	items := raw.Spaces
	if len(items) == 0 {
		items = raw.Items
	}
	return Page[Space]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) ChatMessages(ctx context.Context, spaceName, pageToken string) (Page[ChatMessage], error) {
	params := map[string]any{"parent": spaceName, "pageSize": 50}
	if pageToken != "" {
		params["pageToken"] = pageToken
	}
	payload, _ := json.Marshal(params)
	var raw struct {
		Messages []struct {
			Name       string `json:"name"`
			Text       string `json:"text"`
			Argument   string `json:"argumentText"`
			CreateTime string `json:"createTime"`
			Sender     struct {
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
			} `json:"sender"`
			Thread struct {
				Name string `json:"name"`
			} `json:"thread"`
		} `json:"messages"`
		NextPageToken string `json:"nextPageToken"`
	}
	err := c.runJSON(ctx, &raw, "chat", "spaces", "messages", "list", "--params", string(payload), "--format", "json")
	if err != nil {
		return Page[ChatMessage]{}, err
	}
	items := make([]ChatMessage, 0, len(raw.Messages))
	for _, msg := range raw.Messages {
		created, _ := time.Parse(time.RFC3339, msg.CreateTime)
		text := msg.Text
		if text == "" {
			text = msg.Argument
		}
		items = append(items, ChatMessage{
			ID:         lastSegment(msg.Name),
			Name:       msg.Name,
			Space:      spaceName,
			SenderID:   msg.Sender.Name,
			SenderName: fallback(msg.Sender.DisplayName, msg.Sender.Name),
			Text:       text,
			CreateTime: created,
			ThreadID:   msg.Thread.Name,
		})
	}
	return Page[ChatMessage]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) SendChatMessage(ctx context.Context, spaceName, text string) (ChatMessage, error) {
	var raw struct {
		Name       string `json:"name"`
		Text       string `json:"text"`
		CreateTime string `json:"createTime"`
		Sender     struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"sender"`
	}
	body, _ := json.Marshal(map[string]any{"text": text})
	err := c.runJSON(ctx, &raw, "chat", "spaces", "messages", "create", "--params", fmt.Sprintf(`{"parent":%q}`, spaceName), "--json", string(body), "--format", "json")
	if err != nil {
		return ChatMessage{}, err
	}
	created, _ := time.Parse(time.RFC3339, raw.CreateTime)
	return ChatMessage{ID: lastSegment(raw.Name), Name: raw.Name, Space: spaceName, SenderID: raw.Sender.Name, SenderName: fallback(raw.Sender.DisplayName, "You"), Text: fallback(raw.Text, text), CreateTime: created}, nil
}

func (c *CommandClient) SubscribeChat(ctx context.Context, spaceName string) (<-chan ChatMessage, error) {
	ch := make(chan ChatMessage)
	close(ch)
	return ch, nil
}

func (c *CommandClient) ChatMembers(ctx context.Context, spaceName string) ([]SpaceMember, error) {
	if spaceName == "" {
		return nil, errors.New("space name required")
	}
	params, _ := json.Marshal(map[string]any{"parent": spaceName, "pageSize": 20})
	var raw struct {
		Memberships []struct {
			Name   string `json:"name"`
			Member struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"member"`
		} `json:"memberships"`
	}
	if err := c.runJSON(ctx, &raw, "chat", "spaces", "members", "list", "--params", string(params), "--format", "json"); err != nil {
		return nil, err
	}
	members := make([]SpaceMember, 0, len(raw.Memberships))
	for _, m := range raw.Memberships {
		ref := m.Member.Name
		if ref == "" {
			ref = m.Name
		}
		userID := UserIDFromName(ref)
		if userID == "" {
			continue
		}
		members = append(members, SpaceMember{UserID: userID, Type: m.Member.Type})
	}
	return members, nil
}

var errPeopleAPIUnavailable = errors.New("people API unavailable")

func IsPeopleAPIUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errPeopleAPIUnavailable) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "People API has not been used") || strings.Contains(msg, "it is disabled")
}

func (c *CommandClient) PeopleGet(ctx context.Context, userID string) (Person, error) {
	if userID == "" {
		return Person{}, errors.New("user id required")
	}
	params, _ := json.Marshal(map[string]any{
		"resourceName": "people/" + userID,
		"personFields": "names,emailAddresses",
	})
	var raw struct {
		ResourceName string `json:"resourceName"`
		Names        []struct {
			DisplayName string `json:"displayName"`
		} `json:"names"`
		EmailAddresses []struct {
			Value string `json:"value"`
		} `json:"emailAddresses"`
	}
	if err := c.runJSON(ctx, &raw, "people", "people", "get", "--params", string(params), "--format", "json"); err != nil {
		if IsPeopleAPIUnavailable(err) {
			return Person{}, errPeopleAPIUnavailable
		}
		return Person{}, err
	}
	person := Person{UserID: UserIDFromName(raw.ResourceName)}
	if person.UserID == "" {
		person.UserID = userID
	}
	if len(raw.Names) > 0 {
		person.DisplayName = raw.Names[0].DisplayName
	}
	if len(raw.EmailAddresses) > 0 {
		person.Email = raw.EmailAddresses[0].Value
	}
	return person, nil
}

func UserIDFromName(name string) string {
	if name == "" {
		return ""
	}
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

func (c *CommandClient) MailLabels(context.Context) ([]MailLabel, error) {
	return defaultMailLabels(), nil
}

func (c *CommandClient) MailThreads(ctx context.Context, q MailQuery) (Page[MailThread], error) {
	params := map[string]any{"userId": "me", "maxResults": 20}
	if q.PageToken != "" {
		params["pageToken"] = q.PageToken
	}
	if q.Search != "" {
		params["q"] = q.Search
	} else if q.Label != "" && q.Label != "All Mail" {
		params["labelIds"] = []string{strings.ToUpper(strings.ReplaceAll(q.Label, " ", "_"))}
	}
	payload, _ := json.Marshal(params)
	var raw struct {
		Messages []struct {
			ID       string   `json:"id"`
			ThreadID string   `json:"threadId"`
			Snippet  string   `json:"snippet"`
			LabelIDs []string `json:"labelIds"`
		} `json:"messages"`
		NextPageToken string `json:"nextPageToken"`
	}
	err := c.runJSON(ctx, &raw, "gmail", "users", "messages", "list", "--params", string(payload), "--format", "json")
	if err != nil {
		return Page[MailThread]{}, err
	}
	items := make([]MailThread, 0, len(raw.Messages))
	for _, msg := range raw.Messages {
		items = append(items, MailThread{
			ID:      fallback(msg.ThreadID, msg.ID),
			Sender:  "Gmail",
			Subject: fallback(msg.Snippet, "(no subject)"),
			Snippet: msg.Snippet,
			Date:    time.Now(),
			Body:    msg.Snippet,
			Labels:  msg.LabelIDs,
		})
	}
	return Page[MailThread]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) SendMail(ctx context.Context, draft MailDraft) (MailThread, error) {
	return MailThread{}, errors.New("mail compose through generic gws is not wired yet")
}

func (c *CommandClient) ArchiveMail(context.Context, string) error {
	return errors.New("archive not wired")
}
func (c *CommandClient) TrashMail(context.Context, string) error {
	return errors.New("trash not wired")
}
func (c *CommandClient) ToggleStar(context.Context, string) (MailThread, error) {
	return MailThread{}, errors.New("star not wired")
}

func (c *CommandClient) CalendarEvents(ctx context.Context, q CalendarQuery) (Page[CalendarEvent], error) {
	params := map[string]any{"calendarId": "primary", "maxResults": 20, "singleEvents": true, "orderBy": "startTime"}
	if q.Search != "" {
		params["q"] = q.Search
	} else {
		params["timeMin"] = time.Now().Format(time.RFC3339)
	}
	if q.PageToken != "" {
		params["pageToken"] = q.PageToken
	}
	payload, _ := json.Marshal(params)
	var raw struct {
		Items []struct {
			ID          string                          `json:"id"`
			Summary     string                          `json:"summary"`
			Location    string                          `json:"location"`
			HangoutLink string                          `json:"hangoutLink"`
			Description string                          `json:"description"`
			Start       struct{ DateTime, Date string } `json:"start"`
			End         struct{ DateTime, Date string } `json:"end"`
			Attendees   []struct{ Email string }        `json:"attendees"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	err := c.runJSON(ctx, &raw, "calendar", "events", "list", "--params", string(payload), "--format", "json")
	if err != nil {
		return Page[CalendarEvent]{}, err
	}
	items := make([]CalendarEvent, 0, len(raw.Items))
	for _, item := range raw.Items {
		start := parseGoogleTime(item.Start.DateTime, item.Start.Date)
		end := parseGoogleTime(item.End.DateTime, item.End.Date)
		attendees := make([]string, 0, len(item.Attendees))
		for _, attendee := range item.Attendees {
			attendees = append(attendees, attendee.Email)
		}
		items = append(items, CalendarEvent{
			ID: item.ID, Summary: item.Summary, Start: start, End: end, Location: item.Location,
			HangoutLink: item.HangoutLink, Attendees: attendees, Description: item.Description,
		})
	}
	return Page[CalendarEvent]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) QuickAddEvent(ctx context.Context, text string) (CalendarEvent, error) {
	var raw struct {
		ID          string                          `json:"id"`
		Summary     string                          `json:"summary"`
		Location    string                          `json:"location"`
		HangoutLink string                          `json:"hangoutLink"`
		Description string                          `json:"description"`
		Start       struct{ DateTime, Date string } `json:"start"`
		End         struct{ DateTime, Date string } `json:"end"`
		Attendees   []struct{ Email string }        `json:"attendees"`
	}
	params, _ := json.Marshal(map[string]string{"calendarId": "primary", "text": text})
	err := c.runJSON(ctx, &raw, "calendar", "events", "quickAdd", "--params", string(params), "--format", "json")
	if err != nil {
		return CalendarEvent{}, err
	}
	attendees := make([]string, 0, len(raw.Attendees))
	for _, attendee := range raw.Attendees {
		attendees = append(attendees, attendee.Email)
	}
	return CalendarEvent{
		ID: raw.ID, Summary: raw.Summary, Start: parseGoogleTime(raw.Start.DateTime, raw.Start.Date), End: parseGoogleTime(raw.End.DateTime, raw.End.Date),
		Location: raw.Location, HangoutLink: raw.HangoutLink, Description: raw.Description, Attendees: attendees,
	}, nil
}

func (c *CommandClient) CreateEvent(ctx context.Context, draft EventDraft) (CalendarEvent, error) {
	body := map[string]any{
		"summary": draft.Summary,
		"start":   map[string]string{"dateTime": draft.Start.Format(time.RFC3339)},
		"end":     map[string]string{"dateTime": draft.End.Format(time.RFC3339)},
	}
	if draft.Location != "" {
		body["location"] = draft.Location
	}
	if draft.Description != "" {
		body["description"] = draft.Description
	}
	if len(draft.Attendees) > 0 {
		attendees := make([]map[string]string, 0, len(draft.Attendees))
		for _, email := range draft.Attendees {
			attendees = append(attendees, map[string]string{"email": email})
		}
		body["attendees"] = attendees
	}
	payload, _ := json.Marshal(body)
	var raw CalendarEvent
	err := c.runJSON(ctx, &raw, "calendar", "events", "insert", "--params", `{"calendarId":"primary","sendUpdates":"none"}`, "--json", string(payload), "--format", "json")
	return raw, err
}

func (c *CommandClient) RSVPEvent(context.Context, string, string) (CalendarEvent, error) {
	return CalendarEvent{}, errors.New("rsvp not wired")
}
func (c *CommandClient) DeleteEvent(context.Context, string) error {
	return errors.New("delete not wired")
}

func (c *CommandClient) MeetSpaces(ctx context.Context) (Page[MeetSpace], error) {
	var raw struct {
		Spaces        []MeetSpace `json:"spaces"`
		Items         []MeetSpace `json:"items"`
		NextPageToken string      `json:"nextPageToken"`
	}
	err := c.runJSON(ctx, &raw, "meet", "spaces", "list", "--format", "json")
	if err != nil {
		return Page[MeetSpace]{}, err
	}
	items := raw.Spaces
	if len(items) == 0 {
		items = raw.Items
	}
	return Page[MeetSpace]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) CreateMeetSpace(ctx context.Context, title string) (MeetSpace, error) {
	var raw MeetSpace
	err := c.runJSON(ctx, &raw, "meet", "spaces", "create", "--json", "{}", "--format", "json")
	return raw, err
}

func (c *CommandClient) EndMeetSpace(context.Context, string) error {
	return errors.New("end meet not wired")
}
func (c *CommandClient) Close() error { return nil }

func (c *CommandClient) runJSON(ctx context.Context, out any, args ...string) error {
	if c.path == "" {
		return errors.New("gws path is empty")
	}
	command := exec.CommandContext(ctx, c.path, args...)
	payload, err := command.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return err
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func defaultMailLabels() []MailLabel {
	return []MailLabel{
		{Name: "Inbox", LabelIDs: []string{"INBOX"}},
		{Name: "Unread", LabelIDs: []string{"UNREAD"}},
		{Name: "Starred", LabelIDs: []string{"STARRED"}},
		{Name: "Important", LabelIDs: []string{"IMPORTANT"}},
		{Name: "Sent", LabelIDs: []string{"SENT"}},
		{Name: "Drafts", LabelIDs: []string{"DRAFT"}},
		{Name: "Spam", LabelIDs: []string{"SPAM"}, IncludeSpamTrash: true},
		{Name: "Trash", LabelIDs: []string{"TRASH"}, IncludeSpamTrash: true},
		{Name: "All Mail", Query: "-in:spam -in:trash"},
	}
}

func parseGoogleTime(dateTime, date string) time.Time {
	if dateTime != "" {
		if parsed, err := time.Parse(time.RFC3339, dateTime); err == nil {
			return parsed
		}
	}
	if date != "" {
		if parsed, err := time.Parse("2006-01-02", date); err == nil {
			return parsed
		}
	}
	return time.Now()
}

func lastSegment(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "/")
	return parts[len(parts)-1]
}

func fallback(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
