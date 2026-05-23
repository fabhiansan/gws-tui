package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("GWS_FAKE_COMMAND") == "1" {
		fakeCommand()
		return
	}
	os.Exit(m.Run())
}

func TestCommandClientChatMessagesLoadsLatestPageChronologically(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	page, err := client.ChatMessages(context.Background(), "spaces/engineering", "")
	if err != nil {
		t.Fatal(err)
	}

	if len(page.Items) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(page.Items))
	}
	if page.Items[0].ID != "older-latest-page" || page.Items[1].ID != "newest" {
		t.Fatalf("messages were not returned chronologically: %#v", page.Items)
	}
}

func TestCommandClientChatSpacesUsesSpaceReadState(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	page, err := client.ChatSpaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 spaces, got %#v", page.Items)
	}
	if !page.Items[0].Unread {
		t.Fatalf("engineering should be unread from server read state: %#v", page.Items[0])
	}
	if page.Items[0].LastReadTime.IsZero() {
		t.Fatalf("engineering should carry server last read time: %#v", page.Items[0])
	}
	if page.Items[1].Unread {
		t.Fatalf("design should be read from server read state: %#v", page.Items[1])
	}
}

func TestCommandClientMarkChatReadUpdatesSpaceReadState(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	if err := client.MarkChatRead(context.Background(), "spaces/engineering"); err != nil {
		t.Fatal(err)
	}
}

func TestCommandClientSubscribeChatStreamsCloudEvents(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")
	t.Setenv("GWS_EVENTS_PROJECT", "test-project")

	client := NewCommandClient(os.Args[0])
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := client.SubscribeChat(ctx, "spaces/engineering")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("subscription channel closed before any message")
		}
		if msg.ID != "stream-1" {
			t.Fatalf("unexpected message id: %q", msg.ID)
		}
		if msg.Space != "spaces/engineering" {
			t.Fatalf("unexpected space: %q", msg.Space)
		}
		if msg.Text != "hello via stream" {
			t.Fatalf("unexpected text: %q", msg.Text)
		}
		if msg.SenderName != "Alice" {
			t.Fatalf("unexpected sender: %q", msg.SenderName)
		}
		if len(msg.Attachments) != 1 {
			t.Fatalf("expected streamed attachment, got %#v", msg.Attachments)
		}
		attachment := msg.Attachments[0]
		if attachment.ResourceName != "spaces/engineering/messages/stream-1/attachments/image-1" {
			t.Fatalf("unexpected attachment resource: %#v", attachment)
		}
		if attachment.ContentType != "image/png" || attachment.Name != "stream.png" {
			t.Fatalf("unexpected streamed attachment metadata: %#v", attachment)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streamed chat event")
	}
}

func TestPrepareChatEventsSelectsMode(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")
	t.Setenv("GWS_EVENTS_PROJECT", "")
	t.Setenv("GWS_EVENTS_SUBSCRIPTION", "")

	disabled := NewCommandClient(os.Args[0])
	defer disabled.Close()
	disabled.ConfigureChatEvents(ChatEventOptions{Disabled: true})
	if mode := disabled.PrepareChatEvents(); mode != "polling" {
		t.Fatalf("disabled events should poll, got %q", mode)
	}

	realtime := NewCommandClient(os.Args[0])
	defer realtime.Close()
	realtime.ConfigureChatEvents(ChatEventOptions{Project: "test-project"})
	if mode := realtime.PrepareChatEvents(); mode != "realtime" {
		t.Fatalf("configured project should enable real-time, got %q", mode)
	}
}

func TestCommandClientChatMembersIncludesDisplayName(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	members, err := client.ChatMembers(context.Background(), "spaces/engineering")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %#v", members)
	}
	if members[0].UserID != "alice" || members[0].DisplayName != "Alice" {
		t.Fatalf("displayName was not parsed: %#v", members[0])
	}
}

func emitFakeChatEventStream() {
	target := ""
	for i, arg := range os.Args {
		if arg == "--target" && i+1 < len(os.Args) {
			target = os.Args[i+1]
		}
	}
	// The hub subscribes to every space at once with the spaces/- target.
	if target != "//chat.googleapis.com/spaces/-" {
		fmt.Fprintf(os.Stderr, "unexpected target: %q\n", target)
		os.Exit(2)
	}
	hasProject := false
	hasSubscription := false
	for i, arg := range os.Args {
		if arg == "--project" && i+1 < len(os.Args) {
			hasProject = true
		}
		if arg == "--subscription" && i+1 < len(os.Args) {
			hasSubscription = true
		}
	}
	if !hasProject && !hasSubscription {
		fmt.Fprintln(os.Stderr, "events +subscribe missing --project/--subscription")
		os.Exit(2)
	}
	event := map[string]any{
		"type":    "google.workspace.chat.message.v1.created",
		"subject": "spaces/engineering/messages/stream-1",
		"data": map[string]any{
			"message": map[string]any{
				"name":       "spaces/engineering/messages/stream-1",
				"text":       "hello via stream",
				"createTime": "2026-05-18T10:00:00+07:00",
				"sender":     map[string]any{"name": "users/alice", "displayName": "Alice"},
				"space":      map[string]any{"name": "spaces/engineering"},
				"attachment": []any{map[string]any{
					"name":        "spaces/engineering/messages/stream-1/attachments/image-1",
					"contentName": "stream.png",
					"contentType": "image/png",
					"attachmentDataRef": map[string]any{
						"resourceName": "spaces/engineering/messages/stream-1/attachments/image-1",
					},
				}},
			},
		},
	}
	payload, err := json.Marshal(event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal event: %v\n", err)
		os.Exit(2)
	}
	fmt.Println(string(payload))
	// Block until killed so the parent context cancellation closes us. This
	// mirrors `gws events +subscribe` which is a long-running stream.
	select {}
}

