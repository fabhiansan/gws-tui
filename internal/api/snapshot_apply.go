package api

import (
	"sort"
	"strings"
	"time"
)

func ChatMessageKey(msg ChatMessage) string {
	if msg.Space != "" && msg.ID != "" {
		return msg.Space + "\x00" + msg.ID
	}
	if msg.Name != "" {
		return msg.Name
	}
	return ""
}

func SameChatMessage(a, b ChatMessage) bool {
	if key := ChatMessageKey(a); key != "" && key == ChatMessageKey(b) {
		return true
	}
	return a.Space != "" &&
		a.Space == b.Space &&
		a.SenderID == b.SenderID &&
		a.Text == b.Text &&
		!a.CreateTime.IsZero() &&
		a.CreateTime.Equal(b.CreateTime)
}

func UpsertChatMessage(items []ChatMessage, msg ChatMessage) ([]ChatMessage, bool) {
	for i := range items {
		if SameChatMessage(items[i], msg) {
			items[i] = msg
			return items, false
		}
	}
	return append(items, msg), true
}

func DedupeChatMessages(items []ChatMessage) []ChatMessage {
	if len(items) < 2 {
		return items
	}
	out := make([]ChatMessage, 0, len(items))
	for _, msg := range items {
		out, _ = UpsertChatMessage(out, msg)
	}
	return out
}

func ApplyChatPage(snapshot *WorkspaceSnapshot, spaceName string, page Page[ChatMessage]) Page[ChatMessage] {
	if snapshot == nil || spaceName == "" {
		return page
	}
	snapshot.EnsureMaps()
	page.Items = DedupeChatMessages(page.Items)
	snapshot.ChatMessagesBySpace[spaceName] = page
	return page
}

func MergeChatPage(snapshot *WorkspaceSnapshot, spaceName string, page Page[ChatMessage]) Page[ChatMessage] {
	if snapshot == nil || spaceName == "" {
		return page
	}
	snapshot.EnsureMaps()
	merged := snapshot.ChatMessagesBySpace[spaceName]
	for _, msg := range page.Items {
		merged.Items, _ = UpsertChatMessage(merged.Items, msg)
	}
	sortChatMessages(merged.Items)
	merged.NextPageToken = page.NextPageToken
	snapshot.ChatMessagesBySpace[spaceName] = merged
	return merged
}

func ApplyChatMessage(snapshot *WorkspaceSnapshot, msg ChatMessage) bool {
	if snapshot == nil || msg.ID == "" || msg.Space == "" {
		return false
	}
	snapshot.EnsureMaps()
	page := snapshot.ChatMessagesBySpace[msg.Space]
	var added bool
	page.Items, added = UpsertChatMessage(page.Items, msg)
	sortChatMessages(page.Items)
	snapshot.ChatMessagesBySpace[msg.Space] = page
	snapshot.Spaces = ApplyChatSpaceOrder(snapshot.Spaces, msg.Space)
	return added
}

func RemoveChatMessage(snapshot *WorkspaceSnapshot, messageName string) bool {
	if snapshot == nil || messageName == "" {
		return false
	}
	snapshot.EnsureMaps()
	spaceName := chatSpaceFromMessageName(messageName)
	messageID := chatMessageIDFromName(messageName)
	changed := false
	removeFromPage := func(page Page[ChatMessage]) Page[ChatMessage] {
		out := page.Items[:0]
		for _, msg := range page.Items {
			if msg.Name == messageName || (messageID != "" && msg.ID == messageID) {
				changed = true
				continue
			}
			out = append(out, msg)
		}
		page.Items = out
		return page
	}
	if spaceName != "" {
		if page, ok := snapshot.ChatMessagesBySpace[spaceName]; ok {
			snapshot.ChatMessagesBySpace[spaceName] = removeFromPage(page)
		}
		return changed
	}
	for name, page := range snapshot.ChatMessagesBySpace {
		snapshot.ChatMessagesBySpace[name] = removeFromPage(page)
	}
	return changed
}

func ApplyChatSpace(snapshot *WorkspaceSnapshot, space Space) bool {
	if snapshot == nil || space.Name == "" {
		return false
	}
	for i := range snapshot.Spaces {
		if snapshot.Spaces[i].Name == space.Name {
			snapshot.Spaces[i] = space
			return false
		}
	}
	snapshot.Spaces = append([]Space{space}, snapshot.Spaces...)
	return true
}

func ApplyChatSpaceOrder(spaces []Space, spaceName string) []Space {
	if spaceName == "" || len(spaces) < 2 {
		return spaces
	}
	for index, space := range spaces {
		if space.Name != spaceName {
			continue
		}
		if index == 0 {
			return spaces
		}
		out := make([]Space, 0, len(spaces))
		out = append(out, space)
		out = append(out, spaces[:index]...)
		out = append(out, spaces[index+1:]...)
		return out
	}
	return spaces
}

func MarkChatRead(snapshot *WorkspaceSnapshot, spaceName string, at time.Time) {
	if snapshot == nil || spaceName == "" {
		return
	}
	snapshot.EnsureMaps()
	if at.IsZero() {
		at = time.Now()
	}
	snapshot.LastReadBySpace[spaceName] = at
	for i := range snapshot.Spaces {
		if snapshot.Spaces[i].Name == spaceName {
			snapshot.Spaces[i].LastReadTime = at
			snapshot.Spaces[i].Unread = false
			break
		}
	}
}

