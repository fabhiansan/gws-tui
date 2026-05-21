package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func TestReplyAllCcExcludesSelfAndSender(t *testing.T) {
	thread := api.MailThread{
		SenderEmail: "alice@example.com",
		To:          "Me <me@example.com>, Bob <bob@example.com>",
		Cc:          "Carol <carol@example.com>, alice@example.com",
	}
	cc := replyAllCc(&thread, "me@example.com")

	for _, banned := range []string{"me@example.com", "alice@example.com"} {
		if strings.Contains(strings.ToLower(cc), banned) {
			t.Fatalf("reply-all Cc should not contain %q: %q", banned, cc)
		}
	}
	for _, want := range []string{"bob@example.com", "carol@example.com"} {
		if !strings.Contains(strings.ToLower(cc), want) {
			t.Fatalf("reply-all Cc missing %q: %q", want, cc)
		}
	}
}

func TestReplyAllCcDeduplicates(t *testing.T) {
	thread := api.MailThread{
		SenderEmail: "alice@example.com",
		To:          "Bob <bob@example.com>",
		Cc:          "bob@example.com",
	}
	cc := replyAllCc(&thread, "me@example.com")
	if got := strings.Count(strings.ToLower(cc), "bob@example.com"); got != 1 {
		t.Fatalf("expected bob to appear once, got %d in %q", got, cc)
	}
}

func TestQuotedReplyBodyPutsReplyOnTop(t *testing.T) {
	thread := api.MailThread{
		Sender:      "Alice",
		SenderEmail: "alice@example.com",
		Date:        time.Date(2026, 5, 20, 9, 30, 0, 0, time.UTC),
		Body:        "first line\nsecond line",
	}
	body := quotedReplyBody(&thread)

	if !strings.HasPrefix(body, "\n\n") {
		t.Fatalf("reply body should open with blank space for the new reply: %q", body)
	}
	if !strings.Contains(body, "Alice <alice@example.com> wrote:") {
		t.Fatalf("reply body missing attribution line: %q", body)
	}
	if !strings.Contains(body, "> first line") || !strings.Contains(body, "> second line") {
		t.Fatalf("reply body should quote every line of the original: %q", body)
	}
	attribIdx := strings.Index(body, "wrote:")
	quoteIdx := strings.Index(body, "> first line")
	if attribIdx == -1 || quoteIdx == -1 || attribIdx > quoteIdx {
		t.Fatalf("attribution line must sit above the quoted original: %q", body)
	}
}
