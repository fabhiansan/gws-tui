package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

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
	c.annotateChatSpaceReadStates(ctx, items)
	return Page[Space]{Items: items, NextPageToken: raw.NextPageToken}, nil
}

func (c *CommandClient) annotateChatSpaceReadStates(ctx context.Context, spaces []Space) {
	const maxConcurrent = 8
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	for i := range spaces {
		if spaces[i].Name == "" || spaces[i].LastActiveTime.IsZero() {
			continue
		}
		i := i
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			state, err := c.ChatSpaceReadState(ctx, spaces[i].Name)
			if err != nil {
				return
			}
			spaces[i].LastReadTime = state.LastReadTime
			spaces[i].Unread = chatSpaceUnread(spaces[i].LastActiveTime, state.LastReadTime)
		}()
	}
	wg.Wait()
}

func chatSpaceUnread(lastActive, lastRead time.Time) bool {
	if lastActive.IsZero() {
		return false
	}
	if lastRead.IsZero() {
		return true
	}
	return lastActive.After(lastRead)
}

func (c *CommandClient) ChatSpaceReadState(ctx context.Context, spaceName string) (SpaceReadState, error) {
	if strings.TrimSpace(spaceName) == "" {
		return SpaceReadState{}, errors.New("space name is required")
	}
	params, _ := json.Marshal(map[string]string{"name": chatSpaceReadStateName(spaceName)})
	var state SpaceReadState
	err := c.runJSON(ctx, &state, "chat", "users", "spaces", "getSpaceReadState", "--params", string(params), "--format", "json")
	return state, err
}

func (c *CommandClient) MarkChatRead(ctx context.Context, spaceName string) error {
	if strings.TrimSpace(spaceName) == "" {
		return errors.New("space name is required")
	}
	params, _ := json.Marshal(map[string]string{
		"name":       chatSpaceReadStateName(spaceName),
		"updateMask": "lastReadTime",
	})
	body, _ := json.Marshal(map[string]string{
		"lastReadTime": time.Now().UTC().Format(time.RFC3339Nano),
	})
	return c.runVoid(ctx, "chat", "users", "spaces", "updateSpaceReadState", "--params", string(params), "--json", string(body), "--format", "json")
}

func chatSpaceReadStateName(spaceName string) string {
	spaceName = strings.TrimSpace(spaceName)
	spaceID := strings.TrimPrefix(spaceName, "spaces/")
	return "users/me/spaces/" + spaceID + "/spaceReadState"
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

// SubscribeChat opens a stream of new chat messages for the given space. When
// real-time delivery is available (see resolveChatHub) every space shares a
// single Workspace Events subprocess; otherwise the space is polled every 5
// seconds so environments without Pub/Sub plumbing keep working.
func (c *CommandClient) SubscribeChat(ctx context.Context, spaceName string) (<-chan ChatMessage, error) {
	if spaceName == "" {
		return nil, errors.New("space name required")
	}
	if hub := c.resolveChatHub(); hub != nil {
		return hub.subscribe(ctx, spaceName), nil
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
				Attachment  []rawChatAttachment `json:"attachment"`
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
	attachments := MergeAttachments(
		chatAttachments(raw.Attachment),
		chatAttachments(raw.Attachments),
		ImageAttachmentsFromText(raw.Text),
	)
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
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
				Type        string `json:"type"`
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
		members = append(members, SpaceMember{UserID: userID, DisplayName: m.Member.DisplayName, Type: m.Member.Type})
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
