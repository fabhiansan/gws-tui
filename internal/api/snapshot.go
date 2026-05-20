package api

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const WorkspaceSnapshotVersion = 5

var ErrSnapshotLockBusy = errors.New("workspace snapshot lock is held by another process")

type WorkspaceSnapshot struct {
	ProtocolVersion     int                          `json:"protocol_version"`
	Version             int                          `json:"version"`
	SavedAt             time.Time                    `json:"saved_at"`
	Auth                AuthStatus                   `json:"auth,omitempty"`
	Spaces              []Space                      `json:"spaces,omitempty"`
	ChatMessagesBySpace map[string]Page[ChatMessage] `json:"chat_messages_by_space,omitempty"`
	MailLabels          []MailLabel                  `json:"mail_labels,omitempty"`
	MailThreads         Page[MailThread]             `json:"mail_threads,omitempty"`
	MailDrafts          Page[MailDraftItem]          `json:"mail_drafts,omitempty"`
	CalendarLists       []CalendarListItem           `json:"calendar_lists,omitempty"`
	CalendarID          string                       `json:"calendar_id,omitempty"`
	Events              Page[CalendarEvent]          `json:"events,omitempty"`
	MeetSpaces          []MeetSpace                  `json:"meet_spaces,omitempty"`
	TaskLists           []TaskList                   `json:"task_lists,omitempty"`
	Tasks               Page[TaskItem]               `json:"tasks,omitempty"`
	TaskListID          string                       `json:"task_list_id,omitempty"`
	DriveFiles          Page[DriveFile]              `json:"drive_files,omitempty"`
	DocFiles            Page[DriveFile]              `json:"doc_files,omitempty"`
	Doc                 DocDocument                  `json:"doc,omitempty"`
	UserLabels          map[string]string            `json:"user_labels,omitempty"`
	MembersBySpace      map[string][]SpaceMember     `json:"members_by_space,omitempty"`
	SelfUserIDs         map[string]bool              `json:"self_user_ids,omitempty"`
	PeopleAPIDown       bool                         `json:"people_api_down,omitempty"`
	PinnedSpaces        []string                     `json:"pinned_spaces,omitempty"`
	LastReadBySpace     map[string]time.Time         `json:"last_read_by_space,omitempty"`
}

func NewWorkspaceSnapshot() WorkspaceSnapshot {
	snapshot := WorkspaceSnapshot{
		ProtocolVersion: ProtocolVersion,
		Version:         WorkspaceSnapshotVersion,
	}
	snapshot.EnsureMaps()
	return snapshot
}

func LoadWorkspaceSnapshot(path string) (WorkspaceSnapshot, bool) {
	if path == "" {
		return NewWorkspaceSnapshot(), false
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return NewWorkspaceSnapshot(), false
	}
	var snapshot WorkspaceSnapshot
	if json.Unmarshal(payload, &snapshot) != nil || snapshot.Version != WorkspaceSnapshotVersion {
		return NewWorkspaceSnapshot(), false
	}
	snapshot.EnsureMaps()
	if snapshot.ProtocolVersion == 0 {
		snapshot.ProtocolVersion = ProtocolVersion
	}
	if snapshot.SavedAt.IsZero() || !snapshot.HasData() {
		return snapshot, false
	}
	return snapshot, true
}

func SaveWorkspaceSnapshot(path string, snapshot WorkspaceSnapshot) error {
	if path == "" {
		return nil
	}
	snapshot.ProtocolVersion = ProtocolVersion
	snapshot.Version = WorkspaceSnapshotVersion
	snapshot.SavedAt = time.Now()
	snapshot.EnsureMaps()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o600)
}

type SnapshotLock struct {
	file *os.File
	path string
}

func LockWorkspaceSnapshot(path string) (*SnapshotLock, error) {
	return lockWorkspaceSnapshot(path, false)
}

func TryLockWorkspaceSnapshot(path string) (*SnapshotLock, error) {
	return lockWorkspaceSnapshot(path, true)
}

