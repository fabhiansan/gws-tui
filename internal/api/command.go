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

type rawChatMessage struct {
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
	CardsV2     json.RawMessage     `json:"cardsV2"`
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

type rawGmailThread struct {
	ID       string            `json:"id"`
	Messages []rawGmailMessage `json:"messages"`
}

type rawGmailDraft struct {
	ID      string          `json:"id"`
	Message rawGmailMessage `json:"message"`
}

type rawCalendarDateTime struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
}

type rawCalendarAttendee struct {
	Email          string `json:"email"`
	ResponseStatus string `json:"responseStatus,omitempty"`
}

type rawCalendarEvent struct {
	ID          string                `json:"id"`
	Summary     string                `json:"summary"`
	Location    string                `json:"location"`
	HangoutLink string                `json:"hangoutLink"`
	Description string                `json:"description"`
	Start       rawCalendarDateTime   `json:"start"`
	End         rawCalendarDateTime   `json:"end"`
	Attendees   []rawCalendarAttendee `json:"attendees"`
}

type rawCalendarListItem struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Primary     bool   `json:"primary"`
	AccessRole  string `json:"accessRole"`
}

type rawTaskList struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated string `json:"updated"`
}

type rawTaskItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Notes     string `json:"notes"`
	Status    string `json:"status"`
	Due       string `json:"due"`
	Completed string `json:"completed"`
	Updated   string `json:"updated"`
	Parent    string `json:"parent"`
	Position  string `json:"position"`
}

type rawDriveFile struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	MimeType     string   `json:"mimeType"`
	ModifiedTime string   `json:"modifiedTime"`
	WebViewLink  string   `json:"webViewLink"`
	Size         string   `json:"size"`
	Parents      []string `json:"parents"`
}

