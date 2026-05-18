package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiantomaoludyo/gws-tui/internal/api"
	"github.com/fabhiantomaoludyo/gws-tui/internal/tui/notify"
	"github.com/fabhiantomaoludyo/gws-tui/internal/tui/theme"
)

type Feature string

const (
	FeatureChat     Feature = "chat"
	FeatureMail     Feature = "mail"
	FeatureCalendar Feature = "calendar"
	FeatureMeet     Feature = "meet"
)

var featureOrder = []Feature{FeatureChat, FeatureMail, FeatureCalendar, FeatureMeet}

type Options struct {
	Client       api.WorkspaceClient
	Config       Config
	ForceAuth    bool
	Version      string
	Commit       string
	BuildDate    string
	UpstreamHint string
}

type Model struct {
	client api.WorkspaceClient
	cfg    Config
	theme  theme.Theme

	ctx    context.Context
	cancel context.CancelFunc

	width  int
	height int

	feature      Feature
	authRequired bool
	auth         api.AuthStatus
	upstreamHint string
	version      string
	commit       string
	buildDate    string

	spinner spinner.Model
	detail  viewport.Model
	input   textarea.Model

	actionFocus bool
	loading     bool
	err         string
	toast       string
	search      string

	spaces        []api.Space
	chatMessages  []api.ChatMessage
	chatOlder     string
	chatLoading   bool
	chatLoadID    int
	chatLoadSpace string
	seenMessages  map[string]bool

	mailLabels  []api.MailLabel
	mailThreads []api.MailThread
	mailNext    string

	events       []api.CalendarEvent
	calendarNext string
	weekOffset   int

	meetSpaces []api.MeetSpace

	userLabels     map[string]string
	pendingUsers   map[string]bool
	membersBySpace map[string][]api.SpaceMember
	pendingMembers map[string]bool
	selfUserIDs    map[string]bool
	peopleAPIDown  bool

	selected  map[Feature]int
	persisted persistedState
	modal     *composeModal
}

type loadedMsg struct {
	auth         api.AuthStatus
	spaces       api.Page[api.Space]
	messages     api.Page[api.ChatMessage]
	labels       []api.MailLabel
	threads      api.Page[api.MailThread]
	events       api.Page[api.CalendarEvent]
	meet         api.Page[api.MeetSpace]
	err          error
	authRequired bool
}

type featureLoadedMsg struct {
	feature Feature
	items   any
	next    string
	err     error
}

type chatLoadedMsg struct {
	spaceName string
	loadID    int
	messages  api.Page[api.ChatMessage]
	err       error
}

type chatSentMsg struct {
	pendingID string
	message   api.ChatMessage
	err       error
}

type mailActionMsg struct {
	thread api.MailThread
	err    error
	label  string
}

type eventActionMsg struct {
	event api.CalendarEvent
	err   error
	label string
}

type meetActionMsg struct {
	space api.MeetSpace
	err   error
	label string
}

type realtimeMsg struct {
	message api.ChatMessage
	err     error
}

type autosaveMsg struct{}

type userResolvedMsg struct {
	userID    string
	label     string
	err       error
	apiAbsent bool
}

type membersLoadedMsg struct {
	spaceName string
	members   []api.SpaceMember
	err       error
}

type selfResolvedMsg struct {
	userID    string
	label     string
	err       error
	apiAbsent bool
}

