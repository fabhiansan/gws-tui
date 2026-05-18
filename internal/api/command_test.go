package api

import (
	"context"
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

func TestCommandClientSubscribeChatStreamsCloudEvents(t *testing.T) {
	t.Setenv("GWS_FAKE_COMMAND", "1")
	t.Setenv("GWS_EVENTS_PROJECT", "test-project")

	client := NewCommandClient(os.Args[0])
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
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streamed chat event")
	}
}

func emitFakeChatEventStream() {
	target := ""
	for i, arg := range os.Args {
		if arg == "--target" && i+1 < len(os.Args) {
			target = os.Args[i+1]
			break
		}
	}
	if target != "//chat.googleapis.com/spaces/engineering" {
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
}

func fakeCommand() {
	// `events +subscribe` does not take --params; handle it before requiring one.
	if len(os.Args) >= 3 && os.Args[1] == "events" && os.Args[2] == "+subscribe" {
		emitFakeChatEventStream()
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
	default:
		fmt.Fprintf(os.Stderr, "unexpected command: %s\n", strings.Join(os.Args[1:], " "))
		os.Exit(2)
	}
}