type rawMeetConferenceRecord struct {
	Name       string `json:"name"`
	Space      string `json:"space"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
	ExpireTime string `json:"expireTime"`
}

type rawMeetParticipant struct {
	Name              string `json:"name"`
	EarliestStartTime string `json:"earliestStartTime"`
	LatestEndTime     string `json:"latestEndTime"`
	SignedinUser      struct {
		User        string `json:"user"`
		DisplayName string `json:"displayName"`
	} `json:"signedinUser"`
	AnonymousUser struct {
		DisplayName string `json:"displayName"`
	} `json:"anonymousUser"`
	PhoneUser struct {
		DisplayName string `json:"displayName"`
	} `json:"phoneUser"`
}

type rawMeetArtifact struct {
	Name             string `json:"name"`
	State            string `json:"state"`
	StartTime        string `json:"startTime"`
	EndTime          string `json:"endTime"`
	DriveDestination struct {
		File string `json:"file"`
	} `json:"driveDestination"`
	DocsDestination struct {
		Document string `json:"document"`
	} `json:"docsDestination"`
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
		Messages      []rawChatMessage `json:"messages"`
		NextPageToken string           `json:"nextPageToken"`
	}
	err := c.runJSON(ctx, &raw, "chat", "spaces", "messages", "list", "--params", string(payload), "--format", "json")
	if err != nil {
		return Page[ChatMessage]{}, err
	}
	items := make([]ChatMessage, 0, len(raw.Messages))
	for _, msg := range raw.Messages {
		items = append(items, chatMessageFromRaw(msg, spaceName))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreateTime.Before(items[j].CreateTime)
	})
	linkThreadReplies(items)
	return Page[ChatMessage]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func chatMessageFromRaw(raw rawChatMessage, defaultSpace string) ChatMessage {
	created, _ := time.Parse(time.RFC3339, raw.CreateTime)
	text := raw.Text
	if text == "" {
		text = raw.Argument
	}
	attachments := MergeAttachments(
		chatAttachments(raw.Attachment),
		chatAttachments(raw.Attachments),
		ImageAttachmentsFromText(text),
	)
	space := defaultSpace
	if space == "" {
		space = spaceFromChatMessageName(raw.Name)
	}
	return ChatMessage{
		ID:          lastSegment(raw.Name),
		Name:        raw.Name,
		Space:       space,
		SenderID:    raw.Sender.Name,
		SenderName:  fallback(raw.Sender.DisplayName, raw.Sender.Name),
		Text:        text,
		Attachments: attachments,
		Cards:       decodeCards(raw.CardsV2),
		CreateTime:  created,
		ThreadID:    raw.Thread.Name,
	}
}

func spaceFromChatMessageName(name string) string {
	if idx := strings.Index(name, "/messages/"); idx > 0 {
		return name[:idx]
	}
	return ""
}

// linkThreadReplies marks every message after the first in a given thread as a
// reply by setting ParentID to the starter's ID. Walks in chronological order
// so the earliest message per thread wins the starter slot. Messages whose
// starter falls outside this page get a structural fallback: Google Chat
// thread-starter message IDs share their prefix with the thread key
// (e.g. thread "spaces/X/threads/AAA" → starter message ID "AAA" or "AAA.AAA").
func linkThreadReplies(items []ChatMessage) {
	starter := make(map[string]string, len(items))
	for i := range items {
		thread := items[i].ThreadID
		if thread == "" {
			continue
		}
		if id, ok := starter[thread]; ok {
			items[i].ParentID = id
			continue
		}
		if isThreadStarter(items[i].ID, thread) {
			starter[thread] = items[i].ID
			continue
		}
		items[i].ParentID = lastSegment(thread)
		starter[thread] = lastSegment(thread)
	}
}

func isThreadStarter(messageID, threadName string) bool {
	key := lastSegment(threadName)
	if key == "" || messageID == "" {
		return true
	}
	if messageID == key {
		return true
	}
	prefix, _, ok := strings.Cut(messageID, ".")
	return ok && prefix == key
}

func (c *CommandClient) SendChatMessage(ctx context.Context, spaceName, text, threadID string, attachments []LocalAttachment) (ChatMessage, error) {
	var raw rawChatMessage
	uploaded := make([]map[string]any, 0, len(attachments))
	for _, att := range attachments {
		ref, err := c.uploadChatAttachment(ctx, spaceName, att)
		if err != nil {
			return ChatMessage{}, fmt.Errorf("upload %s: %w", att.Path, err)
		}
		// Prefer the upload token — that's what fresh uploads return and
		// what messages.create expects. resourceName works too but is
		// only present after the upload has been referenced elsewhere.
		dataRef := map[string]any{}
		if ref.UploadToken != "" {
			dataRef["attachmentUploadToken"] = ref.UploadToken
		} else {
			dataRef["resourceName"] = ref.ResourceName
		}
		entry := map[string]any{"attachmentDataRef": dataRef}
		if att.ContentType != "" {
			entry["contentType"] = att.ContentType
		}
		if att.Name != "" {
			entry["contentName"] = att.Name
		}
		uploaded = append(uploaded, entry)
	}
	params := map[string]any{"parent": spaceName}
	bodyMap := map[string]any{"text": text}
	if len(uploaded) > 0 {
		bodyMap["attachment"] = uploaded
	}
	if threadID != "" {
		bodyMap["thread"] = map[string]any{"name": threadID}
		params["messageReplyOption"] = "REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD"
	}
	paramsJSON, _ := json.Marshal(params)
	body, _ := json.Marshal(bodyMap)
	err := c.runJSON(ctx, &raw, "chat", "spaces", "messages", "create", "--params", string(paramsJSON), "--json", string(body), "--format", "json")
	if err != nil {
		return ChatMessage{}, err
	}
	created, _ := time.Parse(time.RFC3339, raw.CreateTime)
	bodyText := fallback(raw.Text, text)
	parent := ""
	if threadID != "" {
		parent = lastSegment(threadID)
	}
	serverAttachments := MergeAttachments(
		chatAttachments(raw.Attachment),
		chatAttachments(raw.Attachments),
	)
	// Order matches the request body, which is the order of `attachments`
	// here. Stamp each server-returned attachment with the temp file we
	// just uploaded so the inline renderer can render without a roundtrip.
	for i := range serverAttachments {
		if i < len(attachments) && attachments[i].Path != "" {
			serverAttachments[i].LocalPath = attachments[i].Path
			if serverAttachments[i].ContentType == "" {
				serverAttachments[i].ContentType = attachments[i].ContentType
			}
			if serverAttachments[i].Name == "" {
				serverAttachments[i].Name = attachments[i].Name
			}
		}
	}
	// If upstream omitted the attachment list entirely (older builds), fall
	// back to synthesizing entries from the local files so the bubble still
	// shows what the user just sent.
	if len(serverAttachments) == 0 && len(attachments) > 0 {
		for _, att := range attachments {
			serverAttachments = append(serverAttachments, Attachment{
				LocalPath:   att.Path,
				ContentType: att.ContentType,
				Name:        att.Name,
			})
		}
	}
	mergedAttachments := MergeAttachments(serverAttachments, ImageAttachmentsFromText(bodyText))
	return ChatMessage{
		ID:          lastSegment(raw.Name),
		Name:        raw.Name,
		Space:       spaceName,
		SenderID:    raw.Sender.Name,
		SenderName:  fallback(raw.Sender.DisplayName, "You"),
		Text:        bodyText,
		Attachments: mergedAttachments,
		CreateTime:  created,
		ThreadID:    fallback(raw.Thread.Name, threadID),
		ParentID:    parent,
	}, nil
}

func (c *CommandClient) EditChatMessage(ctx context.Context, messageName, text string) (ChatMessage, error) {
	if strings.TrimSpace(messageName) == "" {
		return ChatMessage{}, errors.New("message name is required")
	}
	params, _ := json.Marshal(map[string]string{"name": messageName, "updateMask": "text"})
	body, _ := json.Marshal(map[string]string{"text": text})
	var raw rawChatMessage
	if err := c.runJSON(ctx, &raw, "chat", "spaces", "messages", "patch", "--params", string(params), "--json", string(body), "--format", "json"); err != nil {
		return ChatMessage{}, err
	}
	msg := chatMessageFromRaw(raw, spaceFromChatMessageName(messageName))
	if msg.Text == "" {
		msg.Text = text
	}
	return msg, nil
}

func (c *CommandClient) DeleteChatMessage(ctx context.Context, messageName string) error {
	if strings.TrimSpace(messageName) == "" {
		return errors.New("message name is required")
	}
	params, _ := json.Marshal(map[string]string{"name": messageName})
	return c.runVoid(ctx, "chat", "spaces", "messages", "delete", "--params", string(params), "--format", "json")
}

func (c *CommandClient) CreateChatSpace(ctx context.Context, displayName string) (Space, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return Space{}, errors.New("space display name is required")
	}
	body, _ := json.Marshal(map[string]string{"displayName": displayName, "spaceType": "SPACE"})
	var raw Space
	if err := c.runJSON(ctx, &raw, "chat", "spaces", "create", "--params", `{}`, "--json", string(body), "--format", "json"); err != nil {
		return Space{}, err
	}
	if raw.DisplayName == "" {
		raw.DisplayName = displayName
	}
	return raw, nil
}

func (c *CommandClient) SetupChatSpace(ctx context.Context, displayName string, members []string) (Space, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return Space{}, errors.New("space display name is required")
	}
	if len(members) == 0 {
		return c.CreateChatSpace(ctx, displayName)
	}
	memberships := make([]map[string]any, 0, len(members))
	for _, member := range members {
		member = strings.TrimSpace(member)
		if member == "" {
			continue
		}
		if !strings.HasPrefix(member, "users/") {
			member = "users/" + member
		}
		memberships = append(memberships, map[string]any{"member": map[string]string{"name": member, "type": "HUMAN"}})
	}
	if len(memberships) == 0 {
		return c.CreateChatSpace(ctx, displayName)
	}
	body, _ := json.Marshal(map[string]any{
		"space":       map[string]string{"displayName": displayName, "spaceType": "SPACE"},
		"memberships": memberships,
	})
	var raw Space
	if err := c.runJSON(ctx, &raw, "chat", "spaces", "setup", "--params", `{}`, "--json", string(body), "--format", "json"); err != nil {
		return Space{}, err
	}
	if raw.DisplayName == "" {
		raw.DisplayName = displayName
	}
	return raw, nil
}

func (c *CommandClient) AddChatReaction(ctx context.Context, messageName, emoji string) (string, error) {
	if strings.TrimSpace(messageName) == "" {
		return "", errors.New("message name is required")
	}
	emoji = strings.TrimSpace(emoji)
	if emoji == "" {
		return "", errors.New("emoji is required")
	}
	params, _ := json.Marshal(map[string]string{"parent": messageName})
	body, _ := json.Marshal(map[string]any{"emoji": map[string]string{"unicode": emoji}})
	var raw struct {
		Name string `json:"name"`
	}
	if err := c.runJSON(ctx, &raw, "chat", "spaces", "messages", "reactions", "create", "--params", string(params), "--json", string(body), "--format", "json"); err != nil {
		return "", err
	}
	if raw.Name == "" {
		return "", errors.New("reaction response missing name")
	}
	return raw.Name, nil
}

func (c *CommandClient) DeleteChatReaction(ctx context.Context, reactionName string) error {
	if strings.TrimSpace(reactionName) == "" {
		return errors.New("reaction name is required")
	}
	params, _ := json.Marshal(map[string]string{"name": reactionName})
	return c.runVoid(ctx, "chat", "spaces", "messages", "reactions", "delete", "--params", string(params), "--format", "json")
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
				CardsV2     json.RawMessage     `json:"cardsV2"`
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
		Cards:       decodeCards(raw.CardsV2),
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

// uploadedAttachmentRef captures whichever pointer the API gave us back for a
// freshly uploaded attachment. Fresh uploads return an attachmentUploadToken
// that messages.create consumes directly; resourceName only appears once a
// message references the upload. We carry both so the caller picks the right
// field when building the create-message body.
type uploadedAttachmentRef struct {
	UploadToken  string
	ResourceName string
}

// uploadChatAttachment pushes a local file through `chat media upload` and
// returns the upload reference the API hands back. messages.create needs that
// ref to embed the upload as a real attachment on the next message.
func (c *CommandClient) uploadChatAttachment(ctx context.Context, spaceName string, att LocalAttachment) (uploadedAttachmentRef, error) {
	var empty uploadedAttachmentRef
	if att.Path == "" {
		return empty, errors.New("attachment path is empty")
	}
	if spaceName == "" {
		return empty, errors.New("space name is empty")
	}
	if c.path == "" {
		return empty, errors.New("gws path is empty")
	}
	filename := att.Name
	if filename == "" {
		filename = filepath.Base(att.Path)
	}
	params, _ := json.Marshal(map[string]string{"parent": spaceName})
	body, _ := json.Marshal(map[string]any{
		"filename": filename,
	})
	// Upstream rejects --upload paths that resolve outside the current
	// working directory. Run from the file's folder and pass only the
	// basename so the guard sees a cwd-relative path. DownloadAttachment
	// applies the same pattern.
	args := []string{"chat", "media", "upload",
		"--params", string(params),
		"--json", string(body),
		"--upload", filepath.Base(att.Path),
	}
	if att.ContentType != "" {
		args = append(args, "--upload-content-type", att.ContentType)
	}
	args = append(args, "--format", "json")
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Dir = filepath.Dir(att.Path)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		if stderr != "" {
			return empty, fmt.Errorf("gws media upload failed: %s", stderr)
		}
		return empty, fmt.Errorf("gws media upload failed: %w", err)
	}
	// Fresh uploads return an attachmentUploadToken — that token is what
	// messages.create wants in attachmentDataRef. resourceName only exists
	// after a message has referenced the upload, so it usually isn't here.
	// Probe both (plus snake_case variants) so we tolerate upstream drift.
	var resp map[string]any
	if err := json.Unmarshal(out, &resp); err != nil {
		return empty, fmt.Errorf("decode media upload response: %w (raw=%s)", err, truncate(string(out), 400))
	}
	ref := extractAttachmentRef(resp)
	if ref.UploadToken == "" && ref.ResourceName == "" {
		return empty, fmt.Errorf("media upload response missing attachmentUploadToken/resourceName (raw=%s)", truncate(string(out), 400))
	}
	return ref, nil
}

// extractAttachmentRef walks the known response shapes for Google Chat media
// upload and returns the upload token and/or resource name. Tolerates the
// camelCase docs shape, snake_case variants some upstream builds emit, and a
// bare Attachment resource with `name`.
func extractAttachmentRef(resp map[string]any) uploadedAttachmentRef {
	var out uploadedAttachmentRef
	if resp == nil {
		return out
	}
	for _, key := range []string{"attachmentDataRef", "attachment_data_ref"} {
		nested, ok := resp[key].(map[string]any)
		if !ok {
			continue
		}
		for _, inner := range []string{"attachmentUploadToken", "attachment_upload_token"} {
			if s, ok := nested[inner].(string); ok && s != "" {
				out.UploadToken = s
				break
			}
		}
		for _, inner := range []string{"resourceName", "resource_name"} {
			if s, ok := nested[inner].(string); ok && s != "" {
				out.ResourceName = s
				break
			}
		}
		if out.UploadToken != "" || out.ResourceName != "" {
			return out
		}
	}
	for _, key := range []string{"attachmentUploadToken", "attachment_upload_token"} {
		if s, ok := resp[key].(string); ok && s != "" {
			out.UploadToken = s
			return out
		}
	}
	for _, key := range []string{"resourceName", "resource_name", "name"} {
		if s, ok := resp[key].(string); ok && s != "" {
			out.ResourceName = s
			return out
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func (c *CommandClient) DownloadAttachment(ctx context.Context, attachment Attachment, outputPath string) error {
	resourceName := attachment.MediaResourceName()
	if resourceName == "" {
		return errors.New("attachment media resource is missing")
	}
	if strings.HasPrefix(resourceName, "gmail/") {
		return c.downloadGmailAttachment(ctx, resourceName, outputPath)
	}
	if strings.HasPrefix(resourceName, "drive/files/") {
		return c.downloadDriveFile(ctx, strings.TrimPrefix(resourceName, "drive/files/"), outputPath)
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

func (c *CommandClient) downloadGmailAttachment(ctx context.Context, resourceName, outputPath string) error {
	messageID, attachmentID, ok := parseGmailAttachmentResource(resourceName)
	if !ok {
		return fmt.Errorf("invalid gmail attachment resource %q", resourceName)
	}
	if outputPath == "" {
		return errors.New("output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	params, _ := json.Marshal(map[string]string{"userId": "me", "messageId": messageID, "id": attachmentID})
	var raw struct {
		Data string `json:"data"`
		Size int    `json:"size"`
	}
	if err := c.runJSON(ctx, &raw, "gmail", "users", "messages", "attachments", "get", "--params", string(params), "--format", "json"); err != nil {
		return err
	}
	payload := decodeGmailData(raw.Data)
	if raw.Data != "" && payload == "" {
		return errors.New("gmail attachment response contained invalid data")
	}
	return os.WriteFile(outputPath, []byte(payload), 0o600)
}

func (c *CommandClient) downloadDriveFile(ctx context.Context, fileID, outputPath string) error {
	if strings.TrimSpace(fileID) == "" {
		return errors.New("drive file id is required")
	}
	if outputPath == "" {
		return errors.New("output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(outputPath), ".gws-drive-*")
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

	params, _ := json.Marshal(map[string]string{"fileId": fileID, "alt": "media"})
	command := exec.CommandContext(ctx, c.path, "drive", "files", "get", "--params", string(params), "--output", filepath.Base(tmpPath))
	command.Dir = filepath.Dir(tmpPath)
	payload, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gws drive download failed: %s", strings.TrimSpace(string(payload)))
	}
	return os.Rename(tmpPath, outputPath)
}

func parseGmailAttachmentResource(resourceName string) (messageID, attachmentID string, ok bool) {
	const prefix = "gmail/users/me/messages/"
	if !strings.HasPrefix(resourceName, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(resourceName, prefix)
	messageID, attachmentID, ok = strings.Cut(rest, "/attachments/")
	return messageID, attachmentID, ok && messageID != "" && attachmentID != ""
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

func gmailAttachments(messageID string, root rawGmailPart) []Attachment {
	var attachments []Attachment
	var walk func(rawGmailPart)
	walk = func(part rawGmailPart) {
		if part.Filename != "" || strings.HasPrefix(strings.ToLower(part.MimeType), "image/") {
			resourceName := ""
			if messageID != "" && part.Body.AttachmentID != "" {
				resourceName = "gmail/users/me/messages/" + messageID + "/attachments/" + part.Body.AttachmentID
			}
			attachments = append(attachments, Attachment{
				ID:           part.Body.AttachmentID,
				ResourceName: resourceName,
				Name:         fallback(part.Filename, part.Body.AttachmentID),
				ContentType:  part.MimeType,
			})
		}
		for _, child := range part.Parts {
			walk(child)
		}
	}
	walk(root)
	return NormalizeAttachments(attachments)
}

func (c *CommandClient) MailLabels(ctx context.Context) ([]MailLabel, error) {
	params, _ := json.Marshal(map[string]string{"userId": "me"})
	var raw struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"labels"`
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := c.runJSON(ctx, &raw, "gmail", "users", "labels", "list", "--params", string(params), "--format", "json"); err != nil {
		return nil, err
	}
	source := raw.Labels
	if len(source) == 0 {
		source = raw.Items
	}
	labels := make([]MailLabel, 0, len(source)+1)
	seen := map[string]bool{}
	for _, item := range source {
		id := strings.TrimSpace(item.ID)
		name := strings.TrimSpace(item.Name)
		if id == "" && name == "" {
			continue
		}
		if name == "" {
			name = id
		}
		if id == "" {
			id = strings.ToUpper(strings.ReplaceAll(name, " ", "_"))
		}
		seen[name] = true
		labels = append(labels, MailLabel{
			Name:             name,
			LabelIDs:         []string{id},
			IncludeSpamTrash: id == "SPAM" || id == "TRASH",
		})
	}
	if !seen["All Mail"] {
		labels = append(labels, MailLabel{Name: "All Mail", Query: "-in:spam -in:trash"})
	}
	if len(labels) == 0 {
		return defaultMailLabels(), nil
	}
	return labels, nil
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
	return mailThreadFromRawMessage(raw, threadID), nil
}