func New(opts Options) Model {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := opts.Config
	cfg.InitialFeature = normalizeFeature(cfg.InitialFeature)
	persisted := loadPersistedState(cfg.StatePath)
	feature := Feature(cfg.InitialFeature)
	if persisted.LastFeature != "" && cfg.InitialFeature == "chat" {
		feature = Feature(normalizeFeature(persisted.LastFeature))
	}

	spin := spinner.New()
	spin.Spinner = spinner.Dot

	input := textarea.New()
	input.Placeholder = "message"
	input.SetHeight(3)
	input.ShowLineNumbers = false
	input.Blur()

	detail := viewport.New(80, 20)

	return Model{
		client:         opts.Client,
		cfg:            cfg,
		theme:          theme.New(cfg.NoColor),
		ctx:            ctx,
		cancel:         cancel,
		feature:        feature,
		authRequired:   opts.ForceAuth,
		upstreamHint:   opts.UpstreamHint,
		version:        opts.Version,
		commit:         opts.Commit,
		buildDate:      opts.BuildDate,
		spinner:        spin,
		input:          input,
		detail:         detail,
		loading:        true,
		seenMessages:   map[string]bool{},
		userLabels:     map[string]string{},
		pendingUsers:   map[string]bool{},
		membersBySpace: map[string][]api.SpaceMember{},
		pendingMembers: map[string]bool{},
		selfUserIDs:    map[string]bool{},
		selected: map[Feature]int{
			FeatureChat:     persisted.Selections[string(FeatureChat)],
			FeatureMail:     persisted.Selections[string(FeatureMail)],
			FeatureCalendar: persisted.Selections[string(FeatureCalendar)],
			FeatureMeet:     persisted.Selections[string(FeatureMeet)],
		},
		persisted: persisted,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadAllCmd(), m.autosaveTick(), m.whoamiCmd())
}

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
		return selfResolvedMsg{userID: person.UserID, label: label}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	case loadedMsg:
		m.loading = false
		m.chatLoading = false
		m.chatLoadSpace = ""
		if msg.err != nil {
			m.err = msg.err.Error()
		}
		m.auth = msg.auth
		m.authRequired = m.authRequired || msg.authRequired
		m.spaces = msg.spaces.Items
		m.chatMessages = msg.messages.Items
		m.chatOlder = msg.messages.NextPageToken
		for _, chat := range m.chatMessages {
			m.seenMessages[chat.ID] = true
		}
		m.mailLabels = msg.labels
		m.mailThreads = msg.threads.Items
		m.mailNext = msg.threads.NextPageToken
		m.events = msg.events.Items
		m.calendarNext = msg.events.NextPageToken
		m.meetSpaces = msg.meet.Items
		m.clampSelections()
		cmds = append(cmds, m.subscribeCmd())
		cmds = append(cmds, m.enrichSpacesCmds()...)
		cmds = append(cmds, m.enrichSendersCmds()...)
	case chatLoadedMsg:
		if msg.loadID != m.chatLoadID || msg.spaceName != m.selectedSpace().Name {
			break
		}
		m.loading = false
		m.chatLoading = false
		m.chatLoadSpace = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			break
		}
		m.chatMessages = msg.messages.Items
		m.chatOlder = msg.messages.NextPageToken
		for _, chat := range m.chatMessages {
			m.seenMessages[chat.ID] = true
		}
		cmds = append(cmds, m.enrichSendersCmds()...)
	case featureLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			break
		}
		switch msg.feature {
		case FeatureChat:
			if messages, ok := msg.items.([]api.ChatMessage); ok {
				m.chatMessages = append(messages, m.chatMessages...)
				m.chatOlder = msg.next
				for _, chat := range m.chatMessages {
					m.seenMessages[chat.ID] = true
				}
				m.toast = "older messages loaded"
				cmds = append(cmds, m.enrichSendersCmds()...)
			}
		case FeatureMail:
			if threads, ok := msg.items.([]api.MailThread); ok {
				m.mailThreads = append(m.mailThreads, threads...)
				m.mailNext = msg.next
				m.toast = "more mail loaded"
			}
		case FeatureCalendar:
			if events, ok := msg.items.([]api.CalendarEvent); ok {
				m.events = append(m.events, events...)
				m.calendarNext = msg.next
				m.toast = "more events loaded"
			}
		}
	case chatSentMsg:
		m.replacePending(msg.pendingID, msg.message, msg.err)
	case mailActionMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.toast = msg.label
			m.applyMailThread(msg.thread)
		}
	case eventActionMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.toast = msg.label
			m.applyEvent(msg.event)
		}
	case meetActionMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.toast = msg.label
			if msg.space.Name != "" {
				m.applyMeetSpace(msg.space)
			}
		}
	case realtimeMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			cmds = append(cmds, tea.Tick(3*time.Second, func(time.Time) tea.Msg {
				return realtimeMsg{}
			}))
			break
		}
		if msg.message.ID != "" && !m.seenMessages[msg.message.ID] {
			m.seenMessages[msg.message.ID] = true
			m.chatMessages = append(m.chatMessages, msg.message)
			m.toast = "new chat message"
			if cmd := m.resolveUserCmd(api.UserIDFromName(msg.message.SenderID)); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if m.feature != FeatureChat || !m.isSelectedSpace(msg.message.Space) {
				notify.Send("gws chat", msg.message.SenderName+": "+msg.message.Text, notify.Options{
					Desktop:   m.cfg.NotifyDesktop,
					Sound:     m.cfg.NotifySound,
					SoundFile: m.cfg.NotifySoundFile,
				})
			}
		}
		cmds = append(cmds, m.subscribeCmd())
	case selfResolvedMsg:
		if msg.apiAbsent {
			m.peopleAPIDown = true
			break
		}
		if msg.err != nil || msg.userID == "" {
			break
		}
		m.selfUserIDs[msg.userID] = true
		if msg.label != "" {
			m.userLabels[msg.userID] = msg.label
		}
	case userResolvedMsg:
		delete(m.pendingUsers, msg.userID)
		if msg.apiAbsent {
			m.peopleAPIDown = true
			break
		}
		if msg.err != nil {
			break
		}
		if msg.label != "" {
			m.userLabels[msg.userID] = msg.label
		}
	case membersLoadedMsg:
		delete(m.pendingMembers, msg.spaceName)
		if msg.err != nil {
			break
		}
		m.membersBySpace[msg.spaceName] = msg.members
		for _, member := range msg.members {
			if member.Type != "" && member.Type != "HUMAN" {
				continue
			}
			if cmd := m.resolveUserCmd(member.UserID); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case autosaveMsg:
		if m.modal != nil && m.modal.autosave {
			if err := m.saveDraft(); err == nil {
				m.modal.savedAt = time.Now()
			}
		}
		cmds = append(cmds, m.autosaveTick())
	case tea.KeyMsg:
		if m.modal != nil {
			next, cmd := m.updateModal(msg)
			m = next
			cmds = append(cmds, cmd)
			break
		}
		next, cmd := m.updateKey(msg)
		m = next
		cmds = append(cmds, cmd)
	}

	m.updateDetailContent()
	return m, tea.Batch(cmds...)
}

