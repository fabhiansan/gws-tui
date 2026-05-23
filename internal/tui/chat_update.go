package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
	"github.com/fabhiansan/gws-tui/internal/tui/notify"
)

const defaultChatReaction = "\U0001F44D"

// clearPendingChatAttachments drops every staged upload and deletes the temp
// files. Bound to Ctrl+X so users can abort an accidental paste without having
// to send a junk message.
func (m Model) clearPendingChatAttachments() Model {
	for _, att := range m.pendingChatAttachments {
		_ = os.Remove(att.path)
	}
	n := len(m.pendingChatAttachments)
	m.pendingChatAttachments = nil
	if n == 1 {
		m.toast = "attachment cleared"
	} else {
		m.toast = fmt.Sprintf("%d attachments cleared", n)
	}
	return m
}

// handleChatPaste runs when the user presses Ctrl+V while the chat feature is
// active. An image on the clipboard becomes a pending attachment (and focuses
// the composer so the next keystroke is either send-text or send-image).
// Without an image we fall back to inserting clipboard text into the composer
// when it is focused — that mirrors what users expect Ctrl+V to do.
func (m Model) handleChatPaste() (Model, tea.Cmd) {
	if m.feature != FeatureChat {
		return m, nil
	}
	tmp, err := os.CreateTemp("", "gws-paste-*.png")
	if err != nil {
		m.toast = "paste: " + err.Error()
		return m, nil
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		m.toast = "paste: " + err.Error()
		return m, nil
	}
	mime, err := pasteImageTo(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		// No image on clipboard — degrade gracefully to text paste when
		// the composer is focused, otherwise just inform the user.
		if m.focusedPane == paneAction {
			if text, perr := pasteText(); perr == nil && text != "" {
				m.input.InsertString(text)
				return m, nil
			}
		}
		m.toast = "no image on clipboard"
		return m, nil
	}
	name := fmt.Sprintf("paste-%d.png", time.Now().Unix())
	m.pendingChatAttachments = append(m.pendingChatAttachments, pendingAttachment{
		path:        tmpPath,
		contentType: mime,
		name:        name,
	})
	// Move focus to the composer so Enter sends right away — the message
	// pane is read-only, so leaving the user there would force an extra
	// keystroke to switch panes before sending.
	m.focusedPane = paneAction
	m.input.Focus()
	if m.cfg.VimMode {
		m.vimComposer = vimModeInsert
	}
	if n := len(m.pendingChatAttachments); n == 1 {
		m.toast = "image attached - Enter to send"
	} else {
		m.toast = fmt.Sprintf("image attached (%d pending) - Enter to send", n)
	}
	return m, nil
}

// chatMessageUnderCursor returns the chat message the detail-pane cursor is
// currently resting on. Returns ok=false when the cursor is off-message
// (day separator, blank line, list pane focused) so callers can fall back to
// other behavior — used by the `r` keybinding to choose between reply and
// refresh without clobbering refresh in views where there's no cursor.
func (m *Model) chatMessageUnderCursor() (api.ChatMessage, bool) {
	if m.feature != FeatureChat || len(m.chatMessages) == 0 {
		return api.ChatMessage{}, false
	}
	id, ok := m.detailMessageAt[m.detailCursor]
	if !ok || id == "" {
		return api.ChatMessage{}, false
	}
	for i := range m.chatMessages {
		if m.chatMessages[i].ID == id {
			return m.chatMessages[i], true
		}
	}
	return api.ChatMessage{}, false
}

func (m *Model) beginThreadReply(target api.ChatMessage) {
	m.editMessageName = ""
	m.editMessageID = ""
	m.createSpaceMode = false
	thread := target.ThreadID
	if thread == "" {
		// No thread metadata — sending without thread context creates a
		// new top-level message instead of failing silently.
		m.toast = "no thread info; sending as new message"
		thread = ""
	}
	m.replyThreadID = thread
	m.replyTargetName = target.SenderName
	m.focusedPane = paneAction
	m.input.Placeholder = fmt.Sprintf("reply to %s (esc to cancel)", target.SenderName)
	m.input.Focus()
	m.vimComposer = vimModeInsert
	if thread != "" {
		m.toast = "replying to " + target.SenderName
	}
}