func SyncLastReadMarkersFromSpaces(snapshot *WorkspaceSnapshot, spaces []Space) {
	if snapshot == nil {
		return
	}
	snapshot.EnsureMaps()
	for _, space := range spaces {
		if space.Name == "" || space.LastReadTime.IsZero() {
			continue
		}
		snapshot.LastReadBySpace[space.Name] = space.LastReadTime
	}
}

func InferChatUnread(snapshot WorkspaceSnapshot, spaceName string) (bool, bool) {
	if spaceName == "" {
		return false, false
	}
	page, ok := snapshot.ChatMessagesBySpace[spaceName]
	if !ok || len(page.Items) == 0 {
		return false, false
	}
	lastRead := snapshot.LastReadBySpace[spaceName]
	self := InferSelfUserIDs(snapshot.Spaces, snapshot.MembersBySpace, snapshot.SelfUserIDs)
	for i := len(page.Items) - 1; i >= 0; i-- {
		msg := page.Items[i]
		if isSelfUserID(msg.SenderID, self) {
			continue
		}
		if msg.CreateTime.After(lastRead) {
			return true, true
		}
	}
	return false, true
}

func ApplyMailPage(snapshot *WorkspaceSnapshot, folder string, page Page[MailThread]) {
	if snapshot == nil || folder == "" {
		return
	}
	snapshot.EnsureMaps()
	snapshot.MailThreadsByFolder[folder] = page
}

func ApplyMailThread(snapshot *WorkspaceSnapshot, thread MailThread) bool {
	if snapshot == nil || thread.ID == "" {
		return false
	}
	snapshot.EnsureMaps()
	found := false
	for folder, page := range snapshot.MailThreadsByFolder {
		for i := range page.Items {
			if page.Items[i].ID == thread.ID {
				page.Items[i] = thread
				snapshot.MailThreadsByFolder[folder] = page
				found = true
				break
			}
		}
	}
	if found {
		return false
	}
	page := snapshot.MailThreadsByFolder["Inbox"]
	page.Items = append([]MailThread{thread}, page.Items...)
	snapshot.MailThreadsByFolder["Inbox"] = page
	return true
}

func ApplyCalendarEvent(snapshot *WorkspaceSnapshot, event CalendarEvent) bool {
	if snapshot == nil || event.ID == "" {
		return false
	}
	for i := range snapshot.Events.Items {
		if snapshot.Events.Items[i].ID == event.ID {
			snapshot.Events.Items[i] = event
			return false
		}
	}
	snapshot.Events.Items = append(snapshot.Events.Items, event)
	return true
}

func ApplyMeetSpace(snapshot *WorkspaceSnapshot, space MeetSpace) bool {
	if snapshot == nil || space.Name == "" {
		return false
	}
	for i := range snapshot.MeetSpaces {
		if snapshot.MeetSpaces[i].Name == space.Name {
			snapshot.MeetSpaces[i] = space
			return false
		}
	}
	snapshot.MeetSpaces = append([]MeetSpace{space}, snapshot.MeetSpaces...)
	return true
}

func ApplyTask(snapshot *WorkspaceSnapshot, task TaskItem) bool {
	if snapshot == nil || task.ID == "" {
		return false
	}
	if task.TaskListID != "" {
		if snapshot.TaskListID == "" {
			snapshot.TaskListID = task.TaskListID
		}
		if snapshot.TaskListID != task.TaskListID {
			return false
		}
	}
	for i := range snapshot.Tasks.Items {
		if snapshot.Tasks.Items[i].ID == task.ID {
			snapshot.Tasks.Items[i] = task
			return false
		}
	}
	if snapshot.TaskListID == task.TaskListID {
		snapshot.Tasks.Items = append(snapshot.Tasks.Items, task)
		return true
	}
	return false
}

func RemoveTask(snapshot *WorkspaceSnapshot, taskListID, taskID string) bool {
	if snapshot == nil || taskID == "" {
		return false
	}
	if taskListID != "" && snapshot.TaskListID != "" && snapshot.TaskListID != taskListID {
		return false
	}
	out := snapshot.Tasks.Items[:0]
	changed := false
	for _, task := range snapshot.Tasks.Items {
		if task.ID == taskID {
			changed = true
			continue
		}
		out = append(out, task)
	}
	snapshot.Tasks.Items = out
	return changed
}

func sortChatMessages(items []ChatMessage) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreateTime.IsZero() || items[j].CreateTime.IsZero() {
			return false
		}
		return items[i].CreateTime.Before(items[j].CreateTime)
	})
}

func isSelfUserID(value string, self map[string]bool) bool {
	key := NormalizeUserID(value)
	return key != "" && self[key]
}

func chatSpaceFromMessageName(name string) string {
	if idx := strings.Index(name, "/messages/"); idx > 0 {
		return name[:idx]
	}
	return ""
}

func chatMessageIDFromName(name string) string {
	if idx := strings.LastIndex(name, "/messages/"); idx >= 0 {
		return name[idx+len("/messages/"):]
	}
	return ""
}
