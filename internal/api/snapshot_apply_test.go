package api

import (
	"testing"
	"time"
)

func TestApplyChatMessageDedupesAndHydratesUnopenedSpace(t *testing.T) {
	snapshot := NewWorkspaceSnapshot()
	snapshot.Spaces = []Space{
		{Name: "spaces/engineering", DisplayName: "Engineering"},
		{Name: "spaces/design", DisplayName: "Design"},
	}
	msg := ChatMessage{
		ID:         "design-1",
		Name:       "spaces/design/messages/design-1",
		Space:      "spaces/design",
		SenderID:   "users/alice",
		SenderName: "Alice",
		Text:       "new from daemon",
		CreateTime: time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC),
	}

	if added := ApplyChatMessage(&snapshot, msg); !added {
		t.Fatal("first message should be added")
	}
	if added := ApplyChatMessage(&snapshot, msg); added {
		t.Fatal("duplicate message should update, not add")
	}

	page := snapshot.ChatMessagesBySpace["spaces/design"]
	if len(page.Items) != 1 || page.Items[0].ID != "design-1" {
		t.Fatalf("message was not hydrated into unopened-space cache: %#v", page.Items)
	}
	if snapshot.Spaces[0].Name != "spaces/design" {
		t.Fatalf("incoming message should promote its space, got %#v", snapshot.Spaces)
	}
}

func TestInferChatUnreadUsesReadMarkersAndSelfInference(t *testing.T) {
	created := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	snapshot := NewWorkspaceSnapshot()
	snapshot.Spaces = []Space{{Name: "spaces/dm", SpaceType: "DIRECT_MESSAGE"}}
	snapshot.MembersBySpace["spaces/dm"] = []SpaceMember{
		{UserID: "users/me", Type: "HUMAN"},
		{UserID: "users/alice", Type: "HUMAN"},
	}
	snapshot.SelfUserIDs["me"] = true
	snapshot.ChatMessagesBySpace["spaces/dm"] = Page[ChatMessage]{Items: []ChatMessage{
		{ID: "self", Space: "spaces/dm", SenderID: "users/me", Text: "from me", CreateTime: created.Add(time.Minute)},
		{ID: "alice", Space: "spaces/dm", SenderID: "users/alice", Text: "from alice", CreateTime: created},
	}}

	unread, ok := InferChatUnread(snapshot, "spaces/dm")
	if !ok || !unread {
		t.Fatalf("alice message after zero read marker should be unread, unread=%v ok=%v", unread, ok)
	}

	MarkChatRead(&snapshot, "spaces/dm", created.Add(2*time.Minute))
	unread, ok = InferChatUnread(snapshot, "spaces/dm")
	if !ok || unread {
		t.Fatalf("read marker after non-self message should clear unread, unread=%v ok=%v", unread, ok)
	}
	if snapshot.Spaces[0].Unread {
		t.Fatalf("MarkChatRead should clear space unread: %#v", snapshot.Spaces[0])
	}
}

func TestApplyMailThreadUpdatesAllCachedFolders(t *testing.T) {
	snapshot := NewWorkspaceSnapshot()
	old := MailThread{ID: "thread-1", Subject: "Old"}
	updated := MailThread{ID: "thread-1", Subject: "Updated", Starred: true}
	snapshot.MailThreadsByFolder["Inbox"] = Page[MailThread]{Items: []MailThread{old}}
	snapshot.MailThreadsByFolder["Starred"] = Page[MailThread]{Items: []MailThread{old}}

	if added := ApplyMailThread(&snapshot, updated); added {
		t.Fatal("existing thread should update, not add")
	}
	for _, folder := range []string{"Inbox", "Starred"} {
		page := snapshot.MailThreadsByFolder[folder]
		if len(page.Items) != 1 || page.Items[0].Subject != "Updated" || !page.Items[0].Starred {
			t.Fatalf("%s cache was not updated: %#v", folder, page.Items)
		}
	}

	newThread := MailThread{ID: "thread-2", Subject: "New"}
	if added := ApplyMailThread(&snapshot, newThread); !added {
		t.Fatal("new thread should be inserted into Inbox")
	}
	if snapshot.MailThreadsByFolder["Inbox"].Items[0].ID != "thread-2" {
		t.Fatalf("new thread should be prepended to Inbox: %#v", snapshot.MailThreadsByFolder["Inbox"].Items)
	}
}

func TestMergeChatPagePreservesReadMarkers(t *testing.T) {
	readAt := time.Date(2026, 5, 21, 9, 30, 0, 0, time.UTC)
	snapshot := NewWorkspaceSnapshot()
	snapshot.LastReadBySpace["spaces/engineering"] = readAt

	MergeChatPage(&snapshot, "spaces/engineering", Page[ChatMessage]{Items: []ChatMessage{
		{ID: "m1", Space: "spaces/engineering", Text: "one", CreateTime: readAt.Add(time.Minute)},
	}})

	if got := snapshot.LastReadBySpace["spaces/engineering"]; !got.Equal(readAt) {
		t.Fatalf("chat page merge should not rewrite read marker, got %v want %v", got, readAt)
	}
}