func (m *Model) beginEditChatMessage(target api.ChatMessage) {
	name := target.Name
	if name == "" && target.Space != "" && target.ID != "" {
		name = target.Space + "/messages/" + target.ID
	}
	if name == "" {
		m.toast = "message name unavailable"
		return
	}
	m.replyThreadID = ""
	m.replyTargetName = ""
	m.createSpaceMode = false
	m.editMessageName = name
	m.editMessageID = target.ID
	m.focusedPane = paneAction
	m.input.SetValue(target.Text)
	m.input.Placeholder = "edit message (esc to cancel)"
	m.input.Focus()
	m.input.CursorEnd()
	m.vimComposer = vimModeInsert
	m.toast = "editing message"
}

func (m *Model) beginCreateChatSpace() {
	m.replyThreadID = ""
	m.replyTargetName = ""
	m.editMessageName = ""
	m.editMessageID = ""
	m.createSpaceMode = true
	m.focusedPane = paneAction
	m.input.SetValue("")
	m.input.Placeholder = "new space name | user@example.com, user2@example.com"
	m.input.Focus()
	m.vimComposer = vimModeInsert
	m.toast = "creating chat space"
}

func parseChatSpaceSetupInput(value string) (string, []string) {
	name, memberText, hasMembers := strings.Cut(value, "|")
	name = strings.TrimSpace(name)
	if !hasMembers {
		return name, nil
	}
	return name, splitCSV(memberText)
}

func (m *Model) clearReplyContext() {
	m.replyThreadID = ""
	m.replyTargetName = ""
	m.editMessageName = ""
	m.editMessageID = ""
	m.createSpaceMode = false
	m.input.Placeholder = "message"
}

func (m *Model) applyIncomingChatMessage(message api.ChatMessage, sendStandaloneNotify bool) []tea.Cmd {
	var cmds []tea.Cmd
	if chatMessageKey(message) == "" || m.hasSeenChatMessage(message) {
		return cmds
	}
	m.markSeenChatMessage(message)
	m.rememberChatMessage(message)
	m.promoteSpaceToTop(message.Space)
	// selfUserIDs is keyed by the bare numeric id; message.SenderID has the
	// "users/" resource prefix. Normalize before lookup.
	senderBareID := api.UserIDFromName(message.SenderID)
	fromSelf := m.isSelfUserID(senderBareID)
	if m.isSelectedSpace(message.Space) {
		m.chatMessages, _ = upsertChatMessage(m.chatMessages, message)
		// User is actively viewing; keep the daemon's read marker in sync so the
		// badge doesn't reappear on reconnect.
		cmds = append(cmds, m.markChatReadCmd(message.Space))
	} else if !fromSelf {
		m.setSpaceUnread(message.Space, true)
	}
	if !fromSelf {
		m.toast = "new chat message"
	}
	if cmd := m.resolveUserCmd(senderBareID); cmd != nil {
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, m.imageDownloadCmdsForChat([]api.ChatMessage{message})...)
	if sendStandaloneNotify && !fromSelf && !m.cfg.Daemon && (m.feature != FeatureChat || !m.isSelectedSpace(message.Space)) {
		notify.Send(m.chatNotifyTitle(message.Space), message.SenderName+": "+message.Text, notify.Options{
			Desktop:   m.cfg.NotifyDesktop,
			Sound:     m.cfg.NotifySound,
			SoundFile: m.cfg.NotifySoundFile,
		})
	}
	m.persistWorkspaceCache()
	return cmds
}

func (m *Model) openSpaceFilter() {
	if !m.spaceFilterActive {
		m.spaceFilterOrigin = m.selectedSpace().Name
	}
	m.spaceFilterActive = true
	m.focusedPane = paneList
	m.toast = ""
}

func (m Model) updateSpaceFilter(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.persist()
		m.cancel()
		return m, tea.Quit
	case "esc":
		m.spaceFilterActive = false
		m.spaceFilter = ""
		m.restoreSpaceFilterOrigin()
		m.spaceFilterOrigin = ""
		return m, nil
	case "enter":
		m.spaceFilterActive = false
		m.spaceFilterOrigin = ""
		return m.loadSelectedChat()
	case "backspace", "ctrl+h":
		value := []rune(m.spaceFilter)
		if len(value) > 0 {
			m.setSpaceFilter(string(value[:len(value)-1]))
		}
		return m, nil
	case "ctrl+u":
		m.setSpaceFilter("")
		return m, nil
	case "up":
		m.moveSpaceFilterSelection(-1)
		return m, nil
	case "down":
		m.moveSpaceFilterSelection(1)
		return m, nil
	case "space":
		m.setSpaceFilter(m.spaceFilter + " ")
		return m, nil
	default:
		if len(msg.Runes) > 0 {
			m.setSpaceFilter(m.spaceFilter + string(msg.Runes))
		}
		return m, nil
	}
}

