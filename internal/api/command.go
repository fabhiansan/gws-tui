package api

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CommandClient struct {
	path     string
	subMu    sync.Mutex
	lastSeen map[string]time.Time
}

type rawChatAttachment struct {
	Name              string `json:"name"`
	ContentName       string `json:"contentName"`
	ContentType       string `json:"contentType"`
	ThumbnailURI      string `json:"thumbnailUri"`
	DownloadURI       string `json:"downloadUri"`
	AttachmentDataRef struct {
		ResourceName string `json:"resourceName"`
	} `json:"attachmentDataRef"`
}

type rawGmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type rawGmailPart struct {
	Filename string           `json:"filename"`
	MimeType string           `json:"mimeType"`
	Headers  []rawGmailHeader `json:"headers"`
	Body     struct {
		AttachmentID string `json:"attachmentId"`
		Data         string `json:"data"`
		Size         int    `json:"size"`
	} `json:"body"`
	Parts []rawGmailPart `json:"parts"`
}

type rawGmailMessage struct {
	ID           string       `json:"id"`
	ThreadID     string       `json:"threadId"`
	LabelIDs     []string     `json:"labelIds"`
	Snippet      string       `json:"snippet"`
	InternalDate string       `json:"internalDate"`
	Payload      rawGmailPart `json:"payload"`
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
	params := map[string]any{"parent": spaceName, "pageSize": 50, "orderBy": "createTime DESC"}
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
			Attachment  []rawChatAttachment `json:"attachment"`
			Attachments []rawChatAttachment `json:"attachments"`
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
		attachments := MergeAttachments(
			chatAttachments(msg.Attachment),
			chatAttachments(msg.Attachments),
			ImageAttachmentsFromText(text),
		)
		items = append(items, ChatMessage{
			ID:          lastSegment(msg.Name),
			Name:        msg.Name,
			Space:       spaceName,
			SenderID:    msg.Sender.Name,
			SenderName:  fallback(msg.Sender.DisplayName, msg.Sender.Name),
			Text:        text,
			Attachments: attachments,
			CreateTime:  created,
			ThreadID:    msg.Thread.Name,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreateTime.Before(items[j].CreateTime)
	})
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
	bodyText := fallback(raw.Text, text)
	return ChatMessage{ID: lastSegment(raw.Name), Name: raw.Name, Space: spaceName, SenderID: raw.Sender.Name, SenderName: fallback(raw.Sender.DisplayName, "You"), Text: bodyText, Attachments: ImageAttachmentsFromText(bodyText), CreateTime: created}, nil
}

// SubscribeChat opens a long-running stream of new chat messages for the given
// space. When `GWS_EVENTS_PROJECT` or `GWS_EVENTS_SUBSCRIPTION` is configured,
// it spawns `gws events +subscribe` and forwards CloudEvents NDJSON as they
// arrive; otherwise it falls back to a 5-second polling loop so environments
// without Pub/Sub plumbing keep working.
func (c *CommandClient) SubscribeChat(ctx context.Context, spaceName string) (<-chan ChatMessage, error) {
	if spaceName == "" {
		return nil, errors.New("space name required")
	}
	project := strings.TrimSpace(os.Getenv("GWS_EVENTS_PROJECT"))
	subscription := strings.TrimSpace(os.Getenv("GWS_EVENTS_SUBSCRIPTION"))
	if project != "" || subscription != "" {
		return c.subscribeChatStream(ctx, spaceName, project, subscription), nil
	}
	return c.subscribeChatPoll(ctx, spaceName), nil
}

