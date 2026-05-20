package api

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoteClientRoundTripsWorkspaceMethods(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		method string
		result any
		call   func(*RemoteClient) error
	}{
		{
			name:   "AuthStatus",
			method: "AuthStatus",
			result: AuthStatus{AuthMethod: "daemon", TokenCacheExists: true, EncryptionValid: true},
			call: func(c *RemoteClient) error {
				got, err := c.AuthStatus(ctx)
				if got.AuthMethod != "daemon" {
					return fmt.Errorf("auth method=%q", got.AuthMethod)
				}
				return err
			},
		},
		{
			name:   "ChatSpaces",
			method: "ChatSpaces",
			result: Page[Space]{Items: []Space{{Name: "spaces/engineering"}}},
			call: func(c *RemoteClient) error {
				got, err := c.ChatSpaces(ctx)
				if len(got.Items) != 1 || got.Items[0].Name != "spaces/engineering" {
					return fmt.Errorf("spaces=%#v", got)
				}
				return err
			},
		},
		{
			name:   "ChatMessages",
			method: "ChatMessages",
			result: Page[ChatMessage]{Items: []ChatMessage{{ID: "msg-1", Space: "spaces/engineering"}}},
			call: func(c *RemoteClient) error {
				got, err := c.ChatMessages(ctx, "spaces/engineering", "")
				if len(got.Items) != 1 || got.Items[0].ID != "msg-1" {
					return fmt.Errorf("messages=%#v", got)
				}
				return err
			},
		},
		{
			name:   "SendChatMessage",
			method: "SendChatMessage",
			result: ChatMessage{ID: "sent-1", Space: "spaces/engineering", Text: "hello"},
			call: func(c *RemoteClient) error {
				got, err := c.SendChatMessage(ctx, "spaces/engineering", "hello", "", nil)
				if got.ID != "sent-1" {
					return fmt.Errorf("sent=%#v", got)
				}
				return err
			},
		},
		{
			name:   "EditChatMessage",
			method: "EditChatMessage",
			result: ChatMessage{ID: "msg-1", Space: "spaces/engineering", Text: "edited"},
			call: func(c *RemoteClient) error {
				got, err := c.EditChatMessage(ctx, "spaces/engineering/messages/msg-1", "edited")
				if got.Text != "edited" {
					return fmt.Errorf("edited=%#v", got)
				}
				return err
			},
		},
		{name: "DeleteChatMessage", method: "DeleteChatMessage", call: func(c *RemoteClient) error {
			return c.DeleteChatMessage(ctx, "spaces/engineering/messages/msg-1")
		}},
		{
			name:   "CreateChatSpace",
			method: "CreateChatSpace",
			result: Space{Name: "spaces/created", DisplayName: "Created"},
			call: func(c *RemoteClient) error {
				got, err := c.CreateChatSpace(ctx, "Created")
				if got.Name != "spaces/created" {
					return fmt.Errorf("space=%#v", got)
				}
				return err
			},
		},
		{
			name:   "SetupChatSpace",
			method: "SetupChatSpace",
			result: Space{Name: "spaces/setup", DisplayName: "Setup"},
			call: func(c *RemoteClient) error {
				got, err := c.SetupChatSpace(ctx, "Setup", []string{"alice@example.com"})
				if got.Name != "spaces/setup" {
					return fmt.Errorf("space=%#v", got)
				}
				return err
			},
		},
		{
			name:   "AddChatReaction",
			method: "AddChatReaction",
			result: "spaces/engineering/messages/msg-1/reactions/reaction-1",
			call: func(c *RemoteClient) error {
				got, err := c.AddChatReaction(ctx, "spaces/engineering/messages/msg-1", "\U0001F44D")
				if got == "" {
					return fmt.Errorf("reaction=%q", got)
				}
				return err
			},
		},
		{name: "DeleteChatReaction", method: "DeleteChatReaction", call: func(c *RemoteClient) error {
			return c.DeleteChatReaction(ctx, "spaces/engineering/messages/msg-1/reactions/reaction-1")
		}},
		{
			name:   "ChatMembers",
			method: "ChatMembers",
			result: []SpaceMember{{UserID: "alice"}},
			call: func(c *RemoteClient) error {
				got, err := c.ChatMembers(ctx, "spaces/engineering")
				if len(got) != 1 || got[0].UserID != "alice" {
					return fmt.Errorf("members=%#v", got)
				}
				return err
			},
		},
		{
			name:   "PeopleGet",
			method: "PeopleGet",
			result: Person{UserID: "alice", DisplayName: "Alice"},
			call: func(c *RemoteClient) error {
				got, err := c.PeopleGet(ctx, "alice")
				if got.DisplayName != "Alice" {
					return fmt.Errorf("person=%#v", got)
				}
				return err
			},
		},
		{name: "DownloadAttachment", method: "DownloadAttachment", call: func(c *RemoteClient) error {
			return c.DownloadAttachment(ctx, Attachment{ResourceName: "spaces/a/messages/b/attachments/c"}, "/tmp/out.png")
		}},
		{
			name:   "MailLabels",
			method: "MailLabels",
			result: []MailLabel{{Name: "Inbox"}},
			call: func(c *RemoteClient) error {
				got, err := c.MailLabels(ctx)
				if len(got) != 1 || got[0].Name != "Inbox" {
					return fmt.Errorf("labels=%#v", got)
				}
				return err
			},
		},
		{
			name:   "MailThreads",
			method: "MailThreads",
			result: Page[MailThread]{Items: []MailThread{{ID: "mail-1", Subject: "Subject"}}},
			call: func(c *RemoteClient) error {
				got, err := c.MailThreads(ctx, MailQuery{Label: "Inbox"})
				if len(got.Items) != 1 || got.Items[0].ID != "mail-1" {
					return fmt.Errorf("threads=%#v", got)
				}
				return err
			},
		},
		{
			name:   "SendMail",
			method: "SendMail",
			result: MailThread{ID: "mail-sent"},
			call: func(c *RemoteClient) error {
				got, err := c.SendMail(ctx, MailDraft{To: "alice@example.com", Subject: "Hi"})
				if got.ID != "mail-sent" {
					return fmt.Errorf("thread=%#v", got)
				}
				return err
			},
		},
		{
			name:   "MailDrafts",
			method: "MailDrafts",
			result: Page[MailDraftItem]{Items: []MailDraftItem{{ID: "draft-1", Subject: "Draft"}}},
			call: func(c *RemoteClient) error {
				got, err := c.MailDrafts(ctx, "")
				if len(got.Items) != 1 || got.Items[0].ID != "draft-1" {
					return fmt.Errorf("drafts=%#v", got)
				}
				return err
			},
		},
		{
			name:   "CreateMailDraft",
			method: "CreateMailDraft",
			result: MailDraftItem{ID: "draft-created", Subject: "Draft"},
			call: func(c *RemoteClient) error {
				got, err := c.CreateMailDraft(ctx, MailDraft{To: "alice@example.com", Subject: "Draft"})
				if got.ID != "draft-created" {
					return fmt.Errorf("draft=%#v", got)
				}
				return err
			},
		},
		{
			name:   "SendMailDraft",
			method: "SendMailDraft",
			result: MailThread{ID: "mail-draft-sent"},
			call: func(c *RemoteClient) error {
				got, err := c.SendMailDraft(ctx, "draft-1")
				if got.ID != "mail-draft-sent" {
					return fmt.Errorf("thread=%#v", got)
				}
				return err
			},
		},
		{name: "ArchiveMail", method: "ArchiveMail", call: func(c *RemoteClient) error { return c.ArchiveMail(ctx, "mail-1") }},
		{name: "TrashMail", method: "TrashMail", call: func(c *RemoteClient) error { return c.TrashMail(ctx, "mail-1") }},
		{
			name:   "ToggleStar",
			method: "ToggleStar",
			result: MailThread{ID: "mail-1", Starred: true},
			call: func(c *RemoteClient) error {
				got, err := c.ToggleStar(ctx, "mail-1")
				if !got.Starred {
					return fmt.Errorf("thread=%#v", got)
				}
				return err
			},
		},
		{
			name:   "SetMailUnread",
			method: "SetMailUnread",
			result: MailThread{ID: "mail-1", Unread: true},
			call: func(c *RemoteClient) error {
				got, err := c.SetMailUnread(ctx, "mail-1", true)
				if !got.Unread {
					return fmt.Errorf("thread=%#v", got)
				}
				return err
			},
		},
		{
			name:   "CalendarLists",
			method: "CalendarLists",
			result: Page[CalendarListItem]{Items: []CalendarListItem{{ID: "primary", Summary: "Primary"}}},
			call: func(c *RemoteClient) error {
				got, err := c.CalendarLists(ctx)
				if len(got.Items) != 1 || got.Items[0].ID != "primary" {
					return fmt.Errorf("calendars=%#v", got)
				}
				return err
			},
		},
		{
			name:   "CalendarEvents",
			method: "CalendarEvents",
			result: Page[CalendarEvent]{Items: []CalendarEvent{{ID: "event-1", Summary: "Sync"}}},
			call: func(c *RemoteClient) error {
				got, err := c.CalendarEvents(ctx, CalendarQuery{})
				if len(got.Items) != 1 || got.Items[0].ID != "event-1" {
					return fmt.Errorf("events=%#v", got)
				}
				return err
			},
		},
		{
			name:   "QuickAddEvent",
			method: "QuickAddEvent",
			result: CalendarEvent{ID: "event-quick"},
			call: func(c *RemoteClient) error {
				got, err := c.QuickAddEvent(ctx, "tomorrow sync")
				if got.ID != "event-quick" {
					return fmt.Errorf("event=%#v", got)
				}
				return err
			},
		},
		{
			name:   "CreateEvent",
			method: "CreateEvent",
			result: CalendarEvent{ID: "event-new"},
			call: func(c *RemoteClient) error {
				got, err := c.CreateEvent(ctx, EventDraft{Summary: "New"})
				if got.ID != "event-new" {
					return fmt.Errorf("event=%#v", got)
				}
				return err
			},
		},
		{
			name:   "UpdateEvent",
			method: "UpdateEvent",
			result: CalendarEvent{ID: "event-1", Summary: "Updated"},
			call: func(c *RemoteClient) error {
				got, err := c.UpdateEvent(ctx, "event-1", EventDraft{Summary: "Updated"})
				if got.Summary != "Updated" {
					return fmt.Errorf("event=%#v", got)
				}
				return err
			},
		},
		{
			name:   "MoveEvent",
			method: "MoveEvent",
			result: CalendarEvent{ID: "event-1", CalendarID: "team@example.com"},
			call: func(c *RemoteClient) error {
				got, err := c.MoveEvent(ctx, "event-1", "primary", "team@example.com")
				if got.CalendarID != "team@example.com" {
					return fmt.Errorf("event=%#v", got)
				}
				return err
			},
		},
		{
			name:   "RSVPEvent",
			method: "RSVPEvent",
			result: CalendarEvent{ID: "event-1", RSVP: "accepted"},
			call: func(c *RemoteClient) error {
				got, err := c.RSVPEvent(ctx, "event-1", "accepted")
				if got.RSVP != "accepted" {
					return fmt.Errorf("event=%#v", got)
				}
				return err
			},
		},
		{name: "DeleteEvent", method: "DeleteEvent", call: func(c *RemoteClient) error { return c.DeleteEvent(ctx, "event-1") }},
		{
			name:   "MeetSpaces",
			method: "MeetSpaces",
			result: Page[MeetSpace]{Items: []MeetSpace{{Name: "spaces/meet"}}},
			call: func(c *RemoteClient) error {
				got, err := c.MeetSpaces(ctx)
				if len(got.Items) != 1 || got.Items[0].Name != "spaces/meet" {
					return fmt.Errorf("meet=%#v", got)
				}
				return err
			},
		},
		{
			name:   "CreateMeetSpace",
			method: "CreateMeetSpace",
			result: MeetSpace{Name: "spaces/new-meet"},
			call: func(c *RemoteClient) error {
				got, err := c.CreateMeetSpace(ctx, "New")
				if got.Name != "spaces/new-meet" {
					return fmt.Errorf("meet=%#v", got)
				}
				return err
			},
		},
		{name: "EndMeetSpace", method: "EndMeetSpace", call: func(c *RemoteClient) error { return c.EndMeetSpace(ctx, "spaces/meet") }},
		{
			name:   "TaskLists",
			method: "TaskLists",
			result: Page[TaskList]{Items: []TaskList{{ID: "tasks-default", Title: "My Tasks"}}},
			call: func(c *RemoteClient) error {
				got, err := c.TaskLists(ctx)
				if len(got.Items) != 1 || got.Items[0].ID != "tasks-default" {
					return fmt.Errorf("taskLists=%#v", got)
				}
				return err
			},
		},
		{
			name:   "Tasks",
			method: "Tasks",
			result: Page[TaskItem]{Items: []TaskItem{{ID: "task-1", TaskListID: "tasks-default", Title: "Review"}}},
			call: func(c *RemoteClient) error {
				got, err := c.Tasks(ctx, TaskQuery{TaskListID: "tasks-default"})
				if len(got.Items) != 1 || got.Items[0].ID != "task-1" {
					return fmt.Errorf("tasks=%#v", got)
				}
				return err
			},
		},
		{
			name:   "DriveFiles",
			method: "DriveFiles",
			result: Page[DriveFile]{Items: []DriveFile{{ID: "drive-1", Name: "Release checklist.pdf"}}},
			call: func(c *RemoteClient) error {
				got, err := c.DriveFiles(ctx, DriveQuery{})
				if len(got.Items) != 1 || got.Items[0].ID != "drive-1" {
					return fmt.Errorf("driveFiles=%#v", got)
				}
				return err
			},
		},
		{
			name:   "Docs",
			method: "Docs",
			result: Page[DriveFile]{Items: []DriveFile{{ID: "doc-1", Name: "Launch notes"}}},
			call: func(c *RemoteClient) error {
				got, err := c.Docs(ctx, DriveQuery{})
				if len(got.Items) != 1 || got.Items[0].ID != "doc-1" {
					return fmt.Errorf("docs=%#v", got)
				}
				return err
			},
		},
		{
			name:   "Doc",
			method: "Doc",
			result: DocDocument{ID: "doc-1", Title: "Launch notes", Body: "Body"},
			call: func(c *RemoteClient) error {
				got, err := c.Doc(ctx, "doc-1")
				if got.ID != "doc-1" || got.Body == "" {
					return fmt.Errorf("doc=%#v", got)
				}
				return err
			},
		},
		{
			name:   "Snapshot",
			method: "Snapshot",
			result: WorkspaceSnapshot{ProtocolVersion: ProtocolVersion, Version: WorkspaceSnapshotVersion, Spaces: []Space{{Name: "spaces/engineering"}}},
			call: func(c *RemoteClient) error {
				got, err := c.Snapshot(ctx)
				if got.ProtocolVersion != ProtocolVersion || len(got.Spaces) != 1 {
					return fmt.Errorf("snapshot=%#v", got)
				}
				return err
			},
		},
		{name: "Ping", method: "Ping", call: func(c *RemoteClient) error { return c.Ping(ctx) }},
		{name: "ClientHello", method: "ClientHello", call: func(c *RemoteClient) error { return c.ClientHello(ctx, 1234, "/dev/ttys000") }},
		{name: "DraftSave", method: "DraftSave", call: func(c *RemoteClient) error {
			return c.DraftSave(ctx, "draft-1", map[string]any{"body": "hello"})
		}},
		{
			name:   "DraftLoad",
			method: "DraftLoad",
			result: DraftLoadResult{Found: true, Payload: map[string]any{"body": "hello"}},
			call: func(c *RemoteClient) error {
				got, ok, err := c.DraftLoad(ctx, "draft-1")
				if !ok || got["body"] != "hello" {
					return fmt.Errorf("draft=%#v found=%v", got, ok)
				}
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			client := NewRemoteClientConn(clientConn)
			defer client.Close()

			serverErr := make(chan error, 1)
			go func() {
				env, err := ReadFrame(serverConn)
				if err != nil {
					serverErr <- err
					return
				}
				if env.Kind != "request" || env.Method != tc.method {
					serverErr <- fmt.Errorf("method=%q kind=%q want %q request", env.Method, env.Kind, tc.method)
					return
				}
				result, err := MarshalRaw(tc.result)
				if err != nil {
					serverErr <- err
					return
				}
				serverErr <- WriteFrame(serverConn, Envelope{ID: env.ID, Kind: "response", Result: result})
			}()

			if err := tc.call(client); err != nil {
				t.Fatal(err)
			}
			if err := <-serverErr; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRemoteClientSubscribeChatUsesEventStream(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewRemoteClientConn(clientConn)
	defer client.Close()

	serverErr := make(chan error, 1)
	go func() {
		env, err := ReadFrame(serverConn)
		if err != nil {
			serverErr <- err
			return
		}
		if env.Method != "SubscribeTopics" {
			serverErr <- fmt.Errorf("method=%q want SubscribeTopics", env.Method)
			return
		}
		if err := WriteFrame(serverConn, Envelope{ID: env.ID, Kind: "response"}); err != nil {
			serverErr <- err
			return
		}
		msg := ChatMessage{ID: "live-1", Space: "spaces/engineering", Text: "hello"}
		payload, _ := MarshalRaw(msg)
		serverErr <- WriteFrame(serverConn, Envelope{Kind: "event", Topic: "chat.message", Payload: payload})
	}()

	ch, err := client.SubscribeChat(context.Background(), "spaces/engineering")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-ch:
		if msg.ID != "live-1" {
			t.Fatalf("unexpected message: %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestRemoteClientSubscribeEventsUsesGenericEventStream(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewRemoteClientConn(clientConn)
	defer client.Close()

	serverErr := make(chan error, 1)
	go func() {
		env, err := ReadFrame(serverConn)
		if err != nil {
			serverErr <- err
			return
		}
		if env.Method != "SubscribeTopics" {
			serverErr <- fmt.Errorf("method=%q want SubscribeTopics", env.Method)
			return
		}
		if err := WriteFrame(serverConn, Envelope{ID: env.ID, Kind: "response"}); err != nil {
			serverErr <- err
			return
		}
		payload, _ := MarshalRaw(map[string]string{"source": "a", "path": "/tmp/a.png"})
		serverErr <- WriteFrame(serverConn, Envelope{Kind: "event", Topic: "image.cached", Payload: payload})
	}()

	ch, err := client.SubscribeEvents(context.Background(), []string{"image.cached"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-ch:
		if event.Topic != "image.cached" {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestRemoteClientReconnectsAfterSocketRestart(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "gws-remote-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "daemon.sock")

	first := serveOneRemoteResponse(t, socketPath, "Ping")
	client, err := NewRemoteClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)

	second := serveOneRemoteResponse(t, socketPath, "Ping")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

func serveOneRemoteResponse(t *testing.T, socketPath, method string) <-chan error {
	t.Helper()
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		defer os.Remove(socketPath)
		defer listener.Close()
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		env, err := ReadFrame(conn)
		if err != nil {
			done <- err
			return
		}
		if env.Method != method {
			done <- fmt.Errorf("method=%q want %q", env.Method, method)
			return
		}
		done <- WriteFrame(conn, Envelope{ID: env.ID, Kind: "response"})
	}()
	return done
}