func (m *Model) setSpaceFilter(value string) {
	m.spaceFilter = value
	m.selected[FeatureChat] = 0
	m.clampSelections()
}

func (m *Model) moveSpaceFilterSelection(delta int) {
	length := len(m.visibleSpaces())
	if length == 0 {
		m.selected[FeatureChat] = 0
		return
	}
	m.selected[FeatureChat] = clamp(m.selected[FeatureChat]+delta, length)
}

func (m *Model) restoreSpaceFilterOrigin() {
	if m.spaceFilterOrigin == "" {
		m.clampSelections()
		return
	}
	for index, space := range m.spaces {
		if space.Name == m.spaceFilterOrigin {
			m.selected[FeatureChat] = index
			return
		}
	}
	m.clampSelections()
}

func (m Model) loadSelectedChat() (Model, tea.Cmd) {
	if m.feature != FeatureChat {
		return m, nil
	}
	space := m.selectedSpace()
	if space.Name == "" {
		m.chatMessages = nil
		m.chatOlder = ""
		m.chatLoading = false
		m.chatLoadSpace = ""
		return m, nil
	}

	if m.applyCachedSelectedChat() {
		m.loading = false
		m.chatLoading = false
		m.chatLoadSpace = ""
		m.setSpaceUnread(space.Name, false)
		m.persistWorkspaceCache()
		cmds := []tea.Cmd{m.markChatReadCmd(space.Name)}
		cmds = append(cmds, m.precomputeFrameCmdsForChat(m.chatMessages)...)
		return m, tea.Batch(cmds...)
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
		return chatLoadedMsg{spaceName: spaceName, loadID: loadID, messages: page, err: err}
	}
}

func (m *Model) replacePending(pendingID string, msg api.ChatMessage, err error) {
	for i := range m.chatMessages {
		if m.chatMessages[i].ID == pendingID {
			if err != nil {
				m.chatMessages[i].Pending = false
				m.chatMessages[i].Text += "\n[send failed: " + err.Error() + "]"
				m.err = err.Error()
				return
			}
			m.chatMessages[i] = msg
			m.markSeenChatMessage(msg)
			// A realtime push for this same message may have raced the
			// API response and appended a copy under the real ID while
			// the pending placeholder was still here. Collapse those
			// copies — sameChatMessage keys on real ID now that the
			// placeholder has been swapped out.
			m.chatMessages = dedupeChatMessages(m.chatMessages)
			m.toast = "sent"
			return
		}
	}
}

func (m *Model) applyChatMessage(msg api.ChatMessage) {
	if msg.ID == "" && msg.Name != "" {
		msg.ID = lastSegmentOfName(msg.Name)
	}
	if msg.Space == "" && msg.Name != "" {
		msg.Space = spaceFromMessageName(msg.Name)
	}
	if msg.ID == "" {
		return
	}
	m.chatMessages, _ = upsertChatMessage(m.chatMessages, msg)
	m.markSeenChatMessage(msg)
}

func (m *Model) removeChatMessage(messageID, messageName string) {
	if messageID == "" && messageName != "" {
		messageID = lastSegmentOfName(messageName)
	}
	filtered := m.chatMessages[:0]
	for _, msg := range m.chatMessages {
		if msg.Name == messageName || (messageID != "" && msg.ID == messageID) {
			continue
		}
		filtered = append(filtered, msg)
	}
	m.chatMessages = filtered
}

func (m *Model) applyChatSpace(space api.Space) {
	if space.Name == "" {
		return
	}
	for i := range m.spaces {
		if m.spaces[i].Name == space.Name {
			m.spaces[i] = space
			m.selected[FeatureChat] = i
			return
		}
	}
	m.spaces = append([]api.Space{space}, m.spaces...)
	m.selected[FeatureChat] = 0
}