func (c *CommandClient) fetchMailThread(ctx context.Context, threadID string) (MailThread, error) {
	if strings.TrimSpace(threadID) == "" {
		return MailThread{}, errors.New("thread id is required")
	}
	params, _ := json.Marshal(map[string]any{
		"userId": "me",
		"id":     threadID,
		"format": "full",
	})
	var raw rawGmailThread
	if err := c.runJSON(ctx, &raw, "gmail", "users", "threads", "get", "--params", string(params), "--format", "json"); err != nil {
		return MailThread{}, err
	}
	if len(raw.Messages) == 0 {
		return MailThread{ID: fallback(raw.ID, threadID)}, nil
	}
	return mailThreadFromRawMessage(raw.Messages[len(raw.Messages)-1], fallback(raw.ID, threadID)), nil
}

func mailThreadFromRawMessage(raw rawGmailMessage, threadID string) MailThread {
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
		Attachments: MergeAttachments(gmailAttachments(raw.ID, raw.Payload), ImageAttachmentsFromText(body)),
		Unread:      containsLabel(raw.LabelIDs, "UNREAD"),
		Starred:     containsLabel(raw.LabelIDs, "STARRED"),
		Labels:      raw.LabelIDs,
	}
}

func (c *CommandClient) SendMail(ctx context.Context, draft MailDraft) (MailThread, error) {
	rawMessage, err := buildRFC822Message(draft)
	if err != nil {
		return MailThread{}, err
	}
	body := map[string]any{
		"raw": base64.URLEncoding.EncodeToString([]byte(rawMessage)),
	}
	if draft.ThreadID != "" {
		body["threadId"] = draft.ThreadID
	}
	payload, _ := json.Marshal(body)
	params, _ := json.Marshal(map[string]string{"userId": "me"})
	var raw struct {
		ID       string   `json:"id"`
		ThreadID string   `json:"threadId"`
		LabelIDs []string `json:"labelIds"`
	}
	if err := c.runJSON(ctx, &raw, "gmail", "users", "messages", "send", "--params", string(params), "--json", string(payload), "--format", "json"); err != nil {
		return MailThread{}, err
	}
	threadID := fallback(raw.ThreadID, fallback(draft.ThreadID, raw.ID))
	return MailThread{
		ID:      threadID,
		Sender:  "me",
		Subject: fallback(draft.Subject, "(no subject)"),
		Snippet: firstLine(draft.Body),
		Date:    time.Now(),
		Body:    draft.Body,
		Labels:  raw.LabelIDs,
		Starred: containsLabel(raw.LabelIDs, "STARRED"),
		Unread:  containsLabel(raw.LabelIDs, "UNREAD"),
	}, nil
}