func (c *CommandClient) subscribeChatPoll(ctx context.Context, spaceName string) <-chan ChatMessage {
	ch := make(chan ChatMessage, 1)
	c.subMu.Lock()
	if c.lastSeen == nil {
		c.lastSeen = make(map[string]time.Time)
	}
	last, ok := c.lastSeen[spaceName]
	if !ok {
		last = time.Now()
		c.lastSeen[spaceName] = last
	}
	c.subMu.Unlock()
	go func() {
		defer close(ch)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				page, err := c.ChatMessages(ctx, spaceName, "")
				if err != nil {
					continue
				}
				for _, msg := range page.Items {
					if !msg.CreateTime.After(last) {
						continue
					}
					c.subMu.Lock()
					if msg.CreateTime.After(c.lastSeen[spaceName]) {
						c.lastSeen[spaceName] = msg.CreateTime
					}
					c.subMu.Unlock()
					select {
					case ch <- msg:
					case <-ctx.Done():
					}
					return
				}
			}
		}
	}()
	return ch
}

func (c *CommandClient) subscribeChatStream(ctx context.Context, spaceName, project, subscription string) <-chan ChatMessage {
	ch := make(chan ChatMessage, 16)
	go func() {
		defer close(ch)
		backoff := time.Second
		for {
			if ctx.Err() != nil {
				return
			}
			err := c.runChatEventStream(ctx, spaceName, project, subscription, ch)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				if backoff < 30*time.Second {
					backoff *= 2
					if backoff > 30*time.Second {
						backoff = 30 * time.Second
					}
				}
				continue
			}
			backoff = time.Second
			// Stream exited cleanly (helper finished a batch). Re-subscribe.
		}
	}()
	return ch
}

