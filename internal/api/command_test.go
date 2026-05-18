package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