func (c *CommandClient) MailDrafts(ctx context.Context, pageToken string) (Page[MailDraftItem], error) {
	params := map[string]any{"userId": "me", "maxResults": 20}
	if pageToken != "" {
		params["pageToken"] = pageToken
	}
	payload, _ := json.Marshal(params)
	var raw struct {
		Drafts        []rawGmailDraft `json:"drafts"`
		Items         []rawGmailDraft `json:"items"`
		NextPageToken string          `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "gmail", "users", "drafts", "list", "--params", string(payload), "--format", "json"); err != nil {
		return Page[MailDraftItem]{}, err
	}
	source := raw.Drafts
	if len(source) == 0 {
		source = raw.Items
	}
	items := make([]MailDraftItem, 0, len(source))
	for _, item := range source {
		items = append(items, mailDraftItemFromRaw(item.ID, item.Message))
	}
	return Page[MailDraftItem]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) CreateMailDraft(ctx context.Context, draft MailDraft) (MailDraftItem, error) {
	rawMessage, err := buildRFC822Message(draft)
	if err != nil {
		return MailDraftItem{}, err
	}
	message := map[string]any{"raw": base64.URLEncoding.EncodeToString([]byte(rawMessage))}
	if draft.ThreadID != "" {
		message["threadId"] = draft.ThreadID
	}
	body, _ := json.Marshal(map[string]any{"message": message})
	params, _ := json.Marshal(map[string]string{"userId": "me"})
	var raw struct {
		ID      string          `json:"id"`
		Message rawGmailMessage `json:"message"`
	}
	if err := c.runJSON(ctx, &raw, "gmail", "users", "drafts", "create", "--params", string(params), "--json", string(body), "--format", "json"); err != nil {
		return MailDraftItem{}, err
	}
	item := mailDraftItemFromRaw(raw.ID, raw.Message)
	if item.Subject == "" {
		item.Subject = fallback(draft.Subject, "(no subject)")
	}
	if item.To == "" {
		item.To = draft.To
	}
	if item.Snippet == "" {
		item.Snippet = firstLine(draft.Body)
	}
	return item, nil
}

func (c *CommandClient) SendMailDraft(ctx context.Context, draftID string) (MailThread, error) {
	if strings.TrimSpace(draftID) == "" {
		return MailThread{}, errors.New("draft id is required")
	}
	params, _ := json.Marshal(map[string]string{"userId": "me"})
	body, _ := json.Marshal(map[string]string{"id": draftID})
	var raw rawGmailMessage
	if err := c.runJSON(ctx, &raw, "gmail", "users", "drafts", "send", "--params", string(params), "--json", string(body), "--format", "json"); err != nil {
		return MailThread{}, err
	}
	return mailThreadFromRawMessage(raw, raw.ThreadID), nil
}

func mailDraftItemFromRaw(draftID string, raw rawGmailMessage) MailDraftItem {
	headers := flattenGmailHeaders(raw.Payload.Headers)
	return MailDraftItem{
		ID:        draftID,
		MessageID: raw.ID,
		ThreadID:  raw.ThreadID,
		To:        headers["to"],
		Subject:   headers["subject"],
		Snippet:   raw.Snippet,
		Date:      parseGmailDate(raw.InternalDate, headers["date"]),
	}
}

func (c *CommandClient) ArchiveMail(ctx context.Context, threadID string) error {
	return c.modifyMailThreadLabels(ctx, threadID, nil, []string{"INBOX"})
}

func (c *CommandClient) TrashMail(ctx context.Context, threadID string) error {
	if strings.TrimSpace(threadID) == "" {
		return errors.New("thread id is required")
	}
	params, _ := json.Marshal(map[string]string{"userId": "me", "id": threadID})
	return c.runVoid(ctx, "gmail", "users", "threads", "trash", "--params", string(params), "--format", "json")
}

func (c *CommandClient) ToggleStar(ctx context.Context, threadID string) (MailThread, error) {
	thread, err := c.fetchMailThread(ctx, threadID)
	if err != nil {
		return MailThread{}, err
	}
	if thread.Starred {
		err = c.modifyMailThreadLabels(ctx, threadID, nil, []string{"STARRED"})
		thread.Starred = false
		thread.Labels = removeLabel(thread.Labels, "STARRED")
	} else {
		err = c.modifyMailThreadLabels(ctx, threadID, []string{"STARRED"}, nil)
		thread.Starred = true
		if !containsLabel(thread.Labels, "STARRED") {
			thread.Labels = append(thread.Labels, "STARRED")
		}
	}
	if err != nil {
		return MailThread{}, err
	}
	if thread.ID == "" {
		thread.ID = threadID
	}
	return thread, nil
}

func (c *CommandClient) SetMailUnread(ctx context.Context, threadID string, unread bool) (MailThread, error) {
	thread, err := c.fetchMailThread(ctx, threadID)
	if err != nil {
		return MailThread{}, err
	}
	if unread {
		err = c.modifyMailThreadLabels(ctx, threadID, []string{"UNREAD"}, nil)
		thread.Unread = true
		if !containsLabel(thread.Labels, "UNREAD") {
			thread.Labels = append(thread.Labels, "UNREAD")
		}
	} else {
		err = c.modifyMailThreadLabels(ctx, threadID, nil, []string{"UNREAD"})
		thread.Unread = false
		thread.Labels = removeLabel(thread.Labels, "UNREAD")
	}
	if err != nil {
		return MailThread{}, err
	}
	if thread.ID == "" {
		thread.ID = threadID
	}
	return thread, nil
}

func (c *CommandClient) modifyMailThreadLabels(ctx context.Context, threadID string, add, remove []string) error {
	if strings.TrimSpace(threadID) == "" {
		return errors.New("thread id is required")
	}
	body := map[string]any{}
	if len(add) > 0 {
		body["addLabelIds"] = add
	}
	if len(remove) > 0 {
		body["removeLabelIds"] = remove
	}
	payload, _ := json.Marshal(body)
	params, _ := json.Marshal(map[string]string{"userId": "me", "id": threadID})
	return c.runVoid(ctx, "gmail", "users", "threads", "modify", "--params", string(params), "--json", string(payload), "--format", "json")
}

func buildRFC822Message(draft MailDraft) (string, error) {
	if strings.TrimSpace(draft.To) == "" {
		return "", errors.New("mail recipient is required")
	}
	var b strings.Builder
	writeMailHeader(&b, "To", draft.To)
	writeMailHeader(&b, "Cc", draft.Cc)
	writeMailHeader(&b, "Subject", fallback(draft.Subject, "(no subject)"))
	writeMailHeader(&b, "Date", time.Now().Format(time.RFC1123Z))
	if draft.ThreadID != "" {
		ref := messageIDReference(draft.ThreadID)
		writeMailHeader(&b, "In-Reply-To", ref)
		writeMailHeader(&b, "References", ref)
	}
	writeMailHeader(&b, "MIME-Version", "1.0")
	writeMailHeader(&b, "Content-Type", `text/plain; charset="UTF-8"`)
	writeMailHeader(&b, "Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")
	b.WriteString(normalizeMailBody(draft.Body))
	return b.String(), nil
}

func writeMailHeader(b *strings.Builder, key, value string) {
	value = sanitizeMailHeader(value)
	if value == "" {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\r\n")
}

func sanitizeMailHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

func messageIDReference(value string) string {
	value = sanitizeMailHeader(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") {
		return value
	}
	return "<" + value + ">"
}

func normalizeMailBody(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.ReplaceAll(value, "\n", "\r\n")
}

func removeLabel(labels []string, target string) []string {
	if len(labels) == 0 {
		return labels
	}
	out := labels[:0]
	for _, label := range labels {
		if label != target {
			out = append(out, label)
		}
	}
	return out
}

func (c *CommandClient) CalendarLists(ctx context.Context) (Page[CalendarListItem], error) {
	params, _ := json.Marshal(map[string]any{"maxResults": 100})
	var raw struct {
		Items         []rawCalendarListItem `json:"items"`
		CalendarItems []rawCalendarListItem `json:"calendarItems"`
		NextPageToken string                `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "calendar", "calendarList", "list", "--params", string(params), "--format", "json"); err != nil {
		return Page[CalendarListItem]{}, err
	}
	source := raw.Items
	if len(source) == 0 {
		source = raw.CalendarItems
	}
	items := make([]CalendarListItem, 0, len(source))
	for _, item := range source {
		items = append(items, CalendarListItem{
			ID:          item.ID,
			Summary:     fallback(item.Summary, item.ID),
			Description: item.Description,
			Primary:     item.Primary,
			AccessRole:  item.AccessRole,
		})
	}
	return Page[CalendarListItem]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) CalendarEvents(ctx context.Context, q CalendarQuery) (Page[CalendarEvent], error) {
	calendarID := fallback(q.CalendarID, "primary")
	params := map[string]any{"calendarId": calendarID, "maxResults": 20, "singleEvents": true, "orderBy": "startTime"}
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
		Items         []rawCalendarEvent `json:"items"`
		NextPageToken string             `json:"nextPageToken"`
	}
	err := c.runJSON(ctx, &raw, "calendar", "events", "list", "--params", string(payload), "--format", "json")
	if err != nil {
		return Page[CalendarEvent]{}, err
	}
	items := make([]CalendarEvent, 0, len(raw.Items))
	for _, item := range raw.Items {
		items = append(items, calendarEventFromRaw(item, "", calendarID))
	}
	return Page[CalendarEvent]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) QuickAddEvent(ctx context.Context, text string) (CalendarEvent, error) {
	var raw rawCalendarEvent
	params, _ := json.Marshal(map[string]string{"calendarId": "primary", "text": text})
	err := c.runJSON(ctx, &raw, "calendar", "events", "quickAdd", "--params", string(params), "--format", "json")
	if err != nil {
		return CalendarEvent{}, err
	}
	return calendarEventFromRaw(raw, "", "primary"), nil
}

