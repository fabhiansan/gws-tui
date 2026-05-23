package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func (m Model) whoamiCmd() tea.Cmd {
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		person, err := client.PeopleGet(c, "me")
		if err != nil {
			if api.IsPeopleAPIUnavailable(err) {
				return selfResolvedMsg{err: err, apiAbsent: true}
			}
			return selfResolvedMsg{err: err}
		}
		label := person.DisplayName
		if label == "" {
			label = person.Email
		}
		return selfResolvedMsg{userID: person.UserID, label: label, email: person.Email}
	}
}

func (m *Model) resolveUserCmd(userID string) tea.Cmd {
	userID = normalizeUserKey(userID)
	if userID == "" || m.peopleAPIDown {
		return nil
	}
	if label, ok := m.userLabels[userID]; ok && label != "" && label != userID && label != "users/"+userID {
		return nil
	}
	if m.pendingUsers[userID] {
		return nil
	}
	m.pendingUsers[userID] = true
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		person, err := client.PeopleGet(c, userID)
		if err != nil {
			if api.IsPeopleAPIUnavailable(err) {
				return userResolvedMsg{userID: userID, err: err, apiAbsent: true}
			}
			return userResolvedMsg{userID: userID, err: err}
		}
		label := person.DisplayName
		if label == "" {
			label = person.Email
		}
		if label == "" {
			label = userID
		}
		return userResolvedMsg{userID: userID, label: label}
	}
}

func (m *Model) loadMembersCmd(space api.Space) tea.Cmd {
	if space.Name == "" {
		return nil
	}
	if !space.UsesMemberLabels() {
		return nil
	}
	if _, ok := m.membersBySpace[space.Name]; ok {
		return nil
	}
	if m.pendingMembers[space.Name] {
		return nil
	}
	m.pendingMembers[space.Name] = true
	client := m.client
	ctx := m.ctx
	name := space.Name
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		members, err := client.ChatMembers(c, name)
		return membersLoadedMsg{spaceName: name, members: members, err: err}
	}
}

func (m *Model) enrichSpacesCmds() []tea.Cmd {
	var cmds []tea.Cmd
	for _, space := range m.spaces {
		for _, member := range m.membersBySpace[space.Name] {
			if member.Type != "" && member.Type != "HUMAN" {
				continue
			}
			if m.isSelfUserID(member.UserID) {
				continue
			}
			if cmd := m.resolveUserCmd(member.UserID); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if cmd := m.loadMembersCmd(space); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func (m *Model) enrichSendersCmds() []tea.Cmd {
	var cmds []tea.Cmd
	for _, msg := range m.chatMessages {
		userID := api.UserIDFromName(msg.SenderID)
		if userID == "" {
			continue
		}
		if cmd := m.resolveUserCmd(userID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func (m Model) loadChatSectionCmd() tea.Cmd {
	selected := m.selected[FeatureChat]
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		spaces, spacesErr := m.client.ChatSpaces(ctx)
		if spacesErr != nil {
			return chatSectionLoadedMsg{err: spacesErr}
		}
		spaceName := ""
		if len(spaces.Items) > 0 {
			spaceName = spaces.Items[clamp(selected, len(spaces.Items))].Name
		}
		messages, messagesErr := m.client.ChatMessages(ctx, spaceName, "")
		return chatSectionLoadedMsg{spaces: spaces, messages: messages, spaceName: spaceName, messagesErr: messagesErr}
	}
}

func (m *Model) startChatLoad(spaceName string) int {
	m.chatLoadID++
	if m.chatLoadIDs == nil {
		m.chatLoadIDs = map[string]int{}
	}
	m.chatLoadIDs[spaceName] = m.chatLoadID
	return m.chatLoadID
}

func (m *Model) finishChatLoad(spaceName string, loadID int) bool {
	if m.chatLoadIDs == nil {
		return loadID == m.chatLoadID
	}
	latest, ok := m.chatLoadIDs[spaceName]
	if !ok {
		return loadID == m.chatLoadID
	}
	if latest != loadID {
		return false
	}
	delete(m.chatLoadIDs, spaceName)
	return true
}

func (m Model) refreshSelectedChat() (Model, tea.Cmd) {
	space := m.selectedSpace()
	if space.Name == "" {
		m.chatMessages = nil
		m.chatOlder = ""
		m.chatLoading = false
		m.chatLoadSpace = ""
		m.loading = false
		return m, nil
	}

	spaceName := space.Name
	loadID := m.startChatLoad(spaceName)
	m.loading = true
	m.chatLoading = true
	m.chatLoadSpace = spaceName
	m.chatMessages = nil
	m.chatOlder = ""

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()

		page, err := m.client.ChatMessages(ctx, spaceName, "")
		return chatLoadedMsg{spaceName: spaceName, loadID: loadID, messages: page, err: err, refresh: true}
	}
}

func (m Model) subscribeCmd() tea.Cmd {
	space := m.selectedSpace()
	if space.Name == "" || !space.Live {
		return nil
	}
	return func() tea.Msg {
		ch, err := m.client.SubscribeChat(m.ctx, space.Name)
		if err != nil {
			return realtimeMsg{err: err}
		}
		select {
		case <-m.ctx.Done():
			return realtimeMsg{}
		case msg, ok := <-ch:
			if !ok {
				return realtimeMsg{err: api.ErrRemoteClosed}
			}
			return realtimeMsg{message: msg}
		}
	}
}

func (m Model) chatNotifyTitle(spaceName string) string {
	if spaceName == "" {
		return "gws chat"
	}
	for _, space := range m.spaces {
		if space.Name == spaceName {
			if label := m.spaceLabel(space); label != "" {
				return "gws chat · " + label
			}
			break
		}
	}
	return "gws chat"
}

func (m Model) markChatReadCmd(spaceName string) tea.Cmd {
	if spaceName == "" {
		return nil
	}
	reader, ok := m.client.(api.ChatReader)
	if !ok {
		return nil
	}
	parent := m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 8*time.Second)
		defer cancel()
		_ = reader.MarkChatRead(ctx, spaceName)
		return nil
	}
}

func (m Model) daemonEventCmd() tea.Cmd {
	subscriber, ok := m.client.(interface {
		SubscribeEvents(context.Context, []string) (<-chan api.DaemonEvent, error)
	})
	if !ok {
		return nil
	}
	topics := []string{"image.cached", "notify", "chat.message", "chat.history.loaded", "chat.read", "auth.changed", "mail.changed", "calendar.changed", "meet.changed"}
	events := m.daemonEvents
	return func() tea.Msg {
		ch := events
		if ch == nil {
			var err error
			ch, err = subscriber.SubscribeEvents(m.ctx, topics)
			if err != nil {
				return daemonEventMsg{err: err}
			}
		}
		select {
		case <-m.ctx.Done():
			return daemonEventMsg{events: ch}
		case event, ok := <-ch:
			if !ok {
				return daemonEventMsg{err: api.ErrRemoteClosed}
			}
			return daemonEventMsg{events: ch, event: event}
		}
	}
}
