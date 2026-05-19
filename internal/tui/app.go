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
	"unicode"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fabhiansan/gws-tui/internal/api"
	"github.com/fabhiansan/gws-tui/internal/tui/notify"
	"github.com/fabhiansan/gws-tui/internal/tui/theme"
)

type Feature string

const (
	FeatureChat     Feature = "chat"
	FeatureMail     Feature = "mail"
	FeatureCalendar Feature = "calendar"
	FeatureMeet     Feature = "meet"
)

var featureOrder = []Feature{FeatureChat, FeatureMail, FeatureCalendar, FeatureMeet}

type pane int

const (
	paneList pane = iota
	paneDetail
	paneAction
)

type Options struct {
	Client          api.WorkspaceClient
	Config          Config
	InitialSnapshot *api.WorkspaceSnapshot
	ForceAuth       bool
	Version         string
	Commit          string
	BuildDate       string
	UpstreamHint    string
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

	focusedPane       pane
	loading           bool
	err               string
	toast             string
	search            string
	spaceFilter       string
	spaceFilterActive bool
	spaceFilterOrigin string

	spaces        []api.Space
	chatMessages  []api.ChatMessage
	chatOlder     string
	chatLoading   bool
	chatLoadID    int
	chatLoadSpace string
	seenMessages  map[string]bool
	realtimeRetry time.Duration

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

	senderColorIdx   map[string]int
	senderColorNext  int
	senderColorSpace string

	selected        map[Feature]int
	persisted       persistedState
	cache           workspaceCache
	cacheLoaded     bool
	imageFiles      map[string]string
	imageLoading    map[string]bool
	imageErrors     map[string]string
	imageRenders    map[string]inlineImageRender
	imageFramePend  map[string]bool
	imageVersion    int
	imagePlacement  int
	modal           *composeModal
	helpVisible     bool
	daemonEvents    <-chan api.DaemonEvent
	vimComposer     vimMode
	vimPending      string
	vimRegister     string
	vimRegisterLine bool

	detailCursor     int
	detailCol        int
	detailAnchor     int
	detailAnchorCol  int
	detailVisual     bool
	detailVisualLine bool
	detailPending    string
	detailLines      []string
	detailLineCount  int
	detailKey        string
	detailRenderKey  string
	detailImageAt    map[int]api.Attachment
	detailMessageAt  map[int]string
	replyThreadID    string
	replyTargetName  string

	// pendingChatAttachments holds files staged for the next chat send.
	// Populated by Ctrl+V pasting an image; drained (and the temp files
	// deleted) after a successful send.
	pendingChatAttachments []pendingAttachment

	imageViewer *imageViewerState
}