func (c *CommandClient) CreateEvent(ctx context.Context, draft EventDraft) (CalendarEvent, error) {
	calendarID := fallback(draft.CalendarID, "primary")
	payload, _ := json.Marshal(eventDraftBody(draft))
	var raw rawCalendarEvent
	err := c.runJSON(ctx, &raw, "calendar", "events", "insert", "--params", fmt.Sprintf(`{"calendarId":%q,"sendUpdates":"none"}`, calendarID), "--json", string(payload), "--format", "json")
	return calendarEventFromRaw(raw, "", calendarID), err
}

func (c *CommandClient) UpdateEvent(ctx context.Context, eventID string, draft EventDraft) (CalendarEvent, error) {
	if strings.TrimSpace(eventID) == "" {
		return CalendarEvent{}, errors.New("event id is required")
	}
	calendarID := fallback(draft.CalendarID, "primary")
	payload, _ := json.Marshal(eventDraftBody(draft))
	params, _ := json.Marshal(map[string]string{"calendarId": calendarID, "eventId": eventID, "sendUpdates": "none"})
	var raw rawCalendarEvent
	if err := c.runJSON(ctx, &raw, "calendar", "events", "patch", "--params", string(params), "--json", string(payload), "--format", "json"); err != nil {
		return CalendarEvent{}, err
	}
	return calendarEventFromRaw(raw, "", calendarID), nil
}

