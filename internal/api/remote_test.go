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
				got, err := c.SendChatMessage(ctx, "spaces/engineering", "hello")
				if got.ID != "sent-1" {
					return fmt.Errorf("sent=%#v", got)
				}
				return err
			},
		},
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
