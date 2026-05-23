package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

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
	plain, html := gmailBodyParts(part)
	if plain != "" {
		return plain
	}
	return html
}

func gmailBodyParts(part rawGmailPart) (plain, html string) {
	mime := strings.ToLower(part.MimeType)
	if strings.HasPrefix(mime, "text/plain") {
		if decoded := decodeGmailData(part.Body.Data); decoded != "" {
			return decoded, ""
		}
	}
	if strings.HasPrefix(mime, "text/html") {
		if decoded := decodeGmailData(part.Body.Data); decoded != "" {
			return "", decoded
		}
	}
	for _, child := range part.Parts {
		childPlain, childHTML := gmailBodyParts(child)
		if childPlain == "" && childHTML == "" {
			continue
		}
		if plain == "" && childPlain != "" {
			plain = childPlain
		}
		if html == "" && childHTML != "" {
			html = childHTML
		}
	}
	if strings.HasPrefix(mime, "text/") {
		return decodeGmailData(part.Body.Data), ""
	}
	return plain, html
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
	params := map[string]any{"userId": "me", "maxResults": 50}
	if q.PageToken != "" {
		params["pageToken"] = q.PageToken
	}
	if q.IncludeSpamTrash {
		params["includeSpamTrash"] = true
	}
	switch {
	case q.Search != "":
		params["q"] = q.Search
	case len(q.LabelIDs) > 0:
		params["labelIds"] = q.LabelIDs
	case q.LabelQuery != "":
		params["q"] = q.LabelQuery
	case q.Label != "" && q.Label != "All Mail":
		// Fallback for callers that only set a display name (e.g. the
		// daemon's initial fetch): map it to a Gmail system label ID.
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
	body, htmlBody := gmailBodyParts(raw.Payload)
	if body == "" {
		body = htmlBody
	}
	snippet := raw.Snippet
	if snippet == "" {
		snippet = firstLine(body)
	}
	return MailThread{
		ID:          fallback(threadID, raw.ThreadID),
		Sender:      fallback(senderName, fallback(senderEmail, "(unknown)")),
		SenderEmail: senderEmail,
		To:          headers["to"],
		Cc:          headers["cc"],
		Subject:     fallback(headers["subject"], "(no subject)"),
		Snippet:     snippet,
		Date:        parseGmailDate(raw.InternalDate, headers["date"]),
		Body:        body,
		Attachments: MergeAttachments(gmailAttachments(raw.ID, raw.Payload), ImageAttachmentsFromText(body), ImageAttachmentsFromHTML(htmlBody)),
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

func (c *CommandClient) ToggleMailLabel(ctx context.Context, threadID, labelID string) (MailThread, error) {
	if strings.TrimSpace(labelID) == "" {
		return MailThread{}, errors.New("label id is required")
	}
	thread, err := c.fetchMailThread(ctx, threadID)
	if err != nil {
		return MailThread{}, err
	}
	if containsLabel(thread.Labels, labelID) {
		err = c.modifyMailThreadLabels(ctx, threadID, nil, []string{labelID})
		thread.Labels = removeLabel(thread.Labels, labelID)
	} else {
		err = c.modifyMailThreadLabels(ctx, threadID, []string{labelID}, nil)
		thread.Labels = append(thread.Labels, labelID)
	}
	if err != nil {
		return MailThread{}, err
	}
	thread.Starred = containsLabel(thread.Labels, "STARRED")
	thread.Unread = containsLabel(thread.Labels, "UNREAD")
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

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	line, _, _ := strings.Cut(value, "\n")
	return strings.TrimSpace(line)
}