func (c *CommandClient) MoveEvent(ctx context.Context, eventID, sourceCalendarID, destinationCalendarID string) (CalendarEvent, error) {
	if strings.TrimSpace(eventID) == "" {
		return CalendarEvent{}, errors.New("event id is required")
	}
	sourceCalendarID = fallback(sourceCalendarID, "primary")
	if strings.TrimSpace(destinationCalendarID) == "" {
		return CalendarEvent{}, errors.New("destination calendar id is required")
	}
	params, _ := json.Marshal(map[string]string{"calendarId": sourceCalendarID, "eventId": eventID, "destination": destinationCalendarID, "sendUpdates": "none"})
	var raw rawCalendarEvent
	if err := c.runJSON(ctx, &raw, "calendar", "events", "move", "--params", string(params), "--format", "json"); err != nil {
		return CalendarEvent{}, err
	}
	return calendarEventFromRaw(raw, "", destinationCalendarID), nil
}

func eventDraftBody(draft EventDraft) map[string]any {
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
	return body
}

func (c *CommandClient) RSVPEvent(ctx context.Context, eventID, response string) (CalendarEvent, error) {
	if strings.TrimSpace(eventID) == "" {
		return CalendarEvent{}, errors.New("event id is required")
	}
	switch response {
	case "accepted", "declined", "tentative":
	default:
		return CalendarEvent{}, fmt.Errorf("unsupported RSVP response %q", response)
	}
	selfEmail, err := c.primaryCalendarEmail(ctx)
	if err != nil {
		return CalendarEvent{}, err
	}
	getParams, _ := json.Marshal(map[string]string{"calendarId": "primary", "eventId": eventID})
	var raw rawCalendarEvent
	if err := c.runJSON(ctx, &raw, "calendar", "events", "get", "--params", string(getParams), "--format", "json"); err != nil {
		return CalendarEvent{}, err
	}
	attendees := make([]map[string]string, 0, len(raw.Attendees))
	matched := false
	for _, attendee := range raw.Attendees {
		if attendee.Email == "" {
			continue
		}
		next := map[string]string{"email": attendee.Email}
		if attendee.ResponseStatus != "" {
			next["responseStatus"] = attendee.ResponseStatus
		}
		if strings.EqualFold(attendee.Email, selfEmail) {
			next["responseStatus"] = response
			matched = true
		}
		attendees = append(attendees, next)
	}
	if !matched {
		return CalendarEvent{}, fmt.Errorf("self attendee %q not found on event", selfEmail)
	}
	patchBody, _ := json.Marshal(map[string]any{"attendees": attendees})
	patchParams, _ := json.Marshal(map[string]string{"calendarId": "primary", "eventId": eventID, "sendUpdates": "none"})
	var updated rawCalendarEvent
	if err := c.runJSON(ctx, &updated, "calendar", "events", "patch", "--params", string(patchParams), "--json", string(patchBody), "--format", "json"); err != nil {
		return CalendarEvent{}, err
	}
	return calendarEventFromRaw(updated, selfEmail, "primary"), nil
}