func (m Model) deleteSelectedChatMessage() (Model, tea.Cmd) {
	msg, ok := m.chatMessageUnderCursor()
	if !ok {
		m.toast = "move cursor onto a message to delete"
		return m, nil
	}
	name := msg.Name
	if name == "" && msg.Space != "" && msg.ID != "" {
		name = msg.Space + "/messages/" + msg.ID
	}
	if name == "" {
		m.toast = "message name unavailable"
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.DeleteChatMessage(m.ctx, name)
		return chatDeletedMsg{messageID: msg.ID, messageName: name, err: err}
	}
}

func (m Model) addSelectedChatReaction() (Model, tea.Cmd) {
	msg, ok := m.chatMessageUnderCursor()
	if !ok {
		m.toast = "move cursor onto a message to react"
		return m, nil
	}
	name := msg.Name
	if name == "" && msg.Space != "" && msg.ID != "" {
		name = msg.Space + "/messages/" + msg.ID
	}
	if name == "" {
		m.toast = "message name unavailable"
		return m, nil
	}
	emoji := defaultChatReaction
	return m, func() tea.Msg {
		reactionName, err := m.client.AddChatReaction(m.ctx, name, emoji)
		return chatReactionMsg{messageID: msg.ID, messageName: name, reactionName: reactionName, emoji: emoji, err: err}
	}
}

func (m Model) removeSelectedChatReaction() (Model, tea.Cmd) {
	msg, ok := m.chatMessageUnderCursor()
	if !ok {
		m.toast = "move cursor onto a message to remove reaction"
		return m, nil
	}
	name := msg.Name
	if name == "" && msg.Space != "" && msg.ID != "" {
		name = msg.Space + "/messages/" + msg.ID
	}
	reactionName := m.chatReactions[name]
	if reactionName == "" {
		m.toast = "no TUI reaction to remove"
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.DeleteChatReaction(m.ctx, reactionName)
		return chatReactionMsg{messageID: msg.ID, messageName: name, reactionName: reactionName, remove: true, err: err}
	}
}