func (m Model) updateKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.actionFocus {
		switch msg.String() {
		case "esc":
			m.actionFocus = false
			m.input.Blur()
			return m, nil
		case "enter":
			return m.submitAction()
		case "shift+enter":
			m.input.InsertString("\n")
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.persist()
		m.cancel()
		return m, tea.Quit
	case "tab":
		m.feature = m.nextFeature(1)
		m.toast = string(m.feature)
	case "shift+tab":
		m.feature = m.nextFeature(-1)
		m.toast = string(m.feature)
	case "1":
		m.feature = FeatureChat
	case "2":
		m.feature = FeatureMail
	case "3":
		m.feature = FeatureCalendar
	case "4":
		m.feature = FeatureMeet
	case "j", "down":
		return m.moveSelection(1)
	case "k", "up":
		return m.moveSelection(-1)
	case "g":
		m.selected[m.feature] = 0
		return m.loadSelectedChat()
	case "G":
		m.selected[m.feature] = m.listLen() - 1
		return m.loadSelectedChat()
	case "enter", "o":
		m.toast = m.openHint()
	case "i":
		m.actionFocus = true
		m.input.Focus()
	case "r":
		m.loading = true
		return m, m.loadAllCmd()
	case "ctrl+r":
		cfg, err := LoadConfig()
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.cfg = cfg
		m.theme = theme.New(cfg.NoColor)
		m.toast = "config reloaded"
	case "/":
		m.openSearchModal()
	case "m":
		return m.loadMore()
	case "s":
		if m.feature == FeatureChat {
			m.toggleChatSubscription()
		} else if m.feature == FeatureMail {
			return m.toggleSelectedStar()
		}
	case "c":
		if m.feature == FeatureMail {
			m.openMailCompose(nil, false)
		} else if m.feature == FeatureCalendar {
			m.openEventCompose(nil)
		}
	case "R":
		if m.feature == FeatureMail {
			thread := m.selectedMail()
			m.openMailCompose(&thread, true)
		} else if m.feature == FeatureChat {
			m.actionFocus = true
			m.input.Focus()
			m.input.SetValue("↪ ")
		}
	case "f":
		if m.feature == FeatureMail {
			thread := m.selectedMail()
			m.openMailCompose(&thread, false)
		}
	case "e":
		if m.feature == FeatureMail {
			return m.archiveSelectedMail()
		}
	case "#":
		if m.feature == FeatureMail {
			return m.trashSelectedMail()
		}
	case "y":
		if m.feature == FeatureCalendar {
			return m.rsvpSelected("accepted")
		}
	case "n":
		if m.feature == FeatureCalendar {
			return m.rsvpSelected("declined")
		}
		if m.feature == FeatureMeet {
			m.actionFocus = true
			m.input.Focus()
			m.input.SetValue("")
		}
	case "M":
		if m.feature == FeatureCalendar {
			return m.rsvpSelected("tentative")
		}
	case "d":
		if m.feature == FeatureCalendar {
			return m.deleteSelectedEvent()
		}
	case "t":
		if m.feature == FeatureCalendar {
			m.weekOffset = 0
			m.toast = "today"
		}
	case "]":
		if m.feature == FeatureCalendar {
			m.weekOffset++
			m.toast = "next week"
		}
	case "[":
		if m.feature == FeatureCalendar {
			m.weekOffset--
			m.toast = "previous week"
		}
	case "J":
		if m.feature == FeatureMeet {
			return m.openMeetLink()
		}
	case "C":
		if m.feature == FeatureMeet {
			return m.copyMeetLink()
		}
	case "E":
		if m.feature == FeatureMeet {
			return m.endSelectedMeet()
		}
	case "x":
		m.err = ""
		m.toast = ""
	}
	return m, nil
}