func (c *CommandClient) DeleteEvent(ctx context.Context, eventID string) error {
	if strings.TrimSpace(eventID) == "" {
		return errors.New("event id is required")
	}
	params, _ := json.Marshal(map[string]string{"calendarId": "primary", "eventId": eventID})
	return c.runVoid(ctx, "calendar", "events", "delete", "--params", string(params), "--format", "json")
}

func (c *CommandClient) primaryCalendarEmail(ctx context.Context) (string, error) {
	params, _ := json.Marshal(map[string]string{"calendarId": "primary"})
	var raw struct {
		ID string `json:"id"`
	}
	if err := c.runJSON(ctx, &raw, "calendar", "calendarList", "get", "--params", string(params), "--format", "json"); err != nil {
		return "", err
	}
	if strings.TrimSpace(raw.ID) == "" {
		return "", errors.New("primary calendar email not found")
	}
	return raw.ID, nil
}

func calendarEventFromRaw(raw rawCalendarEvent, selfEmail, calendarID string) CalendarEvent {
	attendees := make([]string, 0, len(raw.Attendees))
	rsvp := ""
	for _, attendee := range raw.Attendees {
		if attendee.Email != "" {
			attendees = append(attendees, attendee.Email)
		}
		if selfEmail != "" && strings.EqualFold(attendee.Email, selfEmail) {
			rsvp = attendee.ResponseStatus
		}
	}
	return CalendarEvent{
		ID:          raw.ID,
		CalendarID:  calendarID,
		Summary:     raw.Summary,
		Start:       parseGoogleTime(raw.Start.DateTime, raw.Start.Date),
		End:         parseGoogleTime(raw.End.DateTime, raw.End.Date),
		Location:    raw.Location,
		HangoutLink: raw.HangoutLink,
		Attendees:   attendees,
		Description: raw.Description,
		RSVP:        rsvp,
	}
}