func (m *Model) toggleChatSubscription() tea.Cmd {
	space := m.selectedSpace()
	for i := range m.spaces {
		if m.spaces[i].Name == space.Name {
			m.spaces[i].Live = !m.spaces[i].Live
			pinned := m.spaces[i].Live
			spaceName := m.spaces[i].Name
			if pinned {
				m.toast = "subscription on"
			} else {
				m.toast = "subscription off"
			}
			m.persistWorkspaceCache()
			var cmds []tea.Cmd
			if pinner, ok := m.client.(api.Pinner); ok && m.cfg.Daemon {
				cmds = append(cmds, func() tea.Msg {
					var err error
					if pinned {
						err = pinner.PinChatSpace(m.ctx, spaceName)
					} else {
						err = pinner.UnpinChatSpace(m.ctx, spaceName)
					}
					return pinActionMsg{space: spaceName, pinned: pinned, err: err}
				})
			}
			if pinned {
				if cmd := m.subscribeCmd(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			if len(cmds) == 0 {
				return nil
			}
			return tea.Batch(cmds...)
		}
	}
	return nil
}

func (m *Model) handleChatSectionLoaded(msg chatSectionLoadedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.featureLoading[FeatureChat] = false
	m.chatLoading = false
	m.chatLoadSpace = ""
	if msg.err != nil {
		m.err = msg.err.Error()
		return cmds
	}
	if msg.messagesErr != nil {
		m.err = msg.messagesErr.Error()
	}
	m.featureLoaded[FeatureChat] = true
	m.cacheLoaded = true
	m.spaces = msg.spaces.Items
	m.chatMessages = dedupeChatMessages(msg.messages.Items)
	m.chatOlder = msg.messages.NextPageToken
	m.markSeenChatMessages(m.chatMessages)
	m.clampSelections()
	m.persistWorkspaceCache()
	cmds = append(cmds, m.subscribeCmd())
	cmds = append(cmds, m.enrichSpacesCmds()...)
	cmds = append(cmds, m.enrichSendersCmds()...)
	cmds = append(cmds, m.imageDownloadCmdsForChat(m.chatMessages)...)
	cmds = append(cmds, m.precomputeFrameCmdsForChat(m.chatMessages)...)

	return cmds
}

func (m *Model) handleChatLoaded(msg chatLoadedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if !m.finishChatLoad(msg.spaceName, msg.loadID) {
		return cmds
	}
	selected := msg.spaceName == m.selectedSpace().Name
	if m.chatLoadSpace == msg.spaceName {
		m.loading = false
		m.chatLoading = false
		m.chatLoadSpace = ""
	}
	if msg.err != nil {
		if selected || msg.refresh {
			m.err = msg.err.Error()
		}
		return cmds
	}
	messages := dedupeChatMessages(msg.messages.Items)
	m.markSeenChatMessages(messages)
	if msg.refresh {
		m.toast = "chat refreshed"
	}
	m.rememberChatPage(msg.spaceName, api.Page[api.ChatMessage]{
		Items:         messages,
		NextPageToken: msg.messages.NextPageToken,
	})
	if selected {
		m.chatMessages = messages
		m.chatOlder = msg.messages.NextPageToken
	}
	if selected || msg.refresh {
		// Opening or explicitly refreshing a space means the user has seen it,
		// even if they navigated away before the background request finished.
		m.setSpaceUnread(msg.spaceName, false)
		cmds = append(cmds, m.markChatReadCmd(msg.spaceName))
	}
	m.persistWorkspaceCache()
	if selected {
		cmds = append(cmds, m.enrichSendersCmds()...)
		cmds = append(cmds, m.imageDownloadCmdsForChat(m.chatMessages)...)
		cmds = append(cmds, m.precomputeFrameCmdsForChat(m.chatMessages)...)
	} else {
		cmds = append(cmds, m.imageDownloadCmdsForChat(messages)...)
	}

	return cmds
}

func (m *Model) handleChatSent(msg chatSentMsg) []tea.Cmd {
	var cmds []tea.Cmd
	m.replacePending(msg.pendingID, msg.message, msg.err)
	m.persistWorkspaceCache()
	if msg.err == nil {
		cmds = append(cmds, m.imageDownloadCmdsForChat([]api.ChatMessage{msg.message})...)
	} else if len(msg.attachments) > 0 {
		// Put the staged uploads back so the user can retry without
		// re-pasting. Prepended to preserve order if they paste more.
		m.pendingChatAttachments = append(msg.attachments, m.pendingChatAttachments...)
	}

	return cmds
}

func (m *Model) handleChatEdited(msg chatEditedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		m.err = msg.err.Error()
	} else {
		if msg.message.ID == "" {
			msg.message.ID = msg.messageID
		}
		m.applyChatMessage(msg.message)
		m.toast = "message edited"
		m.persistWorkspaceCache()
	}

	return cmds
}

func (m *Model) handleChatDeleted(msg chatDeletedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		m.err = msg.err.Error()
	} else {
		m.removeChatMessage(msg.messageID, msg.messageName)
		m.toast = "message deleted"
		m.persistWorkspaceCache()
	}

	return cmds
}

func (m *Model) handleChatSpaceCreated(msg chatSpaceCreatedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		m.err = msg.err.Error()
	} else {
		m.applyChatSpace(msg.space)
		m.toast = "space created"
		m.persistWorkspaceCache()
	}

	return cmds
}

func (m *Model) handleChatReaction(msg chatReactionMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		m.err = msg.err.Error()
	} else if msg.remove {
		delete(m.chatReactions, msg.messageName)
		m.toast = "reaction removed"
	} else {
		m.chatReactions[msg.messageName] = msg.reactionName
		m.toast = "reaction added " + msg.emoji
	}

	return cmds
}

func (m *Model) handleRealtime(msg realtimeMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.err != nil {
		m.err = msg.err.Error()
		if m.realtimeRetry == 0 {
			m.realtimeRetry = time.Second
		} else {
			m.realtimeRetry = minDuration(30*time.Second, m.realtimeRetry*2)
		}
		delay := m.realtimeRetry
		cmds = append(cmds, tea.Tick(delay, func(time.Time) tea.Msg {
			return realtimeMsg{}
		}))
		return cmds
	}
	m.realtimeRetry = 0
	cmds = append(cmds, m.applyIncomingChatMessage(msg.message, true)...)
	cmds = append(cmds, m.subscribeCmd())

	return cmds
}

