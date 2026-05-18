package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/fabhiantomaoludyo/gws-tui/internal/api"
)

const workspaceCacheVersion = 2

type workspaceCache struct {
	Version             int                                  `json:"version"`
	SavedAt             time.Time                            `json:"saved_at"`
	Auth                api.AuthStatus                       `json:"auth,omitempty"`
	Spaces              []api.Space                          `json:"spaces,omitempty"`
	ChatMessagesBySpace map[string]api.Page[api.ChatMessage] `json:"chat_messages_by_space,omitempty"`
	MailLabels          []api.MailLabel                      `json:"mail_labels,omitempty"`
	MailThreads         api.Page[api.MailThread]             `json:"mail_threads,omitempty"`
	Events              api.Page[api.CalendarEvent]          `json:"events,omitempty"`
	MeetSpaces          []api.MeetSpace                      `json:"meet_spaces,omitempty"`
	UserLabels          map[string]string                    `json:"user_labels,omitempty"`
	MembersBySpace      map[string][]api.SpaceMember         `json:"members_by_space,omitempty"`
	SelfUserIDs         map[string]bool                      `json:"self_user_ids,omitempty"`
	PeopleAPIDown       bool                                 `json:"people_api_down,omitempty"`
}

func loadWorkspaceCache(path string) (workspaceCache, bool) {
	if path == "" {
		return newWorkspaceCache(), false
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return newWorkspaceCache(), false
	}
	var cache workspaceCache
	if json.Unmarshal(payload, &cache) != nil || cache.Version != workspaceCacheVersion {
		return newWorkspaceCache(), false
	}
	cache.ensureMaps()
	if cache.SavedAt.IsZero() || !cache.hasData() {
		return cache, false
	}
	return cache, true
}

func saveWorkspaceCache(path string, cache workspaceCache) error {
	if path == "" {
		return nil
	}
	cache.Version = workspaceCacheVersion
	cache.SavedAt = time.Now()
	cache.ensureMaps()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o600)
}

func newWorkspaceCache() workspaceCache {
	cache := workspaceCache{Version: workspaceCacheVersion}
	cache.ensureMaps()
	return cache
}

func (c *workspaceCache) ensureMaps() {
	if c.ChatMessagesBySpace == nil {
		c.ChatMessagesBySpace = map[string]api.Page[api.ChatMessage]{}
	}
	if c.UserLabels == nil {
		c.UserLabels = map[string]string{}
	}
	if c.MembersBySpace == nil {
		c.MembersBySpace = map[string][]api.SpaceMember{}
	}
	if c.SelfUserIDs == nil {
		c.SelfUserIDs = map[string]bool{}
	}
}

func (c workspaceCache) hasData() bool {
	return len(c.Spaces) > 0 ||
		len(c.MailLabels) > 0 ||
		len(c.MailThreads.Items) > 0 ||
		len(c.Events.Items) > 0 ||
		len(c.MeetSpaces) > 0
}

func (m *Model) hydrateWorkspaceCache(cache workspaceCache) {
	cache.ensureMaps()
	m.cache = cache
	m.auth = cache.Auth
	m.spaces = cache.Spaces
	m.mailLabels = cache.MailLabels
	m.mailThreads = cache.MailThreads.Items
	m.mailNext = cache.MailThreads.NextPageToken
	m.events = cache.Events.Items
	m.calendarNext = cache.Events.NextPageToken
	m.meetSpaces = cache.MeetSpaces
	m.userLabels = cache.UserLabels
	m.membersBySpace = cache.MembersBySpace
	m.selfUserIDs = cache.SelfUserIDs
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
	m.cache.ensureMaps()
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
	m.chatMessages = page.Items
	m.chatOlder = page.NextPageToken
	for _, chat := range m.chatMessages {
		m.seenMessages[chat.ID] = true
	}
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
	m.cache.ensureMaps()
	m.cache.ChatMessagesBySpace[spaceName] = page
}

func (m *Model) rememberChatMessage(message api.ChatMessage) {
	if message.ID == "" || message.Space == "" {
		return
	}
	m.cache.ensureMaps()
	page := m.cache.ChatMessagesBySpace[message.Space]
	for i := range page.Items {
		if page.Items[i].ID == message.ID {
			page.Items[i] = message
			m.cache.ChatMessagesBySpace[message.Space] = page
			return
		}
	}
	page.Items = append(page.Items, message)
	m.cache.ChatMessagesBySpace[message.Space] = page
}

func (m *Model) persistWorkspaceCache() {
	m.cache.Version = workspaceCacheVersion
	m.cache.Auth = m.auth
	m.cache.Spaces = m.spaces
	m.rememberCurrentChatPage()
	m.cache.MailLabels = m.mailLabels
	m.cache.MailThreads = api.Page[api.MailThread]{
		Items:         m.mailThreads,
		NextPageToken: m.mailNext,
	}
	m.cache.Events = api.Page[api.CalendarEvent]{
		Items:         m.events,
		NextPageToken: m.calendarNext,
	}
	m.cache.MeetSpaces = m.meetSpaces
	m.cache.UserLabels = m.userLabels
	m.cache.MembersBySpace = m.membersBySpace
	m.cache.SelfUserIDs = m.selfUserIDs
	m.cache.PeopleAPIDown = m.peopleAPIDown
	_ = saveWorkspaceCache(m.cfg.CachePath, m.cache)
}