func (m Model) submitAction() (Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	if value == "" {
		m.actionFocus = false
		m.input.Blur()
		return m, nil
	}
	switch m.feature {
	case FeatureChat:
		space := m.selectedSpace()
		if space.Name == "" {
			return m, nil
		}
		pendingID := fmt.Sprintf("pending-%d", time.Now().UnixNano())
		pending := api.ChatMessage{
			ID:         pendingID,
			Space:      space.Name,
			SenderID:   "users/me",
			SenderName: "You",
			Text:       value,
			CreateTime: time.Now(),
			Pending:    true,
		}
		m.chatMessages = append(m.chatMessages, pending)
		m.input.SetValue("")
		m.actionFocus = false
		m.input.Blur()
		return m, func() tea.Msg {
			msg, err := m.client.SendChatMessage(m.ctx, space.Name, value)
			return chatSentMsg{pendingID: pendingID, message: msg, err: err}
		}
	case FeatureCalendar:
		m.input.SetValue("")
		m.actionFocus = false
		m.input.Blur()
		return m, func() tea.Msg {
			event, err := m.client.QuickAddEvent(m.ctx, value)
			return eventActionMsg{event: event, err: err, label: "event created"}
		}
	case FeatureMeet:
		m.input.SetValue("")
		m.actionFocus = false
		m.input.Blur()
		return m, func() tea.Msg {
			space, err := m.client.CreateMeetSpace(m.ctx, value)
			return meetActionMsg{space: space, err: err, label: "meet space created"}
		}
	}
	return m, nil
}