func (m *Model) handleDaemonEvent(msg daemonEventMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.events != nil {
		m.daemonEvents = msg.events
	}
	if msg.err != nil {
		m.daemonEvents = nil
		m.err = msg.err.Error()
		cmds = append(cmds, tea.Tick(3*time.Second, func(time.Time) tea.Msg {
			return daemonEventMsg{}
		}))
		return cmds
	}
	if msg.event.Topic != "" {
		m.recordMessage("event", "daemon: "+msg.event.Topic)
	}
	if msg.event.Topic == "image.cached" {
		var payload struct {
			Source string `json:"source"`
			Path   string `json:"path"`
		}
		if json.Unmarshal(msg.event.Payload, &payload) == nil && payload.Source != "" && payload.Path != "" {
			m.imageFiles[payload.Source] = payload.Path
			delete(m.imageErrors, payload.Source)
			delete(m.imageLoading, payload.Source)
			m.imageVersion++
			cmds = append(cmds, m.precomputeFrameCmdsForCurrentDetail()...)
		}
	}
	if msg.event.Topic == "notify" {
		var payload struct {
			Space string `json:"space"`
		}
		if json.Unmarshal(msg.event.Payload, &payload) == nil && payload.Space != "" {
			if m.isSelectedSpace(payload.Space) {
				m.setSpaceUnread(payload.Space, false)
				cmds = append(cmds, m.markChatReadCmd(payload.Space))
			} else {
				m.setSpaceUnread(payload.Space, true)
				m.toast = "new chat message"
			}
		}
	}
	if msg.event.Topic == "chat.message" {
		var message api.ChatMessage
		if json.Unmarshal(msg.event.Payload, &message) == nil {
			cmds = append(cmds, m.applyIncomingChatMessage(message, false)...)
		}
	}
	if msg.event.Topic == "chat.history.loaded" {
		var payload struct {
			Space string                    `json:"space"`
			Page  api.Page[api.ChatMessage] `json:"page"`
		}
		if json.Unmarshal(msg.event.Payload, &payload) == nil && payload.Space != "" {
			payload.Page.Items = dedupeChatMessages(payload.Page.Items)
			m.rememberChatPage(payload.Space, payload.Page)
			if m.isSelectedSpace(payload.Space) {
				m.chatMessages = payload.Page.Items
				m.chatOlder = payload.Page.NextPageToken
				m.chatLoading = false
				m.chatLoadSpace = ""
				m.markSeenChatMessages(m.chatMessages)
				cmds = append(cmds, m.imageDownloadCmdsForChat(m.chatMessages)...)
				cmds = append(cmds, m.precomputeFrameCmdsForChat(m.chatMessages)...)
			}
		}
	}
	if msg.event.Topic == "chat.read" {
		var payload struct {
			Space string `json:"space"`
		}
		if json.Unmarshal(msg.event.Payload, &payload) == nil && payload.Space != "" {
			m.setSpaceUnread(payload.Space, false)
		}
	}
	cmds = append(cmds, m.daemonEventCmd())

	return cmds
}

func (m *Model) handleSelfResolved(msg selfResolvedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.apiAbsent {
		m.peopleAPIDown = true
		return cmds
	}
	if msg.err != nil || msg.userID == "" {
		return cmds
	}
	m.peopleAPIDown = false
	m.markSelfUser(msg.userID)
	m.markSelfUser(msg.email)
	if msg.email != "" {
		m.selfEmail = msg.email
	}
	if msg.label != "" {
		m.userLabels[normalizeUserKey(msg.userID)] = msg.label
	}
	m.persistWorkspaceCache()
	cmds = append(cmds, m.enrichSpacesCmds()...)
	cmds = append(cmds, m.enrichSendersCmds()...)

	return cmds
}

func (m *Model) handleUserResolved(msg userResolvedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	delete(m.pendingUsers, normalizeUserKey(msg.userID))
	if msg.apiAbsent {
		m.peopleAPIDown = true
		return cmds
	}
	if msg.err != nil {
		return cmds
	}
	m.peopleAPIDown = false
	if msg.label != "" {
		m.userLabels[normalizeUserKey(msg.userID)] = msg.label
		m.persistWorkspaceCache()
	}

	return cmds
}

func (m *Model) handleMembersLoaded(msg membersLoadedMsg) []tea.Cmd {
	var cmds []tea.Cmd
	delete(m.pendingMembers, msg.spaceName)
	if msg.err != nil {
		return cmds
	}
	m.membersBySpace[msg.spaceName] = msg.members
	m.selfUserIDs = api.InferSelfUserIDs(m.spaces, m.membersBySpace, m.selfUserIDs)
	m.persistWorkspaceCache()
	for _, member := range msg.members {
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

	return cmds
}