func TestCommandClientSendChatMessageUploadsAttachments(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	dir := t.TempDir()
	path := filepath.Join(dir, "paste.png")
	if err := os.WriteFile(path, []byte("fake-png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	client := NewCommandClient(os.Args[0])
	msg, err := client.SendChatMessage(context.Background(), "spaces/engineering", "hi", "", []LocalAttachment{{
		Path:        path,
		ContentType: "image/png",
		Name:        "paste.png",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if msg.ID != "msg-with-attachment" {
		t.Fatalf("unexpected message id: %q", msg.ID)
	}
	if len(msg.Attachments) == 0 {
		t.Fatalf("expected returned message to include attachments, got none")
	}
	got := msg.Attachments[0]
	if got.LocalPath != path {
		t.Fatalf("expected LocalPath to be stamped from upload, got %q want %q", got.LocalPath, path)
	}
	if got.ContentType != "image/png" {
		t.Fatalf("expected contentType image/png, got %q", got.ContentType)
	}
}

func TestCommandClientChatMessageActionsUseChatResources(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	msg, err := client.EditChatMessage(context.Background(), "spaces/engineering/messages/msg-1", "edited text")
	if err != nil {
		t.Fatal(err)
	}
	if msg.ID != "msg-1" || msg.Space != "spaces/engineering" || msg.Text != "edited text" {
		t.Fatalf("unexpected edited message: %#v", msg)
	}
	if err := client.DeleteChatMessage(context.Background(), "spaces/engineering/messages/msg-1"); err != nil {
		t.Fatal(err)
	}
	space, err := client.CreateChatSpace(context.Background(), "Launch Room")
	if err != nil {
		t.Fatal(err)
	}
	if space.Name != "spaces/launch-room" || space.DisplayName != "Launch Room" {
		t.Fatalf("unexpected created space: %#v", space)
	}
	setup, err := client.SetupChatSpace(context.Background(), "Launch Room", []string{"alice@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if setup.Name != "spaces/launch-room-setup" {
		t.Fatalf("unexpected setup space: %#v", setup)
	}
	reaction, err := client.AddChatReaction(context.Background(), "spaces/engineering/messages/msg-1", "\U0001F44D")
	if err != nil {
		t.Fatal(err)
	}
	if reaction != "spaces/engineering/messages/msg-1/reactions/reaction-1" {
		t.Fatalf("unexpected reaction name: %q", reaction)
	}
	if err := client.DeleteChatReaction(context.Background(), reaction); err != nil {
		t.Fatal(err)
	}
}

func TestCommandClientDownloadAttachmentUsesMediaResourceName(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	outputPath := filepath.Join(t.TempDir(), "image.png")
	client := NewCommandClient(os.Args[0])
	err := client.DownloadAttachment(context.Background(), Attachment{
		ResourceName: "spaces/engineering/messages/msg-1/attachments/image-1",
		ContentType:  "image/png",
	}, outputPath)
	if err != nil {
		t.Fatal(err)
	}

	payload, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "fake-media" {
		t.Fatalf("unexpected downloaded payload: %q", payload)
	}

	mailOutputPath := filepath.Join(t.TempDir(), "mail.txt")
	err = client.DownloadAttachment(context.Background(), Attachment{
		ResourceName: "gmail/users/me/messages/msg-attach/attachments/att-1",
		ContentType:  "text/plain",
	}, mailOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	payload, err = os.ReadFile(mailOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "mail-attachment" {
		t.Fatalf("unexpected gmail attachment payload: %q", payload)
	}

	driveOutputPath := filepath.Join(t.TempDir(), "drive.bin")
	err = client.DownloadAttachment(context.Background(), Attachment{
		ResourceName: "drive/files/drive-download",
		Name:         "drive.bin",
	}, driveOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	payload, err = os.ReadFile(driveOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "drive-media" {
		t.Fatalf("unexpected drive payload: %q", payload)
	}
}

func TestCommandClientMailLabelsUsesGmailLabelsList(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	labels, err := client.MailLabels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) < 3 {
		t.Fatalf("expected labels plus All Mail, got %#v", labels)
	}
	if labels[0].Name != "Inbox" || labels[0].LabelIDs[0] != "INBOX" {
		t.Fatalf("unexpected first label: %#v", labels[0])
	}
	if labels[len(labels)-1].Name != "All Mail" || labels[len(labels)-1].Query == "" {
		t.Fatalf("expected synthesized All Mail query label, got %#v", labels[len(labels)-1])
	}
}

func TestMailThreadFromRawMessageExtractsHTMLImageAttachments(t *testing.T) {
	plainPart := rawGmailPart{MimeType: "text/plain"}
	plainPart.Body.Data = base64.RawURLEncoding.EncodeToString([]byte("Plain text offer"))
	htmlPart := rawGmailPart{MimeType: "text/html"}
	htmlPart.Body.Data = base64.RawURLEncoding.EncodeToString([]byte(`<html><body><p>HTML offer</p><img src="https://images.example.com/render?id=42&amp;w=600"></body></html>`))

	thread := mailThreadFromRawMessage(rawGmailMessage{
		ID:           "msg-html",
		ThreadID:     "thread-html",
		LabelIDs:     []string{"INBOX"},
		InternalDate: "1779199200000",
		Payload: rawGmailPart{
			MimeType: "multipart/alternative",
			Headers: []rawGmailHeader{
				{Name: "From", Value: "Shutterstock <emktng.shutterstock.com>"},
				{Name: "Subject", Value: "Konten terbaru"},
			},
			Parts: []rawGmailPart{plainPart, htmlPart},
		},
	}, "")

	if thread.Body != "Plain text offer" {
		t.Fatalf("expected plain body to remain preferred, got %q", thread.Body)
	}
	if len(thread.Attachments) != 1 {
		t.Fatalf("expected one HTML image attachment, got %#v", thread.Attachments)
	}
	attachment := thread.Attachments[0]
	if attachment.URL != "https://images.example.com/render?id=42&w=600" {
		t.Fatalf("unexpected HTML image URL: %#v", attachment)
	}
	if attachment.ContentType != "image/unknown" || !attachment.IsImage() {
		t.Fatalf("expected extensionless HTML image to be previewable: %#v", attachment)
	}
}

func TestCommandClientSendMailSendsRFC822RawMessage(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	thread, err := client.SendMail(context.Background(), MailDraft{
		To:       "you@example.com",
		Cc:       "copy@example.com",
		Subject:  "Launch",
		Body:     "Hi team\nShip it.",
		ThreadID: "thread-send",
	})
	if err != nil {
		t.Fatal(err)
	}
	if thread.ID != "thread-send" {
		t.Fatalf("expected sent thread id, got %q", thread.ID)
	}
	if thread.Subject != "Launch" || thread.Body != "Hi team\nShip it." {
		t.Fatalf("sent thread did not preserve draft fields: %#v", thread)
	}
}

func TestCommandClientMailDraftsUseGmailDraftResources(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	drafts, err := client.MailDrafts(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts.Items) != 1 || drafts.Items[0].ID != "draft-1" || drafts.Items[0].Subject != "Draft subject" {
		t.Fatalf("unexpected drafts: %#v", drafts)
	}
	created, err := client.CreateMailDraft(context.Background(), MailDraft{
		To:      "you@example.com",
		Subject: "Draft subject",
		Body:    "draft body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "draft-created" || created.To != "you@example.com" {
		t.Fatalf("unexpected created draft: %#v", created)
	}
	thread, err := client.SendMailDraft(context.Background(), "draft-created")
	if err != nil {
		t.Fatal(err)
	}
	if thread.ID != "thread-draft-sent" {
		t.Fatalf("unexpected sent draft thread: %#v", thread)
	}
}

func TestCommandClientArchiveTrashAndToggleStarUseThreadResources(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	if err := client.ArchiveMail(context.Background(), "thread-archive"); err != nil {
		t.Fatal(err)
	}
	if err := client.TrashMail(context.Background(), "thread-trash"); err != nil {
		t.Fatal(err)
	}
	thread, err := client.ToggleStar(context.Background(), "thread-unstarred")
	if err != nil {
		t.Fatal(err)
	}
	if !thread.Starred {
		t.Fatalf("expected ToggleStar to return a starred thread: %#v", thread)
	}
	if !containsLabel(thread.Labels, "STARRED") {
		t.Fatalf("expected STARRED label in returned thread: %#v", thread.Labels)
	}
	readThread, err := client.SetMailUnread(context.Background(), "thread-read", false)
	if err != nil {
		t.Fatal(err)
	}
	if readThread.Unread || containsLabel(readThread.Labels, "UNREAD") {
		t.Fatalf("expected read thread without UNREAD label: %#v", readThread)
	}
	unreadThread, err := client.SetMailUnread(context.Background(), "thread-unread", true)
	if err != nil {
		t.Fatal(err)
	}
	if !unreadThread.Unread || !containsLabel(unreadThread.Labels, "UNREAD") {
		t.Fatalf("expected unread thread with UNREAD label: %#v", unreadThread)
	}
}

func TestCommandClientToggleMailLabelModifiesThreadLabels(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	thread, err := client.ToggleMailLabel(context.Background(), "thread-unlabeled", "Label_42")
	if err != nil {
		t.Fatal(err)
	}
	if !containsLabel(thread.Labels, "Label_42") {
		t.Fatalf("expected Label_42 in returned thread labels: %#v", thread.Labels)
	}
	if _, err := client.ToggleMailLabel(context.Background(), "thread-unlabeled", ""); err == nil {
		t.Fatal("expected error when label id is empty")
	}
}

func TestCommandClientRSVPAndDeleteEventUseCalendarEndpoints(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	event, err := client.RSVPEvent(context.Background(), "event-rsvp", "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != "event-rsvp" || event.RSVP != "accepted" {
		t.Fatalf("unexpected RSVP result: %#v", event)
	}
	if err := client.DeleteEvent(context.Background(), "event-delete"); err != nil {
		t.Fatal(err)
	}
}

func TestCommandClientCalendarListsUpdateAndMoveEvents(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	calendars, err := client.CalendarLists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(calendars.Items) != 2 || calendars.Items[1].ID != "team@example.com" {
		t.Fatalf("unexpected calendars: %#v", calendars)
	}
	updated, err := client.UpdateEvent(context.Background(), "event-update", EventDraft{
		CalendarID: "team@example.com",
		Summary:    "Updated planning",
		Start:      time.Date(2026, 5, 20, 12, 0, 0, 0, time.FixedZone("WIB", 7*60*60)),
		End:        time.Date(2026, 5, 20, 13, 0, 0, 0, time.FixedZone("WIB", 7*60*60)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != "event-update" || updated.CalendarID != "team@example.com" || updated.Summary != "Updated planning" {
		t.Fatalf("unexpected updated event: %#v", updated)
	}
	moved, err := client.MoveEvent(context.Background(), "event-update", "team@example.com", "primary")
	if err != nil {
		t.Fatal(err)
	}
	if moved.CalendarID != "primary" {
		t.Fatalf("unexpected moved event: %#v", moved)
	}
}

func TestCommandClientTasksLoadsTaskListsAndTasks(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	lists, err := client.TaskLists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(lists.Items) != 1 || lists.Items[0].ID != "tasks-default" {
		t.Fatalf("unexpected task lists: %#v", lists)
	}
	tasks, err := client.Tasks(context.Background(), TaskQuery{TaskListID: "tasks-default"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks.Items) != 1 || tasks.Items[0].Title != "Review launch checklist" {
		t.Fatalf("unexpected tasks: %#v", tasks)
	}
	if tasks.Items[0].TaskListID != "tasks-default" {
		t.Fatalf("expected task list id stamped on task: %#v", tasks.Items[0])
	}
	updated, err := client.SetTaskCompleted(context.Background(), "tasks-default", "task-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != "task-1" || updated.Status != "completed" || updated.Completed.IsZero() {
		t.Fatalf("unexpected completed task: %#v", updated)
	}
	if err := client.DeleteTask(context.Background(), "tasks-default", "task-1"); err != nil {
		t.Fatal(err)
	}
}

func TestCommandClientMeetSpacesLoadsConferenceRecordDetails(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	meet, err := client.MeetSpaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(meet.Items) != 1 || meet.Items[0].Name != "conferenceRecords/rec-1" {
		t.Fatalf("unexpected meet records: %#v", meet)
	}
	record := meet.Items[0]
	if record.SpaceName != "spaces/meet-1" || record.JoinURL() != "https://meet.google.com/abc-defg-hij" {
		t.Fatalf("meet record space/link not enriched: %#v", record)
	}
	if len(record.Participants) != 1 || record.Participants[0].DisplayName != "Alice" {
		t.Fatalf("participants not loaded: %#v", record.Participants)
	}
	if len(record.Recordings) != 1 || record.Recordings[0].File != "drive-files/recording-1" {
		t.Fatalf("recordings not loaded: %#v", record.Recordings)
	}
	if len(record.Transcripts) != 1 || record.Transcripts[0].File != "docs/doc-1" {
		t.Fatalf("transcripts not loaded: %#v", record.Transcripts)
	}
}

func TestCommandClientDriveAndDocs(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")

	client := NewCommandClient(os.Args[0])
	files, err := client.DriveFiles(context.Background(), DriveQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Items) != 1 || files.Items[0].ID != "drive-1" {
		t.Fatalf("unexpected drive files: %#v", files)
	}
	docs, err := client.Docs(context.Background(), DriveQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs.Items) != 1 || docs.Items[0].ID != "doc-1" {
		t.Fatalf("unexpected docs: %#v", docs)
	}
	doc, err := client.Doc(context.Background(), "doc-1")
	if err != nil {
		t.Fatal(err)
	}
	wantBody := "Launch notes\n- Review risks\nSpec (https://example.com/spec)\n[image: Architecture]\nRequirement | Approved"
	if doc.Title != "Launch notes" || doc.Body != wantBody {
		t.Fatalf("unexpected doc: %#v", doc)
	}
	if len(doc.Blocks) != 5 {
		t.Fatalf("expected structured doc blocks, got %#v", doc.Blocks)
	}
	if doc.Blocks[0].Kind != DocBlockTitle || doc.Blocks[1].Kind != DocBlockListItem || doc.Blocks[3].Kind != DocBlockImage {
		t.Fatalf("unexpected block kinds: %#v", doc.Blocks)
	}
	if len(doc.Attachments) != 1 || doc.Attachments[0].PreviewSource() != "https://example.com/architecture.png" {
		t.Fatalf("unexpected doc image attachments: %#v", doc.Attachments)
	}
}

func fakeCommand() {
	// `events +subscribe` does not take --params; handle it before requiring one.
	if len(os.Args) >= 3 && os.Args[1] == "events" && os.Args[2] == "+subscribe" {
		emitFakeChatEventStream()
		return
	}
	// `events +renew` takes --name, not --params; handle it before requiring one.
	if len(os.Args) >= 3 && os.Args[1] == "events" && os.Args[2] == "+renew" {
		fmt.Print(`{}`)
		return
	}

	paramsIndex := -1
	for i, arg := range os.Args {
		if arg == "--params" && i+1 < len(os.Args) {
			paramsIndex = i + 1
			break
		}
	}
	if paramsIndex == -1 {
		fmt.Fprintln(os.Stderr, "missing --params")
		os.Exit(2)
	}

	var params map[string]any
	if err := json.Unmarshal([]byte(os.Args[paramsIndex]), &params); err != nil {
		fmt.Fprintf(os.Stderr, "invalid params: %v\n", err)
		os.Exit(2)
	}

	switch {
	case len(os.Args) >= 4 &&
		os.Args[1] == "events" &&
		os.Args[2] == "subscriptions" &&
		os.Args[3] == "list":
		// Read-only reachability probe / health check: report no existing
		// Workspace Events subscriptions.
		fmt.Print(`{}`)
	case len(os.Args) >= 4 &&
		os.Args[1] == "events" &&
		os.Args[2] == "subscriptions" &&
		os.Args[3] == "delete":
		fmt.Print(`{"done":true}`)
	case len(os.Args) >= 4 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "spaces" &&
		os.Args[3] == "list":
		if params["pageSize"] != float64(100) {
			fmt.Fprintf(os.Stderr, "unexpected spaces params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"spaces":[
			{"name":"spaces/engineering","displayName":"Engineering","spaceType":"SPACE","lastActiveTime":"2026-05-18T10:00:00Z"},
			{"name":"spaces/design","displayName":"Design","spaceType":"SPACE","lastActiveTime":"2026-05-18T08:00:00Z"}
		]}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "users" &&
		os.Args[3] == "spaces" &&
		os.Args[4] == "getSpaceReadState":
		switch params["name"] {
		case "users/me/spaces/engineering/spaceReadState":
			fmt.Print(`{"name":"users/me/spaces/engineering/spaceReadState","lastReadTime":"2026-05-18T09:59:00Z"}`)
		case "users/me/spaces/design/spaceReadState":
			fmt.Print(`{"name":"users/me/spaces/design/spaceReadState","lastReadTime":"2026-05-18T08:00:01Z"}`)
		default:
			fmt.Fprintf(os.Stderr, "unexpected read state name: %v\n", params["name"])
			os.Exit(2)
		}
	case len(os.Args) >= 5 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "users" &&
		os.Args[3] == "spaces" &&
		os.Args[4] == "updateSpaceReadState":
		if params["name"] != "users/me/spaces/engineering/spaceReadState" || params["updateMask"] != "lastReadTime" {
			fmt.Fprintf(os.Stderr, "unexpected update read state params: %v\n", params)
			os.Exit(2)
		}
		body := fakeJSONArg("--json")
		if _, ok := body["lastReadTime"].(string); !ok {
			fmt.Fprintf(os.Stderr, "missing lastReadTime body: %v\n", body)
			os.Exit(2)
		}
		fmt.Print(`{"name":"users/me/spaces/engineering/spaceReadState"}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "spaces" &&
		os.Args[3] == "members" &&
		os.Args[4] == "list":
		if params["parent"] != "spaces/engineering" {
			fmt.Fprintf(os.Stderr, "unexpected members parent: %v\n", params["parent"])
			os.Exit(2)
		}
		fmt.Print(`{"memberships":[
			{"member":{"name":"users/alice","displayName":"Alice","type":"HUMAN"}},
			{"member":{"name":"users/me","displayName":"Fabhianto Maoludyo","type":"HUMAN"}}
		]}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "spaces" &&
		os.Args[3] == "messages" &&
		os.Args[4] == "list":
		if params["orderBy"] != "createTime DESC" {
			fmt.Fprintf(os.Stderr, "unexpected orderBy: %v\n", params["orderBy"])
			os.Exit(2)
		}
		if params["parent"] != "spaces/engineering" {
			fmt.Fprintf(os.Stderr, "unexpected parent: %v\n", params["parent"])
			os.Exit(2)
		}
		fmt.Print(`{
			"messages": [
				{
					"name": "spaces/engineering/messages/newest",
					"text": "newest",
					"createTime": "2026-05-18T10:00:00+07:00",
					"sender": {"name": "users/alice", "displayName": "Alice"}
				},
				{
					"name": "spaces/engineering/messages/older-latest-page",
					"text": "older from latest page",
					"createTime": "2026-05-18T09:59:00+07:00",
					"sender": {"name": "users/bob", "displayName": "Bob"}
				}
			],
			"nextPageToken": "older"
		}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "media" &&
		os.Args[3] == "upload":
		if params["parent"] != "spaces/engineering" {
			fmt.Fprintf(os.Stderr, "unexpected parent: %v\n", params["parent"])
			os.Exit(2)
		}
		uploadIndex := -1
		contentType := ""
		for i, arg := range os.Args {
			if arg == "--upload" && i+1 < len(os.Args) {
				uploadIndex = i + 1
			}
			if arg == "--upload-content-type" && i+1 < len(os.Args) {
				contentType = os.Args[i+1]
			}
		}
		if uploadIndex == -1 {
			fmt.Fprintln(os.Stderr, "missing --upload")
			os.Exit(2)
		}
		// Upstream rejects absolute paths; assert the client passes a
		// cwd-relative basename instead.
		if filepath.IsAbs(os.Args[uploadIndex]) {
			fmt.Fprintf(os.Stderr, "expected basename, got absolute path: %q\n", os.Args[uploadIndex])
			os.Exit(2)
		}
		if _, err := os.Stat(os.Args[uploadIndex]); err != nil {
			fmt.Fprintf(os.Stderr, "upload file missing: %v\n", err)
			os.Exit(2)
		}
		if contentType != "image/png" {
			fmt.Fprintf(os.Stderr, "unexpected content type: %q\n", contentType)
			os.Exit(2)
		}
		fmt.Print(`{"attachmentDataRef":{"attachmentUploadToken":"upload-token-1"}}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "spaces" &&
		os.Args[3] == "messages" &&
		os.Args[4] == "create":
		jsonIndex := -1
		for i, arg := range os.Args {
			if arg == "--json" && i+1 < len(os.Args) {
				jsonIndex = i + 1
				break
			}
		}
		if jsonIndex == -1 {
			fmt.Fprintln(os.Stderr, "missing --json")
			os.Exit(2)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(os.Args[jsonIndex]), &body); err != nil {
			fmt.Fprintf(os.Stderr, "invalid body: %v\n", err)
			os.Exit(2)
		}
		atts, ok := body["attachment"].([]any)
		if !ok || len(atts) != 1 {
			fmt.Fprintf(os.Stderr, "expected one attachment in body, got: %v\n", body["attachment"])
			os.Exit(2)
		}
		first, _ := atts[0].(map[string]any)
		ref, _ := first["attachmentDataRef"].(map[string]any)
		if ref["attachmentUploadToken"] != "upload-token-1" {
			fmt.Fprintf(os.Stderr, "unexpected attachmentDataRef: %v\n", ref)
			os.Exit(2)
		}
		fmt.Print(`{
			"name": "spaces/engineering/messages/msg-with-attachment",
			"text": "hi",
			"createTime": "2026-05-18T10:00:00+07:00",
			"sender": {"name": "users/me", "displayName": "Me"},
			"attachment": [{
				"name": "spaces/engineering/messages/msg-with-attachment/attachments/att-1",
				"contentName": "paste.png",
				"contentType": "image/png",
				"thumbnailUri": "https://chat.google.com/u/0/api/thumb/att-1",
				"downloadUri": "https://chat.google.com/u/0/api/dl/att-1",
				"attachmentDataRef": {"resourceName": "spaces/engineering/attachments/att-1"}
			}]
		}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "spaces" &&
		os.Args[3] == "messages" &&
		os.Args[4] == "patch":
		if params["name"] != "spaces/engineering/messages/msg-1" || params["updateMask"] != "text" {
			fmt.Fprintf(os.Stderr, "unexpected message patch params: %v\n", params)
			os.Exit(2)
		}
		body := fakeJSONArg("--json")
		if body["text"] != "edited text" {
			fmt.Fprintf(os.Stderr, "unexpected message patch body: %v\n", body)
			os.Exit(2)
		}
		fmt.Print(`{
			"name": "spaces/engineering/messages/msg-1",
			"text": "edited text",
			"createTime": "2026-05-18T10:00:00+07:00",
			"sender": {"name": "users/me", "displayName": "Me"}
		}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "spaces" &&
		os.Args[3] == "messages" &&
		os.Args[4] == "delete":
		if params["name"] != "spaces/engineering/messages/msg-1" {
			fmt.Fprintf(os.Stderr, "unexpected message delete params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{}`)
	case len(os.Args) >= 4 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "spaces" &&
		os.Args[3] == "create":
		body := fakeJSONArg("--json")
		if body["displayName"] != "Launch Room" || body["spaceType"] != "SPACE" {
			fmt.Fprintf(os.Stderr, "unexpected space create body: %v\n", body)
			os.Exit(2)
		}
		fmt.Print(`{"name":"spaces/launch-room","displayName":"Launch Room","spaceType":"SPACE"}`)
	case len(os.Args) >= 4 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "spaces" &&
		os.Args[3] == "setup":
		body := fakeJSONArg("--json")
		space, _ := body["space"].(map[string]any)
		if space["displayName"] != "Launch Room" || space["spaceType"] != "SPACE" {
			fmt.Fprintf(os.Stderr, "unexpected space setup body: %v\n", body)
			os.Exit(2)
		}
		memberships, _ := body["memberships"].([]any)
		if len(memberships) != 1 {
			fmt.Fprintf(os.Stderr, "unexpected memberships body: %v\n", body)
			os.Exit(2)
		}
		first, _ := memberships[0].(map[string]any)
		member, _ := first["member"].(map[string]any)
		if member["name"] != "users/alice@example.com" {
			fmt.Fprintf(os.Stderr, "unexpected member body: %v\n", body)
			os.Exit(2)
		}
		fmt.Print(`{"name":"spaces/launch-room-setup","displayName":"Launch Room","spaceType":"SPACE"}`)
	case len(os.Args) >= 6 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "spaces" &&
		os.Args[3] == "messages" &&
		os.Args[4] == "reactions" &&
		os.Args[5] == "create":
		if params["parent"] != "spaces/engineering/messages/msg-1" {
			fmt.Fprintf(os.Stderr, "unexpected reaction create params: %v\n", params)
			os.Exit(2)
		}
		body := fakeJSONArg("--json")
		emoji, _ := body["emoji"].(map[string]any)
		if emoji["unicode"] != "\U0001F44D" {
			fmt.Fprintf(os.Stderr, "unexpected reaction body: %v\n", body)
			os.Exit(2)
		}
		fmt.Print(`{"name":"spaces/engineering/messages/msg-1/reactions/reaction-1"}`)
	case len(os.Args) >= 6 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "spaces" &&
		os.Args[3] == "messages" &&
		os.Args[4] == "reactions" &&
		os.Args[5] == "delete":
		if params["name"] != "spaces/engineering/messages/msg-1/reactions/reaction-1" {
			fmt.Fprintf(os.Stderr, "unexpected reaction delete params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "chat" &&
		os.Args[2] == "media" &&
		os.Args[3] == "download":
		if params["resourceName"] != "spaces/engineering/messages/msg-1/attachments/image-1" {
			fmt.Fprintf(os.Stderr, "unexpected resourceName: %v\n", params["resourceName"])
			os.Exit(2)
		}
		if params["alt"] != "media" {
			fmt.Fprintf(os.Stderr, "expected alt=media, got: %v\n", params["alt"])
			os.Exit(2)
		}
		outputIndex := -1
		for i, arg := range os.Args {
			if arg == "--output" && i+1 < len(os.Args) {
				outputIndex = i + 1
				break
			}
		}
		if outputIndex == -1 {
			fmt.Fprintln(os.Stderr, "missing --output")
			os.Exit(2)
		}
		if err := os.WriteFile(os.Args[outputIndex], []byte("fake-media"), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write output: %v\n", err)
			os.Exit(2)
		}
	case len(os.Args) >= 5 &&
		os.Args[1] == "gmail" &&
		os.Args[2] == "users" &&
		os.Args[3] == "labels" &&
		os.Args[4] == "list":
		if params["userId"] != "me" {
			fmt.Fprintf(os.Stderr, "unexpected userId: %v\n", params["userId"])
			os.Exit(2)
		}
		fmt.Print(`{"labels":[
			{"id":"INBOX","name":"Inbox","type":"system"},
			{"id":"STARRED","name":"Starred","type":"system"}
		]}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "gmail" &&
		os.Args[2] == "users" &&
		os.Args[3] == "drafts" &&
		os.Args[4] == "list":
		if params["userId"] != "me" || params["maxResults"] != float64(20) {
			fmt.Fprintf(os.Stderr, "unexpected drafts list params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"drafts":[{"id":"draft-1","message":{
			"id":"msg-draft","threadId":"thread-draft","snippet":"draft body",
			"internalDate":"1779199200000",
			"payload":{"headers":[
				{"name":"To","value":"you@example.com"},
				{"name":"Subject","value":"Draft subject"}
			]}
		}}]}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "gmail" &&
		os.Args[2] == "users" &&
		os.Args[3] == "drafts" &&
		os.Args[4] == "create":
		if params["userId"] != "me" {
			fmt.Fprintf(os.Stderr, "unexpected drafts create params: %v\n", params)
			os.Exit(2)
		}
		body := fakeJSONArg("--json")
		message, _ := body["message"].(map[string]any)
		raw, _ := message["raw"].(string)
		decoded := fakeDecodeBase64URL(raw)
		for _, want := range []string{"To: you@example.com\r\n", "Subject: Draft subject\r\n", "\r\ndraft body"} {
			if !strings.Contains(decoded, want) {
				fmt.Fprintf(os.Stderr, "draft raw missing %q in %q\n", want, decoded)
				os.Exit(2)
			}
		}
		fmt.Print(`{"id":"draft-created","message":{
			"id":"msg-created","threadId":"thread-created","snippet":"draft body",
			"internalDate":"1779199200000",
			"payload":{"headers":[
				{"name":"To","value":"you@example.com"},
				{"name":"Subject","value":"Draft subject"}
			]}
		}}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "gmail" &&
		os.Args[2] == "users" &&
		os.Args[3] == "drafts" &&
		os.Args[4] == "send":
		if params["userId"] != "me" {
			fmt.Fprintf(os.Stderr, "unexpected drafts send params: %v\n", params)
			os.Exit(2)
		}
		body := fakeJSONArg("--json")
		if body["id"] != "draft-created" {
			fmt.Fprintf(os.Stderr, "unexpected draft send body: %v\n", body)
			os.Exit(2)
		}
		fmt.Print(`{"id":"msg-draft-sent","threadId":"thread-draft-sent","labelIds":["SENT"],"payload":{"headers":[{"name":"Subject","value":"Draft subject"}]}}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "gmail" &&
		os.Args[2] == "users" &&
		os.Args[3] == "messages" &&
		os.Args[4] == "attachments" &&
		len(os.Args) >= 6 &&
		os.Args[5] == "get":
		if params["userId"] != "me" || params["messageId"] != "msg-attach" || params["id"] != "att-1" {
			fmt.Fprintf(os.Stderr, "unexpected gmail attachment params: %v\n", params)
			os.Exit(2)
		}
		fmt.Printf(`{"data":%q,"size":15}`, base64.URLEncoding.EncodeToString([]byte("mail-attachment")))
	case len(os.Args) >= 5 &&
		os.Args[1] == "gmail" &&
		os.Args[2] == "users" &&
		os.Args[3] == "messages" &&
		os.Args[4] == "send":
		if params["userId"] != "me" {
			fmt.Fprintf(os.Stderr, "unexpected userId: %v\n", params["userId"])
			os.Exit(2)
		}
		body := fakeJSONArg("--json")
		if body["threadId"] != "thread-send" {
			fmt.Fprintf(os.Stderr, "unexpected threadId body: %v\n", body["threadId"])
			os.Exit(2)
		}
		raw, _ := body["raw"].(string)
		message := fakeDecodeBase64URL(raw)
		for _, want := range []string{
			"To: you@example.com\r\n",
			"Cc: copy@example.com\r\n",
			"Subject: Launch\r\n",
			"In-Reply-To: <thread-send>\r\n",
			"References: <thread-send>\r\n",
			"\r\nHi team\r\nShip it.",
		} {
			if !strings.Contains(message, want) {
				fmt.Fprintf(os.Stderr, "raw message missing %q in %q\n", want, message)
				os.Exit(2)
			}
		}
		fmt.Print(`{"id":"msg-send","threadId":"thread-send","labelIds":["SENT"]}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "gmail" &&
		os.Args[2] == "users" &&
		os.Args[3] == "threads" &&
		os.Args[4] == "modify":
		if params["userId"] != "me" {
			fmt.Fprintf(os.Stderr, "unexpected userId: %v\n", params["userId"])
			os.Exit(2)
		}
		body := fakeJSONArg("--json")
		switch params["id"] {
		case "thread-archive":
			if !fakeStringSliceContains(body["removeLabelIds"], "INBOX") {
				fmt.Fprintf(os.Stderr, "archive did not remove INBOX: %v\n", body)
				os.Exit(2)
			}
		case "thread-unstarred":
			if !fakeStringSliceContains(body["addLabelIds"], "STARRED") {
				fmt.Fprintf(os.Stderr, "toggle did not add STARRED: %v\n", body)
				os.Exit(2)
			}
		case "thread-read":
			if !fakeStringSliceContains(body["removeLabelIds"], "UNREAD") {
				fmt.Fprintf(os.Stderr, "mark-read did not remove UNREAD: %v\n", body)
				os.Exit(2)
			}
		case "thread-unread":
			if !fakeStringSliceContains(body["addLabelIds"], "UNREAD") {
				fmt.Fprintf(os.Stderr, "mark-unread did not add UNREAD: %v\n", body)
				os.Exit(2)
			}
		case "thread-unlabeled":
			if !fakeStringSliceContains(body["addLabelIds"], "Label_42") {
				fmt.Fprintf(os.Stderr, "toggle label did not add Label_42: %v\n", body)
				os.Exit(2)
			}
		default:
			fmt.Fprintf(os.Stderr, "unexpected thread modify id: %v\n", params["id"])
			os.Exit(2)
		}
		fmt.Print(`{}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "gmail" &&
		os.Args[2] == "users" &&
		os.Args[3] == "threads" &&
		os.Args[4] == "trash":
		if params["userId"] != "me" || params["id"] != "thread-trash" {
			fmt.Fprintf(os.Stderr, "unexpected trash params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "gmail" &&
		os.Args[2] == "users" &&
		os.Args[3] == "threads" &&
		os.Args[4] == "get":
		if params["userId"] != "me" || params["format"] != "full" {
			fmt.Fprintf(os.Stderr, "unexpected thread get params: %v\n", params)
			os.Exit(2)
		}
		threadID, _ := params["id"].(string)
		labels := `["INBOX"]`
		switch threadID {
		case "thread-unstarred", "thread-unread", "thread-unlabeled":
		case "thread-read":
			labels = `["INBOX","UNREAD"]`
		default:
			fmt.Fprintf(os.Stderr, "unexpected thread get id: %v\n", params["id"])
			os.Exit(2)
		}
		fmt.Printf(`{"id":%q,"messages":[{
			"id":"msg-unstarred",
			"threadId":%q,
			"labelIds":%s,
			"internalDate":"1779199200000",
			"payload":{"headers":[
				{"name":"From","value":"Alice <alice@example.com>"},
				{"name":"Subject","value":"Needs star"}
			],"mimeType":"text/plain","body":{"data":"SGVsbG8="}}
		}]}`, threadID, threadID, labels)
	case len(os.Args) >= 4 &&
		os.Args[1] == "calendar" &&
		os.Args[2] == "calendarList" &&
		os.Args[3] == "list":
		if params["maxResults"] != float64(100) {
			fmt.Fprintf(os.Stderr, "unexpected calendarList list params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"items":[
			{"id":"primary","summary":"Primary","primary":true,"accessRole":"owner"},
			{"id":"team@example.com","summary":"Team","accessRole":"writer"}
		]}`)
	case len(os.Args) >= 4 &&
		os.Args[1] == "calendar" &&
		os.Args[2] == "calendarList" &&
		os.Args[3] == "get":
		if params["calendarId"] != "primary" {
			fmt.Fprintf(os.Stderr, "unexpected calendarList params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"id":"me@example.com"}`)
	case len(os.Args) >= 4 &&
		os.Args[1] == "calendar" &&
		os.Args[2] == "events" &&
		os.Args[3] == "get":
		if params["calendarId"] != "primary" || params["eventId"] != "event-rsvp" {
			fmt.Fprintf(os.Stderr, "unexpected event get params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"id":"event-rsvp","summary":"Planning","start":{"dateTime":"2026-05-20T10:00:00+07:00"},"end":{"dateTime":"2026-05-20T11:00:00+07:00"},"attendees":[{"email":"me@example.com","responseStatus":"needsAction"},{"email":"you@example.com","responseStatus":"accepted"}]}`)
	case len(os.Args) >= 4 &&
		os.Args[1] == "calendar" &&
		os.Args[2] == "events" &&
		os.Args[3] == "patch":
		switch params["eventId"] {
		case "event-rsvp":
			if params["calendarId"] != "primary" || params["sendUpdates"] != "none" {
				fmt.Fprintf(os.Stderr, "unexpected event patch params: %v\n", params)
				os.Exit(2)
			}
			body := fakeJSONArg("--json")
			attendees, _ := body["attendees"].([]any)
			if len(attendees) == 0 {
				fmt.Fprintf(os.Stderr, "missing attendees patch body: %v\n", body)
				os.Exit(2)
			}
			self, _ := attendees[0].(map[string]any)
			if self["email"] != "me@example.com" || self["responseStatus"] != "accepted" {
				fmt.Fprintf(os.Stderr, "unexpected attendee patch: %v\n", body)
				os.Exit(2)
			}
			fmt.Print(`{"id":"event-rsvp","summary":"Planning","start":{"dateTime":"2026-05-20T10:00:00+07:00"},"end":{"dateTime":"2026-05-20T11:00:00+07:00"},"attendees":[{"email":"me@example.com","responseStatus":"accepted"},{"email":"you@example.com","responseStatus":"accepted"}]}`)
		case "event-update":
			if params["calendarId"] != "team@example.com" || params["sendUpdates"] != "none" {
				fmt.Fprintf(os.Stderr, "unexpected event update params: %v\n", params)
				os.Exit(2)
			}
			body := fakeJSONArg("--json")
			if body["summary"] != "Updated planning" {
				fmt.Fprintf(os.Stderr, "unexpected event update body: %v\n", body)
				os.Exit(2)
			}
			fmt.Print(`{"id":"event-update","summary":"Updated planning","start":{"dateTime":"2026-05-20T12:00:00+07:00"},"end":{"dateTime":"2026-05-20T13:00:00+07:00"}}`)
		default:
			fmt.Fprintf(os.Stderr, "unexpected event patch params: %v\n", params)
			os.Exit(2)
		}
	case len(os.Args) >= 4 &&
		os.Args[1] == "calendar" &&
		os.Args[2] == "events" &&
		os.Args[3] == "move":
		if params["calendarId"] != "team@example.com" || params["eventId"] != "event-update" || params["destination"] != "primary" {
			fmt.Fprintf(os.Stderr, "unexpected event move params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"id":"event-update","summary":"Updated planning","start":{"dateTime":"2026-05-20T12:00:00+07:00"},"end":{"dateTime":"2026-05-20T13:00:00+07:00"}}`)
	case len(os.Args) >= 4 &&
		os.Args[1] == "calendar" &&
		os.Args[2] == "events" &&
		os.Args[3] == "delete":
		if params["calendarId"] != "primary" || params["eventId"] != "event-delete" {
			fmt.Fprintf(os.Stderr, "unexpected event delete params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{}`)
	case len(os.Args) >= 4 &&
		os.Args[1] == "meet" &&
		os.Args[2] == "conferenceRecords" &&
		os.Args[3] == "list":
		if params["pageSize"] != float64(20) {
			fmt.Fprintf(os.Stderr, "unexpected conference records params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"conferenceRecords":[{"name":"conferenceRecords/rec-1","space":"spaces/meet-1","startTime":"2026-05-20T09:00:00+07:00","endTime":"2026-05-20T10:00:00+07:00"}]}`)
	case len(os.Args) >= 4 &&
		os.Args[1] == "meet" &&
		os.Args[2] == "spaces" &&
		os.Args[3] == "get":
		if params["name"] != "spaces/meet-1" {
			fmt.Fprintf(os.Stderr, "unexpected meet space get params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"name":"spaces/meet-1","meetingUri":"https://meet.google.com/abc-defg-hij","meetingCode":"abc-defg-hij","config":{"accessType":"TRUSTED"}}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "meet" &&
		os.Args[2] == "conferenceRecords" &&
		os.Args[3] == "participants" &&
		os.Args[4] == "list":
		if params["parent"] != "conferenceRecords/rec-1" {
			fmt.Fprintf(os.Stderr, "unexpected participants params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"participants":[{"name":"conferenceRecords/rec-1/participants/alice","earliestStartTime":"2026-05-20T09:00:00+07:00","latestEndTime":"2026-05-20T10:00:00+07:00","signedinUser":{"user":"users/alice","displayName":"Alice"}}]}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "meet" &&
		os.Args[2] == "conferenceRecords" &&
		os.Args[3] == "recordings" &&
		os.Args[4] == "list":
		if params["parent"] != "conferenceRecords/rec-1" {
			fmt.Fprintf(os.Stderr, "unexpected recordings params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"recordings":[{"name":"conferenceRecords/rec-1/recordings/recording-1","state":"FILE_GENERATED","startTime":"2026-05-20T09:00:00+07:00","endTime":"2026-05-20T10:00:00+07:00","driveDestination":{"file":"drive-files/recording-1"}}]}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "meet" &&
		os.Args[2] == "conferenceRecords" &&
		os.Args[3] == "transcripts" &&
		os.Args[4] == "list":
		if params["parent"] != "conferenceRecords/rec-1" {
			fmt.Fprintf(os.Stderr, "unexpected transcripts params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"transcripts":[{"name":"conferenceRecords/rec-1/transcripts/transcript-1","state":"FILE_GENERATED","startTime":"2026-05-20T09:00:00+07:00","endTime":"2026-05-20T10:00:00+07:00","docsDestination":{"document":"docs/doc-1"}}]}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "tasks" &&
		os.Args[2] == "tasklists" &&
		os.Args[3] == "list":
		if params["maxResults"] != float64(100) {
			fmt.Fprintf(os.Stderr, "unexpected tasklists params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"items":[{"id":"tasks-default","title":"My Tasks","updated":"2026-05-20T08:00:00+07:00"}]}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "tasks" &&
		os.Args[2] == "tasks" &&
		os.Args[3] == "list":
		if params["tasklist"] != "tasks-default" || params["maxResults"] != float64(100) || params["showCompleted"] != true || params["showDeleted"] != false {
			fmt.Fprintf(os.Stderr, "unexpected tasks params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"items":[{"id":"task-1","title":"Review launch checklist","notes":"Ship docs","status":"needsAction","due":"2026-05-21T00:00:00.000Z","updated":"2026-05-20T08:30:00+07:00"}]}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "tasks" &&
		os.Args[2] == "tasks" &&
		os.Args[3] == "patch":
		if params["tasklist"] != "tasks-default" || params["task"] != "task-1" {
			fmt.Fprintf(os.Stderr, "unexpected task patch params: %v\n", params)
			os.Exit(2)
		}
		jsonIndex := -1
		for i, arg := range os.Args {
			if arg == "--json" && i+1 < len(os.Args) {
				jsonIndex = i + 1
				break
			}
		}
		if jsonIndex == -1 {
			fmt.Fprintln(os.Stderr, "missing task patch body")
			os.Exit(2)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(os.Args[jsonIndex]), &body); err != nil {
			fmt.Fprintf(os.Stderr, "invalid task patch body: %v\n", err)
			os.Exit(2)
		}
		if body["status"] != "completed" {
			fmt.Fprintf(os.Stderr, "unexpected task patch body: %v\n", body)
			os.Exit(2)
		}
		fmt.Print(`{"id":"task-1","title":"Review launch checklist","notes":"Ship docs","status":"completed","completed":"2026-05-20T09:00:00.000Z","updated":"2026-05-20T09:00:00+07:00"}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "tasks" &&
		os.Args[2] == "tasks" &&
		os.Args[3] == "delete":
		if params["tasklist"] != "tasks-default" || params["task"] != "task-1" {
			fmt.Fprintf(os.Stderr, "unexpected task delete params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"done":true}`)
	case len(os.Args) >= 5 &&
		os.Args[1] == "drive" &&
		os.Args[2] == "files" &&
		os.Args[3] == "get":
		if params["fileId"] != "drive-download" || params["alt"] != "media" {
			fmt.Fprintf(os.Stderr, "unexpected drive get params: %v\n", params)
			os.Exit(2)
		}
		outputIndex := -1
		for i, arg := range os.Args {
			if arg == "--output" && i+1 < len(os.Args) {
				outputIndex = i + 1
				break
			}
		}
		if outputIndex == -1 {
			fmt.Fprintln(os.Stderr, "missing drive --output")
			os.Exit(2)
		}
		if err := os.WriteFile(os.Args[outputIndex], []byte("drive-media"), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write drive output: %v\n", err)
			os.Exit(2)
		}
	case len(os.Args) >= 5 &&
		os.Args[1] == "drive" &&
		os.Args[2] == "files" &&
		os.Args[3] == "list":
		if params["pageSize"] != float64(50) {
			fmt.Fprintf(os.Stderr, "unexpected drive params: %v\n", params)
			os.Exit(2)
		}
		q, _ := params["q"].(string)
		if strings.Contains(q, "application/vnd.google-apps.document") {
			fmt.Print(`{"files":[{"id":"doc-1","name":"Launch notes","mimeType":"application/vnd.google-apps.document","modifiedTime":"2026-05-20T08:00:00+07:00","webViewLink":"https://docs.google.com/document/d/doc-1"}]}`)
		} else {
			fmt.Print(`{"files":[{"id":"drive-1","name":"Release checklist.pdf","mimeType":"application/pdf","modifiedTime":"2026-05-20T08:00:00+07:00","webViewLink":"https://drive.google.com/file/d/drive-1","size":"2048"}]}`)
		}
	case len(os.Args) >= 4 &&
		os.Args[1] == "docs" &&
		os.Args[2] == "documents" &&
		os.Args[3] == "get":
		if params["documentId"] != "doc-1" {
			fmt.Fprintf(os.Stderr, "unexpected doc params: %v\n", params)
			os.Exit(2)
		}
		fmt.Print(`{"documentId":"doc-1","title":"Launch notes","body":{"content":[{"paragraph":{"paragraphStyle":{"namedStyleType":"TITLE"},"elements":[{"textRun":{"content":"Launch notes\n","textStyle":{"bold":true}}}]}},{"paragraph":{"bullet":{"listId":"list-1","nestingLevel":0},"elements":[{"textRun":{"content":"Review risks\n"}}]}},{"paragraph":{"elements":[{"textRun":{"content":"Spec\n","textStyle":{"link":{"url":"https://example.com/spec"}}}}]}},{"paragraph":{"elements":[{"inlineObjectElement":{"inlineObjectId":"img-1"}}]}},{"table":{"tableRows":[{"tableCells":[{"content":[{"paragraph":{"elements":[{"textRun":{"content":"Requirement\n"}}]}}]},{"content":[{"paragraph":{"elements":[{"textRun":{"content":"Approved\n"}}]}}]}]}]}}]},"inlineObjects":{"img-1":{"objectId":"img-1","inlineObjectProperties":{"embeddedObject":{"title":"Architecture","imageProperties":{"contentUri":"https://example.com/architecture.png"}}}}}}`)
	default:
		fmt.Fprintf(os.Stderr, "unexpected command: %s\n", strings.Join(os.Args[1:], " "))
		os.Exit(2)
	}
}

func fakeJSONArg(flag string) map[string]any {
	value, ok := fakeArg(flag)
	if !ok {
		fmt.Fprintf(os.Stderr, "missing %s\n", flag)
		os.Exit(2)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		fmt.Fprintf(os.Stderr, "invalid %s: %v\n", flag, err)
		os.Exit(2)
	}
	return out
}

func fakeArg(flag string) (string, bool) {
	for i, arg := range os.Args {
		if arg == flag && i+1 < len(os.Args) {
			return os.Args[i+1], true
		}
	}
	return "", false
}

func fakeDecodeBase64URL(value string) string {
	for _, encoding := range []*base64.Encoding{base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return string(decoded)
		}
	}
	fmt.Fprintf(os.Stderr, "invalid base64url raw message\n")
	os.Exit(2)
	return ""
}

func fakeStringSliceContains(value any, target string) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