func (m *Model) resolveUserCmd(userID string) tea.Cmd {
	if userID == "" || m.peopleAPIDown {
		return nil
	}
	if _, ok := m.userLabels[userID]; ok {
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
	if space.SpaceType != "DIRECT_MESSAGE" && space.SpaceType != "GROUP_CHAT" {
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

func (m Model) loadAllCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()

		auth, authErr := m.client.AuthStatus(ctx)
		spaces, spacesErr := m.client.ChatSpaces(ctx)
		selectedSpace := ""
		if len(spaces.Items) > 0 {
			selectedSpace = spaces.Items[clamp(m.selected[FeatureChat], len(spaces.Items))].Name
		}
		messages, messagesErr := m.client.ChatMessages(ctx, selectedSpace, "")
		labels, labelsErr := m.client.MailLabels(ctx)
		threads, threadsErr := m.client.MailThreads(ctx, api.MailQuery{Label: "Inbox"})
		events, eventsErr := m.client.CalendarEvents(ctx, api.CalendarQuery{})
		meet, meetErr := m.client.MeetSpaces(ctx)

		err := firstErr(authErr, spacesErr, messagesErr, labelsErr, threadsErr, eventsErr, meetErr)
		return loadedMsg{
			auth: auth, spaces: spaces, messages: messages, labels: labels, threads: threads, events: events, meet: meet,
			err: err, authRequired: !auth.Valid(),
		}
	}
}

func (m Model) loadMore() (Model, tea.Cmd) {
	switch m.feature {
	case FeatureChat:
		if m.chatOlder == "" {
			m.toast = "no older messages"
			return m, nil
		}
		space := m.selectedSpace()
		token := m.chatOlder
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.ChatMessages(m.ctx, space.Name, token)
			return featureLoadedMsg{feature: FeatureChat, items: page.Items, next: page.NextPageToken, err: err}
		}
	case FeatureMail:
		if m.mailNext == "" {
			m.toast = "no more mail"
			return m, nil
		}
		token := m.mailNext
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.MailThreads(m.ctx, api.MailQuery{Label: "Inbox", Search: m.search, PageToken: token})
			return featureLoadedMsg{feature: FeatureMail, items: page.Items, next: page.NextPageToken, err: err}
		}
	case FeatureCalendar:
		if m.calendarNext == "" {
			m.toast = "no more events"
			return m, nil
		}
		token := m.calendarNext
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.CalendarEvents(m.ctx, api.CalendarQuery{Search: m.search, PageToken: token})
			return featureLoadedMsg{feature: FeatureCalendar, items: page.Items, next: page.NextPageToken, err: err}
		}
	}
	return m, nil
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
				return realtimeMsg{}
			}
			return realtimeMsg{message: msg}
		}
	}
}

func (m Model) autosaveTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return autosaveMsg{} })
}

func (m *Model) resize() {
	w, h := m.width, m.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 32
	}
	leftHBorder := m.theme.Pane.GetHorizontalBorderSize()
	detailHBorder := m.theme.Active.GetHorizontalBorderSize()
	detailVBorder := m.theme.Active.GetVerticalBorderSize()
	actionVBorder := m.theme.Input.GetVerticalBorderSize()
	detailHPad := m.theme.Active.GetHorizontalPadding()
	actionHPad := m.theme.Input.GetHorizontalPadding()
	statusH := 1

	left := max(20, int(float64(w)*0.30)-leftHBorder)
	right := max(20, w-left-leftHBorder-detailHBorder)

	actionHeight := max(3, min(8, strings.Count(m.input.Value(), "\n")+3))
	detailHeight := max(5, h-statusH-detailVBorder-actionHeight-actionVBorder)

	m.detail.Width = max(10, right-detailHPad)
	m.detail.Height = max(3, detailHeight-1)
	m.input.SetWidth(max(10, right-actionHPad))
	m.input.SetHeight(max(2, actionHeight-1))
}

