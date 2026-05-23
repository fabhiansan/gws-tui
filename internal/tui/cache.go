package tui

import (
	"strings"
	"time"

	"github.com/fabhiansan/gws-tui/internal/api"
)

const workspaceCacheVersion = api.WorkspaceSnapshotVersion

type workspaceCache = api.WorkspaceSnapshot

func loadWorkspaceCache(path string) (workspaceCache, bool) {
	return api.LoadWorkspaceSnapshot(path)
}

func saveWorkspaceCache(path string, cache workspaceCache) error {
	lock, err := api.TryLockWorkspaceSnapshot(path)
	if err != nil {
		return err
	}
	defer lock.Release()
	return api.SaveWorkspaceSnapshot(path, cache)
}

func newWorkspaceCache() workspaceCache {
	return api.NewWorkspaceSnapshot()
}

func (m *Model) hydrateWorkspaceCache(cache workspaceCache) {
	cache.EnsureMaps()
	m.cache = cache
	m.auth = cache.Auth
	m.spaces = cache.Spaces
	m.mailLabels = cache.MailLabels
	m.mailFolder = cache.MailFolder
	if m.mailFolder == "" {
		m.mailFolder = defaultMailFolder
	}
	m.applyCachedSelectedMail()
	m.calendars = cache.CalendarLists
	m.calendarIndex = indexOfCalendar(cache.CalendarLists, cache.CalendarID)
	m.events = sortedEvents(cache.Events.Items)
	m.calendarNext = cache.Events.NextPageToken
	m.calendarMonth = cache.CalendarMonth
	if !m.calendarMonth.IsZero() {
		m.monthEvents = sortedEvents(cache.Events.Items)
		m.events = m.monthEvents
		m.calendarNext = ""
	}
	m.meetSpaces = cache.MeetSpaces
	m.taskLists = cache.TaskLists
	m.taskListIndex = indexOfTaskList(cache.TaskLists, cache.TaskListID)
	m.tasks = cache.Tasks.Items
	m.taskNext = cache.Tasks.NextPageToken
	m.driveFiles = cache.DriveFiles.Items
	m.driveNext = cache.DriveFiles.NextPageToken
	m.docFiles = cache.DocFiles.Items
	m.docNext = cache.DocFiles.NextPageToken
	m.doc = cache.Doc
	m.userLabels = cache.UserLabels
	m.membersBySpace = cache.MembersBySpace
	m.selfUserIDs = cache.SelfUserIDs
	m.normalizeUserCaches()
	m.peopleAPIDown = cache.PeopleAPIDown
	m.clampSelections()
	if m.persisted.LastSpace != "" {
		for index, space := range m.spaces {
			if space.Name == m.persisted.LastSpace {
				m.selected[FeatureChat] = index
				break
			}
		}
	}
	m.applyCachedSelectedChat()
}

func (m *Model) applyCachedSelectedChat() bool {
	m.cache.EnsureMaps()
	space := m.selectedSpace()
	if space.Name == "" {
		m.chatMessages = nil
		m.chatOlder = ""
		return false
	}
	page, ok := m.cache.ChatMessagesBySpace[space.Name]
	if !ok {
		m.chatMessages = nil
		m.chatOlder = ""
		return false
	}
	m.chatMessages = dedupeChatMessages(page.Items)
	m.chatOlder = page.NextPageToken
	m.markSeenChatMessages(m.chatMessages)
	return true
}

func (m *Model) rememberCurrentChatPage() {
	space := m.selectedSpace()
	if space.Name == "" {
		return
	}
	m.rememberChatPage(space.Name, api.Page[api.ChatMessage]{
		Items:         m.chatMessages,
		NextPageToken: m.chatOlder,
	})
}

func (m *Model) rememberChatPage(spaceName string, page api.Page[api.ChatMessage]) {
	if spaceName == "" {
		return
	}
	api.ApplyChatPage(&m.cache, spaceName, page)
}

func (m *Model) rememberChatMessage(message api.ChatMessage) {
	if message.ID == "" || message.Space == "" {
		return
	}
	api.ApplyChatMessage(&m.cache, message)
}

// applyCachedSelectedMail restores the thread list for the currently selected
// mail folder from the cache, mirroring applyCachedSelectedChat. It returns
// true when the folder had a cached page so callers can skip the network.
func (m *Model) applyCachedSelectedMail() bool {
	m.cache.EnsureMaps()
	if m.mailFolder == "" {
		m.mailThreads = nil
		m.mailNext = ""
		return false
	}
	page, ok := m.cache.MailThreadsByFolder[m.mailFolder]
	if !ok {
		m.mailThreads = nil
		m.mailNext = ""
		return false
	}
	m.mailThreads = page.Items
	m.mailNext = page.NextPageToken
	return true
}

// rememberCurrentMailPage stores the in-memory thread list under the active
// folder. Search results are skipped so they never pollute a folder's cache.
func (m *Model) rememberCurrentMailPage() {
	if strings.TrimSpace(m.search) != "" {
		return
	}
	m.rememberMailPage(m.mailFolder, api.Page[api.MailThread]{
		Items:         m.mailThreads,
		NextPageToken: m.mailNext,
	})
}

func (m *Model) rememberMailPage(folder string, page api.Page[api.MailThread]) {
	if folder == "" {
		return
	}
	api.ApplyMailPage(&m.cache, folder, page)
}

// rememberMailThread upserts a single thread into every cached folder page that
// already contains it, keeping non-active folders fresh on realtime updates.
func (m *Model) rememberMailThread(thread api.MailThread) {
	if thread.ID == "" {
		return
	}
	api.ApplyMailThread(&m.cache, thread)
}

func (m *Model) persistWorkspaceCache() {
	if m.cfg.Daemon {
		return
	}
	m.cache.Version = workspaceCacheVersion
	m.cache.Auth = m.auth
	m.cache.Spaces = m.spaces
	m.rememberCurrentChatPage()
	m.cache.MailLabels = m.mailLabels
	m.rememberCurrentMailPage()
	m.cache.MailFolder = m.mailFolder
	calendarEvents := m.events
	calendarNext := m.calendarNext
	calendarMonth := time.Time{}
	if !m.calendarMonth.IsZero() {
		calendarEvents = m.monthEvents
		calendarNext = ""
		calendarMonth = m.calendarMonth
	}
	m.cache.Events = api.Page[api.CalendarEvent]{
		Items:         calendarEvents,
		NextPageToken: calendarNext,
	}
	m.cache.CalendarLists = m.calendars
	m.cache.CalendarID = m.selectedCalendar().ID
	m.cache.CalendarMonth = calendarMonth
	m.cache.MeetSpaces = m.meetSpaces
	m.cache.TaskLists = m.taskLists
	m.cache.Tasks = api.Page[api.TaskItem]{
		Items:         m.tasks,
		NextPageToken: m.taskNext,
	}
	m.cache.TaskListID = m.selectedTaskList().ID
	m.cache.DriveFiles = api.Page[api.DriveFile]{
		Items:         m.driveFiles,
		NextPageToken: m.driveNext,
	}
	m.cache.DocFiles = api.Page[api.DriveFile]{
		Items:         m.docFiles,
		NextPageToken: m.docNext,
	}
	m.cache.Doc = m.doc
	m.cache.UserLabels = m.userLabels
	m.cache.MembersBySpace = m.membersBySpace
	m.cache.SelfUserIDs = m.selfUserIDs
	m.cache.PeopleAPIDown = m.peopleAPIDown
	_ = saveWorkspaceCache(m.cfg.CachePath, m.cache)
}