func (c *CommandClient) MeetSpaces(ctx context.Context) (Page[MeetSpace], error) {
	params, _ := json.Marshal(map[string]any{"pageSize": 20})
	var raw struct {
		ConferenceRecords []rawMeetConferenceRecord `json:"conferenceRecords"`
		Items             []rawMeetConferenceRecord `json:"items"`
		NextPageToken     string                    `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "meet", "conferenceRecords", "list", "--params", string(params), "--format", "json"); err != nil {
		return Page[MeetSpace]{}, err
	}
	source := raw.ConferenceRecords
	if len(source) == 0 {
		source = raw.Items
	}
	items := make([]MeetSpace, 0, len(source))
	for _, record := range source {
		space := MeetSpace{
			Name:       record.Name,
			MeetingURI: record.Space,
			Created:    parseRFC3339(record.StartTime),
			StartTime:  parseRFC3339(record.StartTime),
			EndTime:    parseRFC3339(record.EndTime),
			Type:       "conferenceRecord",
			Active:     record.EndTime == "",
		}
		space.Participants, _ = c.meetParticipants(ctx, record.Name)
		space.Recordings, _ = c.meetArtifacts(ctx, record.Name, "recordings")
		space.Transcripts, _ = c.meetArtifacts(ctx, record.Name, "transcripts")
		space.ActiveParticipants = len(space.Participants)
		space.Recording = len(space.Recordings) > 0
		items = append(items, space)
	}
	return Page[MeetSpace]{Items: items, NextPageToken: raw.NextPageToken}, nil
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

func (c *CommandClient) meetParticipants(ctx context.Context, recordName string) ([]MeetParticipant, error) {
	if recordName == "" {
		return nil, nil
	}
	params, _ := json.Marshal(map[string]any{"parent": recordName, "pageSize": 50})
	var raw struct {
		Participants []rawMeetParticipant `json:"participants"`
		Items        []rawMeetParticipant `json:"items"`
	}
	if err := c.runJSON(ctx, &raw, "meet", "conferenceRecords", "participants", "list", "--params", string(params), "--format", "json"); err != nil {
		return nil, err
	}
	source := raw.Participants
	if len(source) == 0 {
		source = raw.Items
	}
	out := make([]MeetParticipant, 0, len(source))
	for _, item := range source {
		display := fallback(item.SignedinUser.DisplayName, fallback(item.AnonymousUser.DisplayName, item.PhoneUser.DisplayName))
		out = append(out, MeetParticipant{
			Name:        item.Name,
			DisplayName: display,
			User:        item.SignedinUser.User,
			JoinTime:    parseRFC3339(item.EarliestStartTime),
			LeaveTime:   parseRFC3339(item.LatestEndTime),
		})
	}
	return out, nil
}

func (c *CommandClient) meetArtifacts(ctx context.Context, recordName, kind string) ([]MeetArtifact, error) {
	if recordName == "" {
		return nil, nil
	}
	params, _ := json.Marshal(map[string]any{"parent": recordName, "pageSize": 20})
	var raw struct {
		Recordings  []rawMeetArtifact `json:"recordings"`
		Transcripts []rawMeetArtifact `json:"transcripts"`
		Items       []rawMeetArtifact `json:"items"`
	}
	if err := c.runJSON(ctx, &raw, "meet", "conferenceRecords", kind, "list", "--params", string(params), "--format", "json"); err != nil {
		return nil, err
	}
	source := raw.Items
	if kind == "recordings" && len(raw.Recordings) > 0 {
		source = raw.Recordings
	}
	if kind == "transcripts" && len(raw.Transcripts) > 0 {
		source = raw.Transcripts
	}
	out := make([]MeetArtifact, 0, len(source))
	for _, item := range source {
		file := item.DriveDestination.File
		if file == "" {
			file = item.DocsDestination.Document
		}
		out = append(out, MeetArtifact{
			Name:      item.Name,
			State:     item.State,
			File:      file,
			StartTime: parseRFC3339(item.StartTime),
			EndTime:   parseRFC3339(item.EndTime),
		})
	}
	return out, nil
}

func (c *CommandClient) TaskLists(ctx context.Context) (Page[TaskList], error) {
	params, _ := json.Marshal(map[string]any{"maxResults": 100})
	var raw struct {
		Items         []rawTaskList `json:"items"`
		TaskLists     []rawTaskList `json:"taskLists"`
		NextPageToken string        `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "tasks", "tasklists", "list", "--params", string(params), "--format", "json"); err != nil {
		return Page[TaskList]{}, err
	}
	source := raw.Items
	if len(source) == 0 {
		source = raw.TaskLists
	}
	items := make([]TaskList, 0, len(source))
	for _, item := range source {
		items = append(items, TaskList{
			ID:      item.ID,
			Title:   fallback(item.Title, "(untitled)"),
			Updated: parseRFC3339(item.Updated),
		})
	}
	return Page[TaskList]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) Tasks(ctx context.Context, q TaskQuery) (Page[TaskItem], error) {
	if strings.TrimSpace(q.TaskListID) == "" {
		return Page[TaskItem]{}, errors.New("task list id is required")
	}
	params := map[string]any{
		"tasklist":      q.TaskListID,
		"maxResults":    100,
		"showCompleted": true,
		"showDeleted":   false,
	}
	if q.PageToken != "" {
		params["pageToken"] = q.PageToken
	}
	payload, _ := json.Marshal(params)
	var raw struct {
		Items         []rawTaskItem `json:"items"`
		Tasks         []rawTaskItem `json:"tasks"`
		NextPageToken string        `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "tasks", "tasks", "list", "--params", string(payload), "--format", "json"); err != nil {
		return Page[TaskItem]{}, err
	}
	source := raw.Items
	if len(source) == 0 {
		source = raw.Tasks
	}
	items := make([]TaskItem, 0, len(source))
	for _, item := range source {
		items = append(items, TaskItem{
			ID:         item.ID,
			TaskListID: q.TaskListID,
			Title:      fallback(item.Title, "(untitled task)"),
			Notes:      item.Notes,
			Status:     item.Status,
			Due:        parseRFC3339(item.Due),
			Completed:  parseRFC3339(item.Completed),
			Updated:    parseRFC3339(item.Updated),
			Parent:     item.Parent,
			Position:   item.Position,
		})
	}
	return Page[TaskItem]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) DriveFiles(ctx context.Context, q DriveQuery) (Page[DriveFile], error) {
	return c.driveFiles(ctx, q)
}

func (c *CommandClient) Docs(ctx context.Context, q DriveQuery) (Page[DriveFile], error) {
	q.MimeType = "application/vnd.google-apps.document"
	return c.driveFiles(ctx, q)
}

func (c *CommandClient) driveFiles(ctx context.Context, q DriveQuery) (Page[DriveFile], error) {
	params := map[string]any{
		"pageSize": 50,
		"fields":   "nextPageToken, files(id,name,mimeType,modifiedTime,webViewLink,size,parents)",
	}
	var filters []string
	if strings.TrimSpace(q.Search) != "" {
		escaped := strings.ReplaceAll(q.Search, "'", "\\'")
		filters = append(filters, fmt.Sprintf("name contains '%s'", escaped))
	}
	if strings.TrimSpace(q.MimeType) != "" {
		filters = append(filters, fmt.Sprintf("mimeType = '%s'", q.MimeType))
	}
	if len(filters) > 0 {
		params["q"] = strings.Join(filters, " and ")
	}
	if q.PageToken != "" {
		params["pageToken"] = q.PageToken
	}
	payload, _ := json.Marshal(params)
	var raw struct {
		Files         []rawDriveFile `json:"files"`
		Items         []rawDriveFile `json:"items"`
		NextPageToken string         `json:"nextPageToken"`
	}
	if err := c.runJSON(ctx, &raw, "drive", "files", "list", "--params", string(payload), "--format", "json"); err != nil {
		return Page[DriveFile]{}, err
	}
	source := raw.Files
	if len(source) == 0 {
		source = raw.Items
	}
	items := make([]DriveFile, 0, len(source))
	for _, item := range source {
		size, _ := strconv.ParseInt(item.Size, 10, 64)
		items = append(items, DriveFile{
			ID:           item.ID,
			Name:         fallback(item.Name, "(untitled)"),
			MimeType:     item.MimeType,
			ModifiedTime: parseRFC3339(item.ModifiedTime),
			WebViewLink:  item.WebViewLink,
			Size:         size,
			Parents:      append([]string(nil), item.Parents...),
		})
	}
	return Page[DriveFile]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) Doc(ctx context.Context, documentID string) (DocDocument, error) {
	if strings.TrimSpace(documentID) == "" {
		return DocDocument{}, errors.New("document id is required")
	}
	params, _ := json.Marshal(map[string]string{"documentId": documentID})
	var raw struct {
		DocumentID string `json:"documentId"`
		Title      string `json:"title"`
		Body       struct {
			Content []struct {
				Paragraph *struct {
					Elements []struct {
						TextRun *struct {
							Content string `json:"content"`
						} `json:"textRun"`
					} `json:"elements"`
				} `json:"paragraph"`
			} `json:"content"`
		} `json:"body"`
	}
	if err := c.runJSON(ctx, &raw, "docs", "documents", "get", "--params", string(params), "--format", "json"); err != nil {
		return DocDocument{}, err
	}
	var body strings.Builder
	for _, block := range raw.Body.Content {
		if block.Paragraph == nil {
			continue
		}
		for _, element := range block.Paragraph.Elements {
			if element.TextRun != nil {
				body.WriteString(element.TextRun.Content)
			}
		}
	}
	return DocDocument{
		ID:    fallback(raw.DocumentID, documentID),
		Title: fallback(raw.Title, "(untitled document)"),
		Body:  strings.TrimSpace(body.String()),
	}, nil
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

func parseRFC3339(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	return time.Time{}
}

func lastSegment(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "/")
	return parts[len(parts)-1]
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	line, _, _ := strings.Cut(value, "\n")
	return strings.TrimSpace(line)
}

func fallback(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