func (m *Model) clampSelections() {
	for _, feature := range featureOrder {
		m.selected[feature] = clamp(m.selected[feature], m.listLenFor(feature))
	}
}

func (m Model) nextFeature(delta int) Feature {
	idx := 0
	for i, feature := range featureOrder {
		if feature == m.feature {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(featureOrder)) % len(featureOrder)
	return featureOrder[idx]
}

func (m Model) moveSelection(delta int) (Model, tea.Cmd) {
	length := m.listLen()
	if length == 0 {
		m.selected[m.feature] = 0
		return m, nil
	}
	next := clamp(m.selected[m.feature]+delta, length)
	if next == m.selected[m.feature] {
		return m, nil
	}
	m.selected[m.feature] = next
	return m.loadSelectedChat()
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

	m.chatLoadID++
	loadID := m.chatLoadID
	spaceName := space.Name
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

func (m Model) listLen() int {
	return m.listLenFor(m.feature)
}

func (m Model) listLenFor(feature Feature) int {
	switch feature {
	case FeatureChat:
		return len(m.spaces)
	case FeatureMail:
		return len(m.mailThreads)
	case FeatureCalendar:
		return len(m.events)
	case FeatureMeet:
		return len(m.meetSpaces)
	default:
		return 0
	}
}

func (m Model) selectedSpace() api.Space {
	if len(m.spaces) == 0 {
		return api.Space{}
	}
	return m.spaces[clamp(m.selected[FeatureChat], len(m.spaces))]
}

func (m Model) isSelectedSpace(space string) bool {
	return m.selectedSpace().Name == space
}

func (m Model) selectedMail() api.MailThread {
	if len(m.mailThreads) == 0 {
		return api.MailThread{}
	}
	return m.mailThreads[clamp(m.selected[FeatureMail], len(m.mailThreads))]
}

func (m Model) selectedEvent() api.CalendarEvent {
	if len(m.events) == 0 {
		return api.CalendarEvent{}
	}
	return m.events[clamp(m.selected[FeatureCalendar], len(m.events))]
}

func (m Model) selectedMeet() api.MeetSpace {
	if len(m.meetSpaces) == 0 {
		return api.MeetSpace{}
	}
	return m.meetSpaces[clamp(m.selected[FeatureMeet], len(m.meetSpaces))]
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
			m.seenMessages[msg.ID] = true
			m.toast = "sent"
			return
		}
	}
}

func (m *Model) applyMailThread(thread api.MailThread) {
	if thread.ID == "" {
		return
	}
	for i := range m.mailThreads {
		if m.mailThreads[i].ID == thread.ID {
			m.mailThreads[i] = thread
			return
		}
	}
	m.mailThreads = append([]api.MailThread{thread}, m.mailThreads...)
}

func (m *Model) applyEvent(event api.CalendarEvent) {
	if event.ID == "" {
		return
	}
	for i := range m.events {
		if m.events[i].ID == event.ID {
			m.events[i] = event
			return
		}
	}
	m.events = append(m.events, event)
}

func (m *Model) applyMeetSpace(space api.MeetSpace) {
	for i := range m.meetSpaces {
		if m.meetSpaces[i].Name == space.Name {
			m.meetSpaces[i] = space
			return
		}
	}
	m.meetSpaces = append([]api.MeetSpace{space}, m.meetSpaces...)
}

func (m Model) toggleSelectedStar() (Model, tea.Cmd) {
	thread := m.selectedMail()
	if thread.ID == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		updated, err := m.client.ToggleStar(m.ctx, thread.ID)
		return mailActionMsg{thread: updated, err: err, label: "star toggled"}
	}
}

