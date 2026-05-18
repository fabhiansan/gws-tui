package api

import (
	"context"
	"strings"
	"testing"
)

func TestFixtureClientMutations(t *testing.T) {
	client := NewFixtureClient()
	ctx := context.Background()

	sent, err := client.SendChatMessage(ctx, "spaces/engineering", "deploy started")
	if err != nil {
		t.Fatal(err)
	}
	if sent.Pending || !strings.Contains(sent.Text, "deploy") {
		t.Fatalf("unexpected sent message: %#v", sent)
	}

	thread, err := client.ToggleStar(ctx, "mail-1")
	if err != nil {
		t.Fatal(err)
	}
	if !thread.Starred {
		t.Fatal("expected mail-1 to be starred")
	}

	event, err := client.RSVPEvent(ctx, "event-1", "accepted")
	if err != nil {
		t.Fatal(err)
	}
	if event.RSVP != "accepted" {
		t.Fatalf("unexpected RSVP: %s", event.RSVP)
	}

	meet, err := client.CreateMeetSpace(ctx, "retro may")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(meet.MeetingURI, "retro-may") {
		t.Fatalf("unexpected meet uri: %s", meet.MeetingURI)
	}
}