// pendingAttachment is a file the TUI created locally (e.g. a clipboard paste)
// and will hand off to the workspace client on the next send. Path points to a
// temp file we own — submitAction removes it after the upload completes.
type pendingAttachment struct {
	path        string
	contentType string
	name        string
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

type featureRefreshedMsg struct {
	feature Feature
	labels  []api.MailLabel
	threads api.Page[api.MailThread]
	events  api.Page[api.CalendarEvent]
	meet    api.Page[api.MeetSpace]
	err     error
}

type chatLoadedMsg struct {
	spaceName string
	loadID    int
	messages  api.Page[api.ChatMessage]
	err       error
	refresh   bool
}

type chatSentMsg struct {
	pendingID string
	message   api.ChatMessage
	err       error
	// attachments carries the just-drained pending uploads so the handler
	// can restore them when the send fails. On success the closure deletes
	// the temp files and leaves this empty.
	attachments []pendingAttachment
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

type pinActionMsg struct {
	space  string
	pinned bool
	err    error
}

type autosaveMsg struct{}

type imageCachedMsg struct {
	source string
	path   string
	err    error
}

type daemonEventMsg struct {
	events <-chan api.DaemonEvent
	event  api.DaemonEvent
	err    error
}

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
	cache := newWorkspaceCache()
	cacheLoaded := false
	if opts.InitialSnapshot != nil {
		cache = *opts.InitialSnapshot
		cache.EnsureMaps()
		cacheLoaded = cache.HasData()
	} else if !cfg.Daemon {
		cache, cacheLoaded = loadWorkspaceCache(cfg.CachePath)
	}
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

	model := Model{
		client:         opts.Client,
		cfg:            cfg,
		theme:          theme.New(cfg.Theme, cfg.NoColor),
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
		focusedPane:    paneList,
		loading:        !cacheLoaded,
		seenMessages:   map[string]bool{},
		userLabels:     map[string]string{},
		pendingUsers:   map[string]bool{},
		membersBySpace: map[string][]api.SpaceMember{},
		pendingMembers: map[string]bool{},
		selfUserIDs:    map[string]bool{},
		senderColorIdx: map[string]int{},
		selected: map[Feature]int{
			FeatureChat:     persisted.Selections[string(FeatureChat)],
			FeatureMail:     persisted.Selections[string(FeatureMail)],
			FeatureCalendar: persisted.Selections[string(FeatureCalendar)],
			FeatureMeet:     persisted.Selections[string(FeatureMeet)],
		},
		persisted:       persisted,
		cache:           cache,
		cacheLoaded:     cacheLoaded,
		imageFiles:      map[string]string{},
		imageLoading:    map[string]bool{},
		imageErrors:     map[string]string{},
		imageRenders:    map[string]inlineImageRender{},
		imageFramePend:  map[string]bool{},
		detailImageAt:   map[int]api.Attachment{},
		detailMessageAt: map[int]string{},
	}
	if cacheLoaded {
		model.hydrateWorkspaceCache(cache)
	}
	return model
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, m.autosaveTick()}
	cmds = append(cmds, m.imageDownloadCmdsForWorkspace()...)
	if !m.cacheLoaded {
		cmds = append(cmds, m.loadAllCmd(), m.whoamiCmd())
	} else if m.cfg.Daemon {
		cmds = append(cmds, m.subscribeCmd())
	}
	if m.cfg.Daemon {
		cmds = append(cmds, m.daemonEventCmd())
	}
	return tea.Batch(cmds...)
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
		cmds = append(cmds, m.precomputeFrameCmdsForCurrentDetail()...)
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
		m.chatMessages = dedupeChatMessages(msg.messages.Items)
		m.chatOlder = msg.messages.NextPageToken
		m.markSeenChatMessages(m.chatMessages)
		m.mailLabels = msg.labels
		m.mailThreads = msg.threads.Items
		m.mailNext = msg.threads.NextPageToken
		m.events = msg.events.Items
		m.calendarNext = msg.events.NextPageToken
		m.meetSpaces = msg.meet.Items
		m.clampSelections()
		m.persistWorkspaceCache()
		cmds = append(cmds, m.subscribeCmd())
		cmds = append(cmds, m.enrichSpacesCmds()...)
		cmds = append(cmds, m.enrichSendersCmds()...)
		cmds = append(cmds, m.imageDownloadCmdsForWorkspace()...)
		cmds = append(cmds, m.precomputeFrameCmdsForCurrentDetail()...)
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
		m.chatMessages = dedupeChatMessages(msg.messages.Items)
		m.chatOlder = msg.messages.NextPageToken
		m.markSeenChatMessages(m.chatMessages)
		if msg.refresh {
			m.toast = "chat refreshed"
		}
		m.rememberChatPage(msg.spaceName, api.Page[api.ChatMessage]{
			Items:         m.chatMessages,
			NextPageToken: msg.messages.NextPageToken,
		})
		// Opening a space implicitly marks it read.
		for i := range m.spaces {
			if m.spaces[i].Name == msg.spaceName {
				m.spaces[i].Unread = false
				break
			}
		}
		cmds = append(cmds, m.markChatReadCmd(msg.spaceName))
		m.persistWorkspaceCache()
		cmds = append(cmds, m.enrichSendersCmds()...)
		cmds = append(cmds, m.imageDownloadCmdsForChat(m.chatMessages)...)
		cmds = append(cmds, m.precomputeFrameCmdsForChat(m.chatMessages)...)
	case featureLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			break
		}
		switch msg.feature {
		case FeatureChat:
			if messages, ok := msg.items.([]api.ChatMessage); ok {
				m.chatMessages = dedupeChatMessages(append(messages, m.chatMessages...))
				m.chatOlder = msg.next
				m.markSeenChatMessages(m.chatMessages)
				m.toast = "older messages loaded"
				m.persistWorkspaceCache()
				cmds = append(cmds, m.enrichSendersCmds()...)
				cmds = append(cmds, m.imageDownloadCmdsForChat(messages)...)
			}
		case FeatureMail:
			if threads, ok := msg.items.([]api.MailThread); ok {
				m.mailThreads = append(m.mailThreads, threads...)
				m.mailNext = msg.next
				m.toast = "more mail loaded"
				m.persistWorkspaceCache()
				cmds = append(cmds, m.imageDownloadCmdsForMail(threads)...)
			}
		case FeatureCalendar:
			if events, ok := msg.items.([]api.CalendarEvent); ok {
				m.events = append(m.events, events...)
				m.calendarNext = msg.next
				m.toast = "more events loaded"
				m.persistWorkspaceCache()
			}
		}
	case featureRefreshedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			break
		}
		switch msg.feature {
		case FeatureMail:
			m.mailLabels = msg.labels
			m.mailThreads = msg.threads.Items
			m.mailNext = msg.threads.NextPageToken
			m.toast = "mail refreshed"
			m.clampSelections()
			m.persistWorkspaceCache()
			cmds = append(cmds, m.imageDownloadCmdsForMail(m.mailThreads)...)
		case FeatureCalendar:
			m.events = msg.events.Items
			m.calendarNext = msg.events.NextPageToken
			m.toast = "calendar refreshed"
			m.clampSelections()
			m.persistWorkspaceCache()
		case FeatureMeet:
			m.meetSpaces = msg.meet.Items
			m.toast = "meet refreshed"
			m.clampSelections()
			m.persistWorkspaceCache()
		}
	case chatSentMsg:
		m.replacePending(msg.pendingID, msg.message, msg.err)
		m.persistWorkspaceCache()
		if msg.err == nil {
			cmds = append(cmds, m.imageDownloadCmdsForChat([]api.ChatMessage{msg.message})...)
		} else if len(msg.attachments) > 0 {
			// Put the staged uploads back so the user can retry without
			// re-pasting. Prepended to preserve order if they paste more.
			m.pendingChatAttachments = append(msg.attachments, m.pendingChatAttachments...)
		}
	case mailActionMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.toast = msg.label
			m.applyMailThread(msg.thread)
			m.persistWorkspaceCache()
			cmds = append(cmds, m.imageDownloadCmdsForMail([]api.MailThread{msg.thread})...)
		}
	case eventActionMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.toast = msg.label
			m.applyEvent(msg.event)
			m.persistWorkspaceCache()
		}
	case meetActionMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.toast = msg.label
			if msg.space.Name != "" {
				m.applyMeetSpace(msg.space)
				m.persistWorkspaceCache()
			}
		}
	case pinActionMsg:
		if msg.err != nil {
			// Roll back the optimistic local flip so the indicator
			// matches what the daemon actually has.
			for i := range m.spaces {
				if m.spaces[i].Name == msg.space {
					m.spaces[i].Live = !msg.pinned
					break
				}
			}
			m.err = msg.err.Error()
		}
	case realtimeMsg:
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
			break
		}
		m.realtimeRetry = 0
		cmds = append(cmds, m.applyIncomingChatMessage(msg.message, true)...)
		cmds = append(cmds, m.subscribeCmd())
	case imageCachedMsg:
		delete(m.imageLoading, msg.source)
		if msg.err == nil && msg.source != "" && msg.path != "" {
			m.imageFiles[msg.source] = msg.path
			delete(m.imageErrors, msg.source)
			m.imageVersion++
			cmds = append(cmds, m.precomputeFrameCmdsForCurrentDetail()...)
		} else if msg.err != nil && msg.source != "" {
			m.imageErrors[msg.source] = msg.err.Error()
			m.imageVersion++
		}
	case imageFrameReadyMsg:
		delete(m.imageFramePend, msg.key)
		if msg.err == nil && msg.key != "" {
			m.imageRenders[msg.key] = inlineImageRender{
				file:        msg.file,
				source:      msg.source,
				columns:     msg.columns,
				rows:        msg.rows,
				size:        msg.size,
				modTime:     msg.modTime,
				full:        msg.full,
				placeholder: msg.placeholder,
			}
			m.imagePlacement++
			m.imageVersion++
		}
	case daemonEventMsg:
		if msg.events != nil {
			m.daemonEvents = msg.events
		}
		if msg.err != nil {
			m.daemonEvents = nil
			m.err = msg.err.Error()
			cmds = append(cmds, tea.Tick(3*time.Second, func(time.Time) tea.Msg {
				return daemonEventMsg{}
			}))
			break
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
		if msg.event.Topic == "chat.read" {
			var payload struct {
				Space string `json:"space"`
			}
			if json.Unmarshal(msg.event.Payload, &payload) == nil && payload.Space != "" {
				m.setSpaceUnread(payload.Space, false)
			}
		}
		cmds = append(cmds, m.daemonEventCmd())
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
		m.persistWorkspaceCache()
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
			m.persistWorkspaceCache()
		}
	case membersLoadedMsg:
		delete(m.pendingMembers, msg.spaceName)
		if msg.err != nil {
			break
		}
		m.membersBySpace[msg.spaceName] = msg.members
		m.persistWorkspaceCache()
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
	case imageViewerOpenErrMsg:
		m.toast = "open: " + msg.err
	case tea.MouseMsg:
		next, cmd := m.updateMouse(msg)
		m = next
		cmds = append(cmds, cmd)
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

	cmds = append(cmds, m.imageDownloadCmdsForCurrentDetail()...)
	m.updateDetailContent()
	return m, tea.Batch(cmds...)
}