func (m Model) archiveSelectedMail() (Model, tea.Cmd) {
	thread := m.selectedMail()
	if thread.ID == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.ArchiveMail(m.ctx, thread.ID)
		return mailActionMsg{thread: thread, err: err, label: "archived"}
	}
}

func (m Model) trashSelectedMail() (Model, tea.Cmd) {
	thread := m.selectedMail()
	if thread.ID == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.TrashMail(m.ctx, thread.ID)
		return mailActionMsg{thread: thread, err: err, label: "moved to trash"}
	}
}

func (m Model) rsvpSelected(response string) (Model, tea.Cmd) {
	event := m.selectedEvent()
	if event.ID == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		updated, err := m.client.RSVPEvent(m.ctx, event.ID, response)
		return eventActionMsg{event: updated, err: err, label: "RSVP " + response}
	}
}

func (m Model) deleteSelectedEvent() (Model, tea.Cmd) {
	event := m.selectedEvent()
	if event.ID == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.DeleteEvent(m.ctx, event.ID)
		return eventActionMsg{event: event, err: err, label: "event deleted"}
	}
}

func (m Model) openMeetLink() (Model, tea.Cmd) {
	space := m.selectedMeet()
	if space.MeetingURI == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		err := openURL(space.MeetingURI)
		return meetActionMsg{space: space, err: err, label: "opening browser"}
	}
}

func (m Model) copyMeetLink() (Model, tea.Cmd) {
	space := m.selectedMeet()
	if space.MeetingURI == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		err := copyText(space.MeetingURI)
		return meetActionMsg{space: space, err: err, label: "link copied"}
	}
}

func (m Model) endSelectedMeet() (Model, tea.Cmd) {
	space := m.selectedMeet()
	if space.Name == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.EndMeetSpace(m.ctx, space.Name)
		return meetActionMsg{space: space, err: err, label: "conference ended"}
	}
}

func (m *Model) toggleChatSubscription() {
	space := m.selectedSpace()
	for i := range m.spaces {
		if m.spaces[i].Name == space.Name {
			m.spaces[i].Live = !m.spaces[i].Live
			if m.spaces[i].Live {
				m.toast = "subscription on"
			} else {
				m.toast = "subscription off"
			}
			return
		}
	}
}

func (m Model) openHint() string {
	switch m.feature {
	case FeatureChat:
		return "space opened"
	case FeatureMail:
		return "thread opened"
	case FeatureCalendar:
		return "event opened"
	case FeatureMeet:
		return "meet details opened"
	default:
		return ""
	}
}

func (m *Model) persist() {
	state := persistedState{
		LastFeature: string(m.feature),
		LastSpace:   m.selectedSpace().Name,
		Selections:  map[string]int{},
	}
	for feature, index := range m.selected {
		state.Selections[string(feature)] = index
	}
	_ = savePersistedState(m.cfg.StatePath, state)
}

func (m *Model) updateDetailContent() {
	if m.width == 0 {
		return
	}
	m.detail.SetContent(m.detailContent())
}

func normalizeFeature(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "mail", "gmail":
		return string(FeatureMail)
	case "calendar", "cal":
		return string(FeatureCalendar)
	case "meet":
		return string(FeatureMeet)
	default:
		return string(FeatureChat)
	}
}

func clamp(value, length int) int {
	if length <= 0 {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value >= length {
		return length - 1
	}
	return value
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func sortedEvents(events []api.CalendarEvent) []api.CalendarEvent {
	out := append([]api.CalendarEvent(nil), events...)
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

func (m Model) saveDraft() error {
	if m.modal == nil {
		return nil
	}
	if err := os.MkdirAll(m.cfg.DraftDir, 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(m.modal.snapshot(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.cfg.DraftDir, m.modal.id+".json"), append(payload, '\n'), 0o600)
}