func (s *WorkspaceSnapshot) EnsureMaps() {
	if s.ChatMessagesBySpace == nil {
		s.ChatMessagesBySpace = map[string]Page[ChatMessage]{}
	}
	if s.UserLabels == nil {
		s.UserLabels = map[string]string{}
	}
	if s.MembersBySpace == nil {
		s.MembersBySpace = map[string][]SpaceMember{}
	}
	if s.SelfUserIDs == nil {
		s.SelfUserIDs = map[string]bool{}
	}
	if s.LastReadBySpace == nil {
		s.LastReadBySpace = map[string]time.Time{}
	}
	if s.ProtocolVersion == 0 {
		s.ProtocolVersion = ProtocolVersion
	}
	if s.Version == 0 {
		s.Version = WorkspaceSnapshotVersion
	}
}

func (s WorkspaceSnapshot) HasData() bool {
	return len(s.Spaces) > 0 ||
		len(s.MailLabels) > 0 ||
		len(s.MailThreads.Items) > 0 ||
		len(s.MailDrafts.Items) > 0 ||
		len(s.CalendarLists) > 0 ||
		len(s.Events.Items) > 0 ||
		len(s.MeetSpaces) > 0 ||
		len(s.TaskLists) > 0 ||
		len(s.Tasks.Items) > 0 ||
		len(s.DriveFiles.Items) > 0 ||
		len(s.DocFiles.Items) > 0 ||
		s.Doc.ID != ""
}

// Clone returns a deep copy of the snapshot. Maps and the top-level slices of
// each owned collection are duplicated so the result can be marshalled or
// otherwise read concurrently with mutations on the original.
func (s WorkspaceSnapshot) Clone() WorkspaceSnapshot {
	out := s
	if s.Spaces != nil {
		out.Spaces = append([]Space(nil), s.Spaces...)
	}
	if s.ChatMessagesBySpace != nil {
		out.ChatMessagesBySpace = make(map[string]Page[ChatMessage], len(s.ChatMessagesBySpace))
		for k, page := range s.ChatMessagesBySpace {
			if page.Items != nil {
				page.Items = append([]ChatMessage(nil), page.Items...)
			}
			out.ChatMessagesBySpace[k] = page
		}
	}
	if s.MailLabels != nil {
		out.MailLabels = append([]MailLabel(nil), s.MailLabels...)
	}
	if s.MailThreads.Items != nil {
		out.MailThreads.Items = append([]MailThread(nil), s.MailThreads.Items...)
	}
	if s.MailDrafts.Items != nil {
		out.MailDrafts.Items = append([]MailDraftItem(nil), s.MailDrafts.Items...)
	}
	if s.CalendarLists != nil {
		out.CalendarLists = append([]CalendarListItem(nil), s.CalendarLists...)
	}
	if s.Events.Items != nil {
		out.Events.Items = append([]CalendarEvent(nil), s.Events.Items...)
	}
	if s.MeetSpaces != nil {
		out.MeetSpaces = append([]MeetSpace(nil), s.MeetSpaces...)
	}
	if s.TaskLists != nil {
		out.TaskLists = append([]TaskList(nil), s.TaskLists...)
	}
	if s.Tasks.Items != nil {
		out.Tasks.Items = append([]TaskItem(nil), s.Tasks.Items...)
	}
	if s.DriveFiles.Items != nil {
		out.DriveFiles.Items = append([]DriveFile(nil), s.DriveFiles.Items...)
	}
	if s.DocFiles.Items != nil {
		out.DocFiles.Items = append([]DriveFile(nil), s.DocFiles.Items...)
	}
	if s.UserLabels != nil {
		out.UserLabels = make(map[string]string, len(s.UserLabels))
		for k, v := range s.UserLabels {
			out.UserLabels[k] = v
		}
	}
	if s.MembersBySpace != nil {
		out.MembersBySpace = make(map[string][]SpaceMember, len(s.MembersBySpace))
		for k, members := range s.MembersBySpace {
			if members != nil {
				members = append([]SpaceMember(nil), members...)
			}
			out.MembersBySpace[k] = members
		}
	}
	if s.SelfUserIDs != nil {
		out.SelfUserIDs = make(map[string]bool, len(s.SelfUserIDs))
		for k, v := range s.SelfUserIDs {
			out.SelfUserIDs[k] = v
		}
	}
	if s.PinnedSpaces != nil {
		out.PinnedSpaces = append([]string(nil), s.PinnedSpaces...)
	}
	if s.LastReadBySpace != nil {
		out.LastReadBySpace = make(map[string]time.Time, len(s.LastReadBySpace))
		for k, v := range s.LastReadBySpace {
			out.LastReadBySpace[k] = v
		}
	}
	return out
}