func (c *CommandClient) runChatEventStream(ctx context.Context, spaceName, project, subscription string, out chan<- ChatMessage) error {
	if c.path == "" {
		return errors.New("gws path is empty")
	}
	args := []string{
		"events", "+subscribe",
		"--target", "//chat.googleapis.com/" + spaceName,
		"--event-types", "google.workspace.chat.message.v1.created",
		"--format", "json",
	}
	if subscription != "" {
		args = append(args, "--subscription", subscription)
	} else {
		args = append(args, "--project", project)
	}

	cmd := exec.CommandContext(ctx, c.path, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = cmd.Wait() }()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		msg, ok := parseChatCloudEvent(scanner.Bytes(), spaceName)
		if !ok {
			continue
		}
		select {
		case out <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

// parseChatCloudEvent decodes a single CloudEvent NDJSON line emitted by
// `gws events +subscribe` for the Workspace Chat API. It returns the
// corresponding ChatMessage when the event carries an inline message payload.
func parseChatCloudEvent(line []byte, defaultSpace string) (ChatMessage, bool) {
	line = bytesTrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return ChatMessage{}, false
	}
	var env struct {
		Type    string `json:"type"`
		Subject string `json:"subject"`
		Data    struct {
			Message struct {
				Name       string `json:"name"`
				Text       string `json:"text"`
				CreateTime string `json:"createTime"`
				Sender     struct {
					Name        string `json:"name"`
					DisplayName string `json:"displayName"`
				} `json:"sender"`
				Space struct {
					Name string `json:"name"`
				} `json:"space"`
				Attachments []rawChatAttachment `json:"attachments"`
			} `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		return ChatMessage{}, false
	}
	raw := env.Data.Message
	if raw.Name == "" {
		return ChatMessage{}, false
	}
	space := raw.Space.Name
	if space == "" {
		if i := strings.Index(raw.Name, "/messages/"); i > 0 {
			space = raw.Name[:i]
		}
	}
	if space == "" {
		space = defaultSpace
	}
	created, _ := time.Parse(time.RFC3339, raw.CreateTime)
	attachments := make([]Attachment, 0, len(raw.Attachments))
	for _, a := range raw.Attachments {
		attachments = append(attachments, Attachment{
			ID:           lastSegment(a.Name),
			ResourceName: a.AttachmentDataRef.ResourceName,
			Name:         a.ContentName,
			ContentType:  a.ContentType,
			DownloadURL:  a.DownloadURI,
			ThumbnailURL: a.ThumbnailURI,
		})
	}
	attachments = append(attachments, ImageAttachmentsFromText(raw.Text)...)
	return ChatMessage{
		ID:          lastSegment(raw.Name),
		Name:        raw.Name,
		Space:       space,
		SenderID:    raw.Sender.Name,
		SenderName:  fallback(raw.Sender.DisplayName, raw.Sender.Name),
		Text:        raw.Text,
		Attachments: attachments,
		CreateTime:  created,
	}, true
}

func bytesTrimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n') {
		b = b[1:]
	}
	for len(b) > 0 {
		last := b[len(b)-1]
		if last != ' ' && last != '\t' && last != '\r' && last != '\n' {
			break
		}
		b = b[:len(b)-1]
	}
	return b
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

func chatAttachments(raw []rawChatAttachment) []Attachment {
	attachments := make([]Attachment, 0, len(raw))
	for _, item := range raw {
		id := item.Name
		if id == "" {
			id = item.AttachmentDataRef.ResourceName
		}
		attachments = append(attachments, Attachment{
			ID:           id,
			ResourceName: item.AttachmentDataRef.ResourceName,
			Name:         item.ContentName,
			ContentType:  item.ContentType,
			ThumbnailURL: item.ThumbnailURI,
			DownloadURL:  item.DownloadURI,
		})
	}
	return NormalizeAttachments(attachments)
}

func (c *CommandClient) DownloadAttachment(ctx context.Context, attachment Attachment, outputPath string) error {
	resourceName := attachment.MediaResourceName()
	if resourceName == "" {
		return errors.New("attachment media resource is missing")
	}
	if outputPath == "" {
		return errors.New("output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(outputPath), ".gws-media-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	params, _ := json.Marshal(map[string]string{"resourceName": resourceName, "alt": "media"})
	command := exec.CommandContext(ctx, c.path, "chat", "media", "download", "--params", string(params), "--output", filepath.Base(tmpPath))
	command.Dir = filepath.Dir(tmpPath)
	payload, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gws media download failed: %s", strings.TrimSpace(string(payload)))
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return err
	}
	return nil
}

func flattenGmailHeaders(headers []rawGmailHeader) map[string]string {
	out := make(map[string]string, len(headers))
	for _, h := range headers {
		out[strings.ToLower(h.Name)] = h.Value
	}
	return out
}

func parseFromHeader(value string) (name, email string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	if addr, err := mail.ParseAddress(value); err == nil {
		name = addr.Name
		if name == "" {
			name = addr.Address
		}
		return name, addr.Address
	}
	if lt := strings.LastIndex(value, "<"); lt >= 0 {
		if gt := strings.LastIndex(value, ">"); gt > lt {
			email = strings.TrimSpace(value[lt+1 : gt])
			name = strings.Trim(strings.TrimSpace(value[:lt]), "\"")
			if name == "" {
				name = email
			}
			return
		}
	}
	return value, value
}

func parseGmailDate(internal, header string) time.Time {
	if internal != "" {
		if ms, err := strconv.ParseInt(internal, 10, 64); err == nil {
			return time.UnixMilli(ms)
		}
	}
	if header != "" {
		if t, err := mail.ParseDate(header); err == nil {
			return t
		}
	}
	return time.Now()
}

func gmailBody(part rawGmailPart) string {
	mime := strings.ToLower(part.MimeType)
	if strings.HasPrefix(mime, "text/plain") {
		if decoded := decodeGmailData(part.Body.Data); decoded != "" {
			return decoded
		}
	}
	var plain, html string
	for _, child := range part.Parts {
		body := gmailBody(child)
		if body == "" {
			continue
		}
		childMime := strings.ToLower(child.MimeType)
		switch {
		case plain == "" && strings.HasPrefix(childMime, "text/plain"):
			plain = body
		case html == "" && strings.HasPrefix(childMime, "text/html"):
			html = body
		case plain == "" && html == "":
			plain = body
		}
	}
	if plain != "" {
		return plain
	}
	if html != "" {
		return html
	}
	if strings.HasPrefix(mime, "text/") {
		return decodeGmailData(part.Body.Data)
	}
	return ""
}

func decodeGmailData(data string) string {
	if data == "" {
		return ""
	}
	if decoded, err := base64.URLEncoding.DecodeString(data); err == nil {
		return string(decoded)
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(data); err == nil {
		return string(decoded)
	}
	if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
		return string(decoded)
	}
	return ""
}

func containsLabel(labels []string, target string) bool {
	for _, label := range labels {
		if label == target {
			return true
		}
	}
	return false
}

func gmailAttachments(root rawGmailPart) []Attachment {
	var attachments []Attachment
	var walk func(rawGmailPart)
	walk = func(part rawGmailPart) {
		if part.Filename != "" || strings.HasPrefix(strings.ToLower(part.MimeType), "image/") {
			attachments = append(attachments, Attachment{
				ID:          part.Body.AttachmentID,
				Name:        part.Filename,
				ContentType: part.MimeType,
			})
		}
		for _, child := range part.Parts {
			walk(child)
		}
	}
	walk(root)
	return NormalizeAttachments(attachments)
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
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "gmail", "users", "messages", "list", "--params", string(payload), "--format", "json"); err != nil {
		return Page[MailThread]{}, err
	}

	items := make([]MailThread, len(raw.Messages))
	const maxConcurrent = 6
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	for i, msg := range raw.Messages {
		i, msg := i, msg
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			thread, err := c.fetchMailMessage(ctx, msg.ID, msg.ThreadID)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			items[i] = thread
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return Page[MailThread]{}, firstErr
	}
	return Page[MailThread]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) fetchMailMessage(ctx context.Context, messageID, threadID string) (MailThread, error) {
	params, _ := json.Marshal(map[string]any{
		"userId": "me",
		"id":     messageID,
		"format": "full",
	})
	var raw rawGmailMessage
	if err := c.runJSON(ctx, &raw, "gmail", "users", "messages", "get", "--params", string(params), "--format", "json"); err != nil {
		return MailThread{}, err
	}
	headers := flattenGmailHeaders(raw.Payload.Headers)
	senderName, senderEmail := parseFromHeader(headers["from"])
	body := gmailBody(raw.Payload)
	snippet := raw.Snippet
	if snippet == "" {
		snippet = firstLine(body)
	}
	return MailThread{
		ID:          fallback(threadID, raw.ThreadID),
		Sender:      fallback(senderName, fallback(senderEmail, "(unknown)")),
		SenderEmail: senderEmail,
		Subject:     fallback(headers["subject"], "(no subject)"),
		Snippet:     snippet,
		Date:        parseGmailDate(raw.InternalDate, headers["date"]),
		Body:        body,
		Attachments: MergeAttachments(gmailAttachments(raw.Payload), ImageAttachmentsFromText(body)),
		Unread:      containsLabel(raw.LabelIDs, "UNREAD"),
		Starred:     containsLabel(raw.LabelIDs, "STARRED"),
		Labels:      raw.LabelIDs,
	}, nil
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

func (c *CommandClient) MeetSpaces(_ context.Context) (Page[MeetSpace], error) {
	// gws CLI exposes only create/get/endActiveConference/patch on meet
	// spaces — there is no `list` endpoint in the Meet API v2. Spaces
	// created during the session are tracked client-side instead.
	return Page[MeetSpace]{}, nil
}

func (c *CommandClient) CreateMeetSpace(ctx context.Context, _ string) (MeetSpace, error) {
	var raw MeetSpace
	err := c.runJSON(ctx, &raw, "meet", "spaces", "create", "--json", "{}", "--format", "json")
	return raw, err
}

func (c *CommandClient) EndMeetSpace(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("meet space name is required")
	}
	params, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}
	return c.runVoid(ctx, "meet", "spaces", "endActiveConference", "--params", string(params), "--format", "json")
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

// runVoid runs a gws subcommand and ignores its stdout. Use for endpoints
// that return google.protobuf.Empty (e.g. endActiveConference) where parsing
// the response would fail or yield no useful data.
func (c *CommandClient) runVoid(ctx context.Context, args ...string) error {
	if c.path == "" {
		return errors.New("gws path is empty")
	}
	command := exec.CommandContext(ctx, c.path, args...)
	if _, err := command.Output(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return err
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