func (m Model) updateKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.imageViewer != nil {
		return m.updateImageViewer(msg)
	}
	if m.helpVisible {
		switch msg.String() {
		case "?", "esc", "q":
			m.helpVisible = false
		}
		return m, nil
	}
	if m.spaceFilterActive && m.feature == FeatureChat && m.focusedPane == paneList {
		return m.updateSpaceFilter(msg)
	}
	if m.feature == FeatureChat && msg.String() == "ctrl+v" {
		return m.handleChatPaste()
	}
	if m.feature == FeatureChat && msg.String() == "ctrl+x" && len(m.pendingChatAttachments) > 0 {
		return m.clearPendingChatAttachments(), nil
	}
	if m.focusedPane == paneAction {
		if m.cfg.VimMode {
			key := msg.String()
			if m.vimComposer == vimModeNormal && key == "esc" {
				m.focusedPane = paneList
				m.input.Blur()
				m.vimPending = ""
				m.clearReplyContext()
				return m, nil
			}
			if m.vimComposer == vimModeNormal && key == "enter" {
				return m.submitAction()
			}
			if m.vimComposer == vimModeNormal && key == "/" {
				m.openSearchModal()
				return m, nil
			}
			if m.vimComposer == vimModeNormal && m.vimPending == "" {
				switch key {
				case "1":
					m.focusedPane = paneList
					m.input.Blur()
					return m, nil
				case "2":
					m.focusedPane = paneDetail
					m.input.Blur()
					return m, nil
				case "3":
					return m, nil
				}
			}
			if m.vimComposerKey(msg) {
				return m, nil
			}
		} else if msg.String() == "esc" {
			m.focusedPane = paneList
			m.input.Blur()
			m.clearReplyContext()
			return m, nil
		}
		switch msg.String() {
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

	if m.focusedPane == paneDetail && m.cfg.VimMode {
		if next, cmd, handled := m.updateDetailVim(msg); handled {
			return next, cmd
		}
	}

	switch msg.String() {
	case "ctrl+c", "q":
		m.persist()
		m.cancel()
		return m, tea.Quit
	case "?":
		m.helpVisible = true
		return m, nil
	case "H", "ctrl+h":
		m.focusedPane = paneList
		return m, nil
	case "L", "ctrl+l":
		m.focusedPane = paneDetail
		return m, nil
	case "esc":
		if m.focusedPane != paneList {
			m.focusedPane = paneList
			return m, nil
		}
	case "tab":
		m.feature = m.nextFeature(1)
		m.toast = string(m.feature)
	case "shift+tab":
		m.feature = m.nextFeature(-1)
		m.toast = string(m.feature)
	case "ctrl+1":
		m.feature = FeatureChat
	case "ctrl+2":
		m.feature = FeatureMail
	case "ctrl+3":
		m.feature = FeatureCalendar
	case "ctrl+4":
		m.feature = FeatureMeet
	case "1":
		m.focusedPane = paneList
		return m, nil
	case "2":
		m.focusedPane = paneDetail
		return m, nil
	case "3":
		m.focusedPane = paneAction
		m.input.Focus()
		m.vimComposer = vimModeInsert
		return m, nil
	case "j", "down":
		if m.focusedPane == paneDetail {
			m.detail.LineDown(1)
			return m, nil
		}
		return m.moveSelection(1)
	case "k", "up":
		if m.focusedPane == paneDetail {
			m.detail.LineUp(1)
			return m, nil
		}
		return m.moveSelection(-1)
	case "ctrl+d":
		if m.focusedPane == paneDetail {
			m.detail.HalfViewDown()
			return m, nil
		}
	case "ctrl+u":
		if m.focusedPane == paneDetail {
			m.detail.HalfViewUp()
			return m, nil
		}
	case "ctrl+f", "pgdown":
		if m.focusedPane == paneDetail {
			m.detail.ViewDown()
			return m, nil
		}
	case "ctrl+b", "pgup":
		if m.focusedPane == paneDetail {
			m.detail.ViewUp()
			return m, nil
		}
	case "g":
		if m.focusedPane == paneDetail {
			m.detail.GotoTop()
			return m, nil
		}
		m.selected[m.feature] = 0
		return m.loadSelectedChat()
	case "G":
		if m.focusedPane == paneDetail {
			m.detail.GotoBottom()
			return m, nil
		}
		m.selected[m.feature] = m.listLen() - 1
		return m.loadSelectedChat()
	case "enter", "o":
		if m.focusedPane == paneDetail {
			if att, ok := m.detailImageAt[m.detailCursor]; ok {
				m.openImageViewer(att)
				return m, nil
			}
		}
		m.toast = m.openHint()
	case "i":
		m.focusedPane = paneAction
		m.input.Focus()
		m.vimComposer = vimModeInsert
	case "a":
		m.focusedPane = paneAction
		m.input.Focus()
		m.vimComposer = vimModeInsert
		m.input.CursorEnd()
	case "y":
		if m.feature == FeatureCalendar {
			return m.rsvpSelected("accepted")
		}
		cmd := m.yankFocused()
		return m, cmd
	case "p":
		return m.pasteIntoComposer()
	case "r":
		if m.feature == FeatureChat {
			if msg, ok := m.chatMessageUnderCursor(); ok {
				m.beginThreadReply(msg)
				return m, nil
			}
		}
		if len(m.imageErrors) > 0 {
			m.imageErrors = map[string]string{}
			m.imageVersion++
		}
		return m.refreshCurrentFeature()
	case "ctrl+r":
		cfg, err := LoadConfig()
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.cfg = cfg
		m.theme = theme.New(cfg.Theme, cfg.NoColor)
		m.toast = "config reloaded"
	case "/":
		if m.feature == FeatureChat && m.focusedPane == paneList {
			m.openSpaceFilter()
			return m, nil
		}
		m.openSearchModal()
	case "m":
		return m.loadMore()
	case "s":
		if m.feature == FeatureChat {
			return m, m.toggleChatSubscription()
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
			m.loading = true
			if len(m.imageErrors) > 0 {
				m.imageErrors = map[string]string{}
				m.imageVersion++
			}
			return m, m.loadAllCmd()
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
	case "n":
		if m.feature == FeatureCalendar {
			return m.rsvpSelected("declined")
		}
		if m.feature == FeatureMeet {
			return m.createMeetSpaceNow()
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

func (m Model) submitAction() (Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	// Chat allows sending image-only messages (paste + Enter with no text),
	// so the empty-input early return only applies when there's also nothing
	// pending to upload.
	if value == "" && !(m.feature == FeatureChat && len(m.pendingChatAttachments) > 0) {
		m.focusedPane = paneList
		m.input.Blur()
		m.clearReplyContext()
		return m, nil
	}
	switch m.feature {
	case FeatureChat:
		space := m.selectedSpace()
		if space.Name == "" {
			return m, nil
		}
		pendingID := fmt.Sprintf("pending-%d", time.Now().UnixNano())
		threadID := m.replyThreadID
		attachments := m.pendingChatAttachments
		m.pendingChatAttachments = nil
		uploads := make([]api.LocalAttachment, 0, len(attachments))
		for _, att := range attachments {
			uploads = append(uploads, api.LocalAttachment{
				Path:        att.path,
				ContentType: att.contentType,
				Name:        att.name,
			})
		}
		pending := api.ChatMessage{
			ID:         pendingID,
			Space:      space.Name,
			SenderID:   "users/me",
			SenderName: "You",
			Text:       value,
			CreateTime: time.Now(),
			Pending:    true,
			ThreadID:   threadID,
		}
		if threadID != "" {
			pending.ParentID = lastSegmentOfName(threadID)
		}
		m.chatMessages, _ = upsertChatMessage(m.chatMessages, pending)
		m.markSeenChatMessage(pending)
		m.input.SetValue("")
		m.focusedPane = paneList
		m.input.Blur()
		m.clearReplyContext()
		return m, func() tea.Msg {
			msg, err := m.client.SendChatMessage(m.ctx, space.Name, value, threadID, uploads)
			if err == nil {
				// Keep the temp files: the returned ChatMessage now points
				// to them via LocalPath so the inline renderer can show the
				// just-sent image without re-downloading from upstream.
				// They get cleaned up by the OS tmp sweep eventually.
				return chatSentMsg{pendingID: pendingID, message: msg}
			}
			// On failure, hand the staged pending attachments back to the
			// handler so the user can retry without re-pasting.
			return chatSentMsg{pendingID: pendingID, message: msg, err: err, attachments: attachments}
		}
	case FeatureCalendar:
		m.input.SetValue("")
		m.focusedPane = paneList
		m.input.Blur()
		return m, func() tea.Msg {
			event, err := m.client.QuickAddEvent(m.ctx, value)
			return eventActionMsg{event: event, err: err, label: "event created"}
		}
	case FeatureMeet:
		m.input.SetValue("")
		m.focusedPane = paneList
		m.input.Blur()
		return m, func() tea.Msg {
			space, err := m.client.CreateMeetSpace(m.ctx, value)
			return meetActionMsg{space: space, err: err, label: "meet space created"}
		}
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

func (m *Model) clearReplyContext() {
	m.replyThreadID = ""
	m.replyTargetName = ""
	m.input.Placeholder = "message"
}

func lastSegmentOfName(value string) string {
	if value == "" {
		return ""
	}
	idx := strings.LastIndex(value, "/")
	if idx < 0 {
		return value
	}
	return value[idx+1:]
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

func (m Model) refreshCurrentFeature() (Model, tea.Cmd) {
	switch m.feature {
	case FeatureChat:
		return m.refreshSelectedChat()
	case FeatureMail:
		m.loading = true
		search := m.search
		label := "Inbox"
		if search != "" {
			label = "All Mail"
		}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			labels, labelsErr := m.client.MailLabels(ctx)
			threads, threadsErr := m.client.MailThreads(ctx, api.MailQuery{Label: label, Search: search})
			return featureRefreshedMsg{
				feature: FeatureMail,
				labels:  labels,
				threads: threads,
				err:     firstErr(labelsErr, threadsErr),
			}
		}
	case FeatureCalendar:
		m.loading = true
		search := m.search
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			events, err := m.client.CalendarEvents(ctx, api.CalendarQuery{Search: search})
			return featureRefreshedMsg{feature: FeatureCalendar, events: events, err: err}
		}
	case FeatureMeet:
		m.loading = true
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			meet, err := m.client.MeetSpaces(ctx)
			return featureRefreshedMsg{feature: FeatureMeet, meet: meet, err: err}
		}
	default:
		return m, nil
	}
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
		return chatLoadedMsg{spaceName: spaceName, loadID: loadID, messages: page, err: err, refresh: true}
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
				return realtimeMsg{err: api.ErrRemoteClosed}
			}
			return realtimeMsg{message: msg}
		}
	}
}

func (m *Model) applyIncomingChatMessage(message api.ChatMessage, sendStandaloneNotify bool) []tea.Cmd {
	var cmds []tea.Cmd
	if chatMessageKey(message) == "" || m.hasSeenChatMessage(message) {
		return cmds
	}
	m.markSeenChatMessage(message)
	m.rememberChatMessage(message)
	// selfUserIDs is keyed by the bare numeric id; message.SenderID has the
	// "users/" resource prefix. Normalize before lookup.
	senderBareID := api.UserIDFromName(message.SenderID)
	fromSelf := senderBareID != "" && m.selfUserIDs[senderBareID]
	if m.isSelectedSpace(message.Space) {
		m.chatMessages, _ = upsertChatMessage(m.chatMessages, message)
		// User is actively viewing; keep the daemon's read marker in sync so the
		// badge doesn't reappear on reconnect.
		cmds = append(cmds, m.markChatReadCmd(message.Space))
	} else if !fromSelf {
		m.setSpaceUnread(message.Space, true)
	}
	m.toast = "new chat message"
	if cmd := m.resolveUserCmd(senderBareID); cmd != nil {
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, m.imageDownloadCmdsForChat([]api.ChatMessage{message})...)
	if sendStandaloneNotify && !m.cfg.Daemon && (m.feature != FeatureChat || !m.isSelectedSpace(message.Space)) {
		notify.Send("gws chat", message.SenderName+": "+message.Text, notify.Options{
			Desktop:   m.cfg.NotifyDesktop,
			Sound:     m.cfg.NotifySound,
			SoundFile: m.cfg.NotifySoundFile,
		})
	}
	m.persistWorkspaceCache()
	return cmds
}

func (m Model) markChatReadCmd(spaceName string) tea.Cmd {
	if spaceName == "" || !m.cfg.Daemon {
		return nil
	}
	reader, ok := m.client.(api.ChatReader)
	if !ok {
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg {
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
	topics := []string{"image.cached", "notify", "chat.message", "chat.read", "auth.changed", "mail.changed", "calendar.changed", "meet.changed"}
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

	detailWidth := max(10, right-detailHPad)
	if m.cfg.VimMode {
		detailWidth = max(10, detailWidth-2)
	}
	m.detail.Width = detailWidth
	m.detail.Height = max(3, detailHeight)
	m.input.SetWidth(max(10, right-actionHPad))
	m.input.SetHeight(max(2, actionHeight))
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
		return m, tea.Batch(m.precomputeFrameCmdsForChat(m.chatMessages)...)
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
		return len(m.visibleSpaces())
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

func (m Model) visibleSpaces() []api.Space {
	if strings.TrimSpace(m.spaceFilter) == "" {
		return m.spaces
	}
	query := strings.TrimSpace(m.spaceFilter)
	matches := make([]spaceFilterMatch, 0, len(m.spaces))
	for index, space := range m.spaces {
		if score, ok := fuzzySpaceScore(m.spaceSearchText(space), query); ok {
			matches = append(matches, spaceFilterMatch{
				space: space,
				score: score,
				index: index,
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		return matches[i].index < matches[j].index
	})
	filtered := make([]api.Space, 0, len(matches))
	for _, match := range matches {
		filtered = append(filtered, match.space)
	}
	return filtered
}

type spaceFilterMatch struct {
	space api.Space
	score int
	index int
}

func (m Model) spaceSearchText(space api.Space) string {
	return strings.Join([]string{
		m.spaceLabel(space),
		space.DisplayName,
		space.FormattedName,
		space.Name,
		lastSegment(space.Name),
	}, " ")
}

func fuzzySpaceScore(candidate, query string) (int, bool) {
	candidate = strings.ToLower(candidate)
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return 0, true
	}
	total := 0
	for _, term := range terms {
		score, ok := fuzzyTermScore(candidate, term)
		if !ok {
			return 0, false
		}
		total += score
	}
	return total, true
}

func fuzzyTermScore(candidate, term string) (int, bool) {
	if term == "" {
		return 0, true
	}
	if idx := strings.Index(candidate, term); idx >= 0 {
		return idx, true
	}
	query := []rune(term)
	haystack := []rune(candidate)
	queryIndex := 0
	first := -1
	last := -1
	gaps := 0
	boundaryBonus := 0
	for index, char := range haystack {
		if char != query[queryIndex] {
			continue
		}
		if first == -1 {
			first = index
		}
		if last >= 0 {
			gaps += index - last - 1
		}
		if index == 0 || isFuzzyBoundary(haystack[index-1]) {
			boundaryBonus++
		}
		last = index
		queryIndex++
		if queryIndex == len(query) {
			return 100 + first + gaps*2 - boundaryBonus*3, true
		}
	}
	return 0, false
}

func isFuzzyBoundary(char rune) bool {
	return unicode.IsSpace(char) || char == '-' || char == '_' || char == '/' || char == '#' || char == '.' || char == ','
}

func (m Model) visibleChatMessages() []api.ChatMessage {
	if strings.TrimSpace(m.search) == "" {
		return m.chatMessages
	}
	needle := strings.ToLower(strings.TrimSpace(m.search))
	filtered := make([]api.ChatMessage, 0, len(m.chatMessages))
	for _, msg := range m.chatMessages {
		if strings.Contains(strings.ToLower(msg.Text), needle) {
			filtered = append(filtered, msg)
			continue
		}
		if strings.Contains(strings.ToLower(m.senderLabel(msg)), needle) {
			filtered = append(filtered, msg)
		}
	}
	return filtered
}

func (m Model) selectedSpace() api.Space {
	visible := m.visibleSpaces()
	if len(visible) == 0 {
		return api.Space{}
	}
	return visible[clamp(m.selected[FeatureChat], len(visible))]
}

func chatMessageKey(msg api.ChatMessage) string {
	if msg.Space != "" && msg.ID != "" {
		return msg.Space + "\x00" + msg.ID
	}
	if msg.Name != "" {
		return msg.Name
	}
	return ""
}

func sameChatMessage(a, b api.ChatMessage) bool {
	if key := chatMessageKey(a); key != "" && key == chatMessageKey(b) {
		return true
	}
	return a.Space != "" &&
		a.Space == b.Space &&
		a.SenderID == b.SenderID &&
		a.Text == b.Text &&
		!a.CreateTime.IsZero() &&
		a.CreateTime.Equal(b.CreateTime)
}

func upsertChatMessage(items []api.ChatMessage, msg api.ChatMessage) ([]api.ChatMessage, bool) {
	for i := range items {
		if sameChatMessage(items[i], msg) {
			items[i] = msg
			return items, false
		}
	}
	return append(items, msg), true
}

func dedupeChatMessages(items []api.ChatMessage) []api.ChatMessage {
	if len(items) < 2 {
		return items
	}
	out := make([]api.ChatMessage, 0, len(items))
	for _, msg := range items {
		out, _ = upsertChatMessage(out, msg)
	}
	return out
}

func (m *Model) markSeenChatMessage(msg api.ChatMessage) {
	if msg.ID != "" {
		m.seenMessages[msg.ID] = true
	}
	if msg.Name != "" {
		m.seenMessages[msg.Name] = true
	}
	if key := chatMessageKey(msg); key != "" {
		m.seenMessages[key] = true
	}
}

func (m *Model) markSeenChatMessages(items []api.ChatMessage) {
	for _, msg := range items {
		m.markSeenChatMessage(msg)
	}
}

func (m Model) hasSeenChatMessage(msg api.ChatMessage) bool {
	if msg.ID != "" && m.seenMessages[msg.ID] {
		return true
	}
	if msg.Name != "" && m.seenMessages[msg.Name] {
		return true
	}
	if key := chatMessageKey(msg); key != "" && m.seenMessages[key] {
		return true
	}
	for _, existing := range m.chatMessages {
		if sameChatMessage(existing, msg) {
			return true
		}
	}
	return false
}

func (m Model) isSelectedSpace(space string) bool {
	return m.selectedSpace().Name == space
}

func (m *Model) setSpaceUnread(spaceName string, unread bool) {
	for i := range m.spaces {
		if m.spaces[i].Name == spaceName {
			m.spaces[i].Unread = unread
			return
		}
	}
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

func (m Model) createMeetSpaceNow() (Model, tea.Cmd) {
	m.toast = "creating meet space..."
	return m, func() tea.Msg {
		space, err := m.client.CreateMeetSpace(m.ctx, "")
		return meetActionMsg{space: space, err: err, label: "meet space created"}
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

func (m *Model) yankFocused() tea.Cmd {
	var text string
	switch m.feature {
	case FeatureChat:
		if len(m.chatMessages) > 0 {
			text = m.chatMessages[len(m.chatMessages)-1].Text
		}
	case FeatureMail:
		thread := m.selectedMail()
		text = thread.Subject
		if thread.Body != "" {
			text = thread.Subject + "\n\n" + thread.Body
		}
	case FeatureCalendar:
		event := m.selectedEvent()
		text = event.Summary
	case FeatureMeet:
		text = m.selectedMeet().MeetingURI
	}
	if text == "" {
		m.toast = "nothing to yank"
		return nil
	}
	m.vimRegister = text
	m.vimRegisterLine = false
	if err := copyText(text); err != nil {
		m.toast = "yank: " + err.Error()
	} else {
		m.toast = "yanked to clipboard"
	}
	return nil
}

func (m Model) pasteIntoComposer() (Model, tea.Cmd) {
	text, err := pasteText()
	if err != nil || text == "" {
		m.toast = "clipboard empty"
		return m, nil
	}
	m.focusedPane = paneAction
	m.input.Focus()
	m.vimComposer = vimModeInsert
	if current := m.input.Value(); current != "" {
		m.input.SetValue(current + text)
	} else {
		m.input.SetValue(text)
	}
	m.input.CursorEnd()
	return m, nil
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
	wasAtBottom := m.detail.AtBottom()
	prevLast := m.detailLineCount - 1
	wasAtLastLine := prevLast >= 0 && m.detailCursor >= prevLast

	selectionKey := m.detailKeyForSelection()
	keyChanged := selectionKey != m.detailKey
	if keyChanged {
		m.detailResetCursor()
		m.detailKey = selectionKey
		wasAtLastLine = false
	}

	renderKey := m.detailRenderFingerprint()
	if renderKey == m.detailRenderKey {
		return
	}

	// Chat history reads bottom-up (oldest → newest), so whenever a chat is
	// first opened or the user switches to a different space the latest
	// messages should already be in view without manual scrolling.
	pinToBottom := m.feature == FeatureChat && keyChanged

	if m.detailImageAt == nil {
		m.detailImageAt = map[int]api.Attachment{}
	} else {
		for k := range m.detailImageAt {
			delete(m.detailImageAt, k)
		}
	}
	if m.detailMessageAt == nil {
		m.detailMessageAt = map[int]string{}
	} else {
		for k := range m.detailMessageAt {
			delete(m.detailMessageAt, k)
		}
	}
	decorated, plain := m.decorateDetail(m.detailContent())
	m.detailLines = plain
	m.detailLineCount = len(plain)
	m.detailClampCursor()

	m.detail.SetContent(decorated)
	m.detailRenderKey = renderKey
	if wasAtBottom || pinToBottom {
		m.detail.GotoBottom()
	}
	if m.detailVimEnabled() {
		if (wasAtLastLine || pinToBottom) && m.detailLineCount > 0 {
			m.detailCursor = m.detailLineCount - 1
		}
		m.detailEnsureCursorVisible()
	}
}

func (m Model) detailRenderFingerprint() string {
	var b strings.Builder
	fmt.Fprintf(&b, "feature=%s|selection=%s|width=%d|height=%d|focus=%d|vim=%t|color=%t|icons=%t|inline=%t|image=%d|placement=%d",
		m.feature,
		m.detailKeyForSelection(),
		m.detail.Width,
		m.detail.Height,
		m.focusedPane,
		m.cfg.VimMode,
		!m.cfg.NoColor,
		!m.cfg.NoIcons,
		m.cfg.InlineImages,
		m.imageVersion,
		m.imagePlacement,
	)
	if m.detailVimEnabled() {
		fmt.Fprintf(&b, "|cursor=%d:%d|anchor=%d:%d|visual=%t|visualLine=%t",
			m.detailCursor,
			m.detailCol,
			m.detailAnchor,
			m.detailAnchorCol,
			m.detailVisual,
			m.detailVisualLine,
		)
	}

	switch m.feature {
	case FeatureChat:
		space := m.selectedSpace()
		fmt.Fprintf(&b, "|chatLoading=%t|chatLoadSpace=%s|spaceFilter=%t,%s|space=%s|messages=%d",
			m.chatLoading,
			m.chatLoadSpace,
			m.spaceFilterActive,
			m.spaceFilter,
			space.Name,
			len(m.chatMessages),
		)
		for _, msg := range m.chatMessages {
			label := m.senderLabel(msg)
			fmt.Fprintf(&b, "|msg=%s,%s,%s,%t,%t,%d,%s,%s",
				msg.ID,
				msg.ParentID,
				msg.SenderID,
				m.isSelfMessage(msg, label),
				msg.Pending,
				msg.CreateTime.UnixNano(),
				label,
				msg.Text,
			)
			writeAttachmentFingerprint(&b, msg.Attachments)
		}
	case FeatureMail:
		thread := m.selectedMail()
		fmt.Fprintf(&b, "|mail=%s,%s,%s,%s,%d,%t,%t,%d",
			thread.ID,
			thread.Sender,
			thread.Subject,
			thread.Body,
			thread.Date.UnixNano(),
			thread.Unread,
			thread.Starred,
			thread.QuotedLines,
		)
		writeAttachmentFingerprint(&b, thread.Attachments)
	case FeatureCalendar:
		event := m.selectedEvent()
		fmt.Fprintf(&b, "|event=%s,%s,%s,%d,%d,%s,%s,%s,%s",
			event.ID,
			event.Summary,
			event.Description,
			event.Start.UnixNano(),
			event.End.UnixNano(),
			event.Location,
			event.HangoutLink,
			event.RSVP,
			strings.Join(event.Attendees, ","),
		)
	case FeatureMeet:
		space := m.selectedMeet()
		activeConference := ""
		if space.ActiveConference != nil {
			activeConference = space.ActiveConference.ConferenceRecord
		}
		fmt.Fprintf(&b, "|meet=%s,%s,%s,%d,%s,%d,%t,%t,%s",
			space.Name,
			space.MeetingURI,
			space.MeetingCode,
			space.Created.UnixNano(),
			space.AccessType(),
			space.ActiveParticipants,
			space.Recording,
			space.Active,
			activeConference,
		)
	}
	return b.String()
}

func writeAttachmentFingerprint(b *strings.Builder, attachments []api.Attachment) {
	normalized := api.NormalizeAttachments(attachments)
	fmt.Fprintf(b, "|attachments=%d", len(normalized))
	for _, attachment := range normalized {
		fmt.Fprintf(b, ",%s,%s,%s,%s,%s,%s,%s",
			attachment.ID,
			attachment.MediaResourceName(),
			attachment.PreviewSource(),
			attachment.DisplayName(),
			attachment.ContentType,
			attachment.DownloadURL,
			attachment.ThumbnailURL,
		)
	}
}

func (m Model) detailKeyForSelection() string {
	switch m.feature {
	case FeatureChat:
		return "chat:" + m.selectedSpace().Name
	case FeatureMail:
		return "mail:" + m.selectedMail().ID
	case FeatureCalendar:
		return "cal:" + m.selectedEvent().ID
	case FeatureMeet:
		return "meet:" + m.selectedMeet().Name
	default:
		return string(m.feature)
	}
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

func minDuration(a, b time.Duration) time.Duration {
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
	if m.cfg.Daemon {
		saver, ok := m.client.(interface {
			DraftSave(context.Context, string, map[string]any) error
		})
		if !ok {
			return nil
		}
		ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
		defer cancel()
		return saver.DraftSave(ctx, m.modal.id, m.modal.snapshot())
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
