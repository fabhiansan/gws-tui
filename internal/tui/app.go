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
	FeatureTasks    Feature = "tasks"
	FeatureDrive    Feature = "drive"
	FeatureDocs     Feature = "docs"
)

var featureOrder = []Feature{FeatureChat, FeatureMail, FeatureCalendar, FeatureMeet, FeatureTasks, FeatureDrive, FeatureDocs}

const defaultChatReaction = "\U0001F44D"

var workspaceInitialLoadStages = []string{
	"Auth",
	"Chat spaces",
	"Current chat",
	"Mail labels",
	"Inbox",
	"Calendars",
	"Calendar events",
	"Meet",
	"Task lists",
	"Tasks",
	"Drive files",
	"Docs",
	"Document preview",
}

type pane int

const (
	paneList pane = iota
	paneDetail
	paneAction
	// paneMailSidebar is the Gmail-style folder rail. It only exists in the
	// Mail feature, where it sits left of the inbox list.
	paneMailSidebar
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
	loadProgress      chan loadProgressMsg
	loadStep          int
	loadTotal         int
	loadStage         string
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
	chatLoadIDs   map[string]int
	seenMessages  map[string]bool
	realtimeRetry time.Duration

	mailLabels  []api.MailLabel
	mailThreads []api.MailThread
	mailNext    string
	// mailFolder is the display name of the Gmail folder currently loaded
	// into mailThreads; mailFolderCursor is the highlighted row while the
	// folder sidebar is focused.
	mailFolder       string
	mailFolderCursor int

	events        []api.CalendarEvent
	calendarNext  string
	calendars     []api.CalendarListItem
	calendarIndex int
	weekOffset    int

	meetSpaces []api.MeetSpace

	taskLists     []api.TaskList
	tasks         []api.TaskItem
	taskNext      string
	taskListIndex int

	driveFiles []api.DriveFile
	driveNext  string

	docFiles     []api.DriveFile
	docNext      string
	doc          api.DocDocument
	docLoadingID string

	userLabels     map[string]string
	pendingUsers   map[string]bool
	membersBySpace map[string][]api.SpaceMember
	pendingMembers map[string]bool
	selfUserIDs    map[string]bool
	selfEmail      string
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

	detailCursor       int
	detailCol          int
	detailAnchor       int
	detailAnchorCol    int
	detailVisual       bool
	detailVisualLine   bool
	detailPending      string
	detailLines        []string
	detailLineCount    int
	detailKey          string
	detailRenderKey    string
	detailImageAt      map[int]api.Attachment
	detailAttachmentAt map[int]api.Attachment
	detailMessageAt    map[int]string
	replyThreadID      string
	replyTargetName    string
	editMessageName    string
	editMessageID      string
	createSpaceMode    bool
	chatReactions      map[string]string

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
	calendars    api.Page[api.CalendarListItem]
	calendarID   string
	meet         api.Page[api.MeetSpace]
	taskLists    api.Page[api.TaskList]
	tasks        api.Page[api.TaskItem]
	taskListID   string
	driveFiles   api.Page[api.DriveFile]
	docFiles     api.Page[api.DriveFile]
	doc          api.DocDocument
	err          error
	authRequired bool
}

type loadProgressMsg struct {
	Step  int
	Total int
	Label string
}

type loadProgressDoneMsg struct{}

type featureLoadedMsg struct {
	feature Feature
	items   any
	next    string
	err     error
}

type featureRefreshedMsg struct {
	feature    Feature
	labels     []api.MailLabel
	threads    api.Page[api.MailThread]
	events     api.Page[api.CalendarEvent]
	calendars  api.Page[api.CalendarListItem]
	calendarID string
	meet       api.Page[api.MeetSpace]
	taskLists  api.Page[api.TaskList]
	tasks      api.Page[api.TaskItem]
	taskListID string
	driveFiles api.Page[api.DriveFile]
	docFiles   api.Page[api.DriveFile]
	doc        api.DocDocument
	err        error
}

type docLoadedMsg struct {
	documentID string
	doc        api.DocDocument
	err        error
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

type chatEditedMsg struct {
	messageID string
	message   api.ChatMessage
	err       error
}

type chatDeletedMsg struct {
	messageID   string
	messageName string
	err         error
}

type chatSpaceCreatedMsg struct {
	space api.Space
	err   error
}

type chatReactionMsg struct {
	messageID    string
	messageName  string
	reactionName string
	emoji        string
	remove       bool
	err          error
}

type mailActionMsg struct {
	thread api.MailThread
	err    error
	label  string
}

type mailDraftActionMsg struct {
	draft api.MailDraftItem
	err   error
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

type taskActionMsg struct {
	task       api.TaskItem
	taskListID string
	taskID     string
	deleted    bool
	err        error
	label      string
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

type attachmentDownloadedMsg struct {
	name string
	path string
	err  error
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
	email     string
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
	var loadProgress chan loadProgressMsg
	loadTotal := 0
	loadStage := ""
	if !cacheLoaded {
		loadProgress = make(chan loadProgressMsg, len(workspaceInitialLoadStages)+2)
		loadTotal = len(workspaceInitialLoadStages)
		loadStage = "Starting workspace fetch"
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
		mailFolder:     defaultMailFolder,
		loading:        !cacheLoaded,
		loadProgress:   loadProgress,
		loadTotal:      loadTotal,
		loadStage:      loadStage,
		chatLoadIDs:    map[string]int{},
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
			FeatureTasks:    persisted.Selections[string(FeatureTasks)],
			FeatureDrive:    persisted.Selections[string(FeatureDrive)],
			FeatureDocs:     persisted.Selections[string(FeatureDocs)],
		},
		persisted:          persisted,
		cache:              cache,
		cacheLoaded:        cacheLoaded,
		imageFiles:         map[string]string{},
		imageLoading:       map[string]bool{},
		imageErrors:        map[string]string{},
		imageRenders:       map[string]inlineImageRender{},
		imageFramePend:     map[string]bool{},
		detailImageAt:      map[int]api.Attachment{},
		detailAttachmentAt: map[int]api.Attachment{},
		detailMessageAt:    map[int]string{},
		chatReactions:      map[string]string{},
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
		cmds = append(cmds, m.loadAllWithProgressCmd(m.loadProgress), m.whoamiCmd())
		if m.loadProgress != nil {
			cmds = append(cmds, m.loadProgressCmd())
		}
	} else {
		cmds = append(cmds, m.whoamiCmd())
		cmds = append(cmds, m.enrichSpacesCmds()...)
		cmds = append(cmds, m.enrichSendersCmds()...)
		if m.cfg.Daemon {
			cmds = append(cmds, m.subscribeCmd())
		}
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
		return selfResolvedMsg{userID: person.UserID, label: label, email: person.Email}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	prevFeature := m.feature
	prevFocus := m.focusedPane
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.resizeModalFields()
		cmds = append(cmds, m.precomputeFrameCmdsForCurrentDetail()...)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	case loadProgressMsg:
		m.loading = true
		m.loadStep = msg.Step
		m.loadTotal = msg.Total
		m.loadStage = msg.Label
		cmds = append(cmds, m.loadProgressCmd())
	case loadProgressDoneMsg:
		m.loadProgress = nil
	case loadedMsg:
		m.loading = false
		m.chatLoading = false
		m.chatLoadSpace = ""
		m.loadProgress = nil
		m.loadStage = ""
		m.loadStep = 0
		m.loadTotal = 0
		if msg.err != nil {
			m.err = msg.err.Error()
		}
		if msg.err == nil {
			m.cacheLoaded = true
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
		if msg.calendars.Items != nil {
			m.calendars = msg.calendars.Items
			m.calendarIndex = indexOfCalendar(m.calendars, msg.calendarID)
		}
		m.meetSpaces = msg.meet.Items
		m.taskLists = msg.taskLists.Items
		m.taskListIndex = indexOfTaskList(m.taskLists, msg.taskListID)
		m.tasks = msg.tasks.Items
		m.taskNext = msg.tasks.NextPageToken
		m.driveFiles = msg.driveFiles.Items
		m.driveNext = msg.driveFiles.NextPageToken
		m.docFiles = msg.docFiles.Items
		m.docNext = msg.docFiles.NextPageToken
		m.doc = msg.doc
		m.docLoadingID = ""
		m.clampSelections()
		m.persistWorkspaceCache()
		cmds = append(cmds, m.subscribeCmd())
		cmds = append(cmds, m.enrichSpacesCmds()...)
		cmds = append(cmds, m.enrichSendersCmds()...)
		cmds = append(cmds, m.imageDownloadCmdsForWorkspace()...)
		cmds = append(cmds, m.precomputeFrameCmdsForCurrentDetail()...)
	case chatLoadedMsg:
		if !m.finishChatLoad(msg.spaceName, msg.loadID) {
			break
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
			break
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
		case FeatureTasks:
			if tasks, ok := msg.items.([]api.TaskItem); ok {
				m.tasks = append(m.tasks, tasks...)
				m.taskNext = msg.next
				m.toast = "more tasks loaded"
				m.persistWorkspaceCache()
			}
		case FeatureDrive:
			if files, ok := msg.items.([]api.DriveFile); ok {
				m.driveFiles = append(m.driveFiles, files...)
				m.driveNext = msg.next
				m.toast = "more drive files loaded"
				m.persistWorkspaceCache()
			}
		case FeatureDocs:
			if files, ok := msg.items.([]api.DriveFile); ok {
				m.docFiles = append(m.docFiles, files...)
				m.docNext = msg.next
				m.toast = "more docs loaded"
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
			if strings.TrimSpace(m.search) != "" {
				m.toast = "search: " + m.search
			} else {
				m.toast = fallback(m.mailFolder, defaultMailFolder)
			}
			m.clampSelections()
			m.persistWorkspaceCache()
			cmds = append(cmds, m.imageDownloadCmdsForMail(m.mailThreads)...)
		case FeatureCalendar:
			if msg.calendars.Items != nil {
				m.calendars = msg.calendars.Items
				m.calendarIndex = indexOfCalendar(m.calendars, msg.calendarID)
			}
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
		case FeatureTasks:
			m.taskLists = msg.taskLists.Items
			m.taskListIndex = indexOfTaskList(m.taskLists, msg.taskListID)
			m.tasks = msg.tasks.Items
			m.taskNext = msg.tasks.NextPageToken
			m.toast = "tasks refreshed"
			m.clampSelections()
			m.persistWorkspaceCache()
		case FeatureDrive:
			m.driveFiles = msg.driveFiles.Items
			m.driveNext = msg.driveFiles.NextPageToken
			m.toast = "drive refreshed"
			m.clampSelections()
			m.persistWorkspaceCache()
		case FeatureDocs:
			m.docFiles = msg.docFiles.Items
			m.docNext = msg.docFiles.NextPageToken
			m.doc = msg.doc
			m.docLoadingID = ""
			if strings.TrimSpace(m.search) != "" {
				m.toast = "search: " + m.search
			} else {
				m.toast = "docs refreshed"
			}
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
	case chatEditedMsg:
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
	case chatDeletedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.removeChatMessage(msg.messageID, msg.messageName)
			m.toast = "message deleted"
			m.persistWorkspaceCache()
		}
	case chatSpaceCreatedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.applyChatSpace(msg.space)
			m.toast = "space created"
			m.persistWorkspaceCache()
		}
	case chatReactionMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else if msg.remove {
			delete(m.chatReactions, msg.messageName)
			m.toast = "reaction removed"
		} else {
			m.chatReactions[msg.messageName] = msg.reactionName
			m.toast = "reaction added " + msg.emoji
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
	case mailDraftActionMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.toast = "draft saved"
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
	case taskActionMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.toast = msg.label
			if msg.deleted {
				m.removeTask(msg.taskListID, msg.taskID)
			} else {
				m.applyTask(msg.task)
			}
			m.clampSelections()
			m.persistWorkspaceCache()
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
	case attachmentDownloadedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			break
		}
		if msg.path != "" {
			m.toast = "downloaded to " + compactHomePath(msg.path)
		}
	case docLoadedMsg:
		if msg.documentID != m.selectedDocFile().ID {
			break
		}
		m.docLoadingID = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			break
		}
		m.doc = msg.doc
		m.persistWorkspaceCache()
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
	case userResolvedMsg:
		delete(m.pendingUsers, normalizeUserKey(msg.userID))
		if msg.apiAbsent {
			m.peopleAPIDown = true
			break
		}
		if msg.err != nil {
			break
		}
		m.peopleAPIDown = false
		if msg.label != "" {
			m.userLabels[normalizeUserKey(msg.userID)] = msg.label
			m.persistWorkspaceCache()
		}
	case membersLoadedMsg:
		delete(m.pendingMembers, msg.spaceName)
		if msg.err != nil {
			break
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
	case autosaveMsg:
		if m.modal != nil && m.modal.autosave {
			if err := m.saveDraft(); err == nil {
				m.modal.savedAt = time.Now()
			}
		}
		cmds = append(cmds, m.autosaveTick())
	case imageViewerOpenErrMsg:
		m.toast = "open: " + msg.err
	case detailURLOpenErrMsg:
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

	// The folder rail only exists in the Mail feature; if focus is left on it
	// while switching away, fall back to the list pane so j/k keep working.
	if m.feature != FeatureMail && m.focusedPane == paneMailSidebar {
		m.focusedPane = paneList
	}
	if m.feature == FeatureDocs {
		if prevFeature != FeatureDocs || m.focusedPane == paneAction {
			m.focusedPane = paneList
		}
	}

	// Mail uses a different pane layout than the other features, and its
	// composer pane appears only while focused — so the viewport sizes have
	// to be recomputed whenever the feature or the focused pane changes.
	if m.feature != prevFeature || m.focusedPane != prevFocus {
		m.resize()
	}
	cmds = append(cmds, m.imageDownloadCmdsForCurrentDetail()...)
	cmds = append(cmds, m.precomputeFrameCmdsForCurrentDetail()...)
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
					if m.feature == FeatureMail {
						m.focusMailSidebar()
					} else {
						m.focusedPane = paneList
					}
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
		// In Mail the folder rail sits left of the list, so H steps left
		// into it once the list is already focused.
		if m.feature == FeatureMail && m.focusedPane == paneList {
			m.focusMailSidebar()
			return m, nil
		}
		m.focusedPane = paneList
		return m, nil
	case "L", "ctrl+l":
		if m.feature == FeatureMail && m.focusedPane == paneMailSidebar {
			m.focusedPane = paneList
			return m, nil
		}
		if m.feature == FeatureDocs {
			return m.openSelectedDoc()
		}
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
	case "ctrl+5":
		m.feature = FeatureTasks
	case "ctrl+6":
		m.feature = FeatureDrive
	case "ctrl+7":
		m.feature = FeatureDocs
	case "1":
		// Mail's folder rail is the leftmost pane, so 1 focuses it; every
		// other feature has no rail, so 1 keeps focusing the list.
		if m.feature == FeatureMail {
			m.focusMailSidebar()
			return m, nil
		}
		m.focusedPane = paneList
		return m, nil
	case "2":
		if m.feature == FeatureDocs {
			return m.openSelectedDoc()
		}
		m.focusedPane = paneDetail
		return m, nil
	case "3":
		if m.feature == FeatureDocs {
			return m, nil
		}
		m.focusedPane = paneAction
		m.input.Focus()
		m.vimComposer = vimModeInsert
		return m, nil
	case "j", "down":
		if m.focusedPane == paneMailSidebar {
			m.moveMailFolderCursor(1)
			return m, nil
		}
		if m.focusedPane == paneDetail {
			m.detail.LineDown(1)
			return m, nil
		}
		return m.moveSelection(1)
	case "k", "up":
		if m.focusedPane == paneMailSidebar {
			m.moveMailFolderCursor(-1)
			return m, nil
		}
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
		if m.focusedPane == paneMailSidebar {
			m.mailFolderCursor = 0
			return m, nil
		}
		m.selected[m.feature] = 0
		return m.loadSelectedItem()
	case "G":
		if m.focusedPane == paneDetail {
			m.detail.GotoBottom()
			return m, nil
		}
		if m.focusedPane == paneMailSidebar {
			m.mailFolderCursor = max(0, len(m.mailFolderList())-1)
			return m, nil
		}
		m.selected[m.feature] = m.listLen() - 1
		return m.loadSelectedItem()
	case "enter", "o":
		if m.focusedPane == paneMailSidebar {
			return m.selectMailFolder()
		}
		if m.focusedPane == paneDetail {
			if att, ok := m.detailAttachmentAtCursor(); ok {
				if att.IsImage() {
					m.openImageViewer(att)
					return m, nil
				}
				return m.downloadAttachment(att)
			}
			if next, cmd, ok := m.openDetailURLAtCursor(); ok {
				return next, cmd
			}
		}
		if m.feature == FeatureMail && m.focusedPane == paneList {
			// Mail browses the inbox full-screen; opening a thread swaps in
			// the reading pane the same way clicking a Gmail row does.
			m.focusedPane = paneDetail
			return m, nil
		}
		if m.feature == FeatureDocs && m.focusedPane == paneList {
			return m.openSelectedDoc()
		}
		if m.feature == FeatureDrive {
			return m.downloadSelectedDriveFile()
		}
		m.toast = m.openHint()
	case "i":
		if m.feature == FeatureDocs {
			return m, nil
		}
		m.focusedPane = paneAction
		m.input.Focus()
		m.vimComposer = vimModeInsert
	case "a":
		if m.feature == FeatureDocs {
			return m, nil
		}
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
	case "u":
		if m.feature == FeatureMail {
			return m.toggleSelectedUnread()
		}
	case "c":
		if m.feature == FeatureMail {
			m.openMailCompose(nil, mailComposeNew)
		} else if m.feature == FeatureCalendar {
			m.openEventCompose(nil)
		}
	case "R":
		if m.feature == FeatureMail {
			thread := m.selectedMail()
			m.openMailCompose(&thread, mailComposeReply)
		} else if m.feature == FeatureChat {
			m.loading = true
			if len(m.imageErrors) > 0 {
				m.imageErrors = map[string]string{}
				m.imageVersion++
			}
			return m, m.loadAllCmd()
		}
	case "A":
		if m.feature == FeatureMail {
			thread := m.selectedMail()
			m.openMailCompose(&thread, mailComposeReplyAll)
		}
	case "l":
		if m.feature == FeatureMail {
			m.openMailLabelModal()
		}
	case "f":
		if m.feature == FeatureMail {
			thread := m.selectedMail()
			m.openMailCompose(&thread, mailComposeForward)
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
		if m.feature == FeatureChat {
			m.beginCreateChatSpace()
		} else if m.feature == FeatureCalendar {
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
		if m.feature == FeatureChat {
			return m.deleteSelectedChatMessage()
		}
		if m.feature == FeatureCalendar {
			return m.deleteSelectedEvent()
		}
		if m.feature == FeatureTasks {
			return m.deleteSelectedTask()
		}
	case " ", "space":
		if m.feature == FeatureTasks {
			return m.toggleSelectedTaskCompleted()
		}
	case "t":
		if m.feature == FeatureCalendar {
			m.weekOffset = 0
			m.toast = "today"
		}
	case "]":
		if m.feature == FeatureCalendar {
			return m.moveCalendar(1)
		} else if m.feature == FeatureTasks {
			return m.moveTaskList(1)
		}
	case "[":
		if m.feature == FeatureCalendar {
			return m.moveCalendar(-1)
		} else if m.feature == FeatureTasks {
			return m.moveTaskList(-1)
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
		if m.feature == FeatureChat {
			if msg, ok := m.chatMessageUnderCursor(); ok {
				m.beginEditChatMessage(msg)
			} else {
				m.toast = "move cursor onto a message to edit"
			}
			return m, nil
		}
		if m.feature == FeatureCalendar {
			event := m.selectedEvent()
			if event.ID != "" {
				m.openEventCompose(&event)
			}
			return m, nil
		}
		if m.feature == FeatureMeet {
			return m.endSelectedMeet()
		}
	case ">":
		if m.feature == FeatureCalendar {
			return m.moveSelectedEventToNextCalendar()
		}
	case "+":
		if m.feature == FeatureChat {
			return m.addSelectedChatReaction()
		}
	case "-":
		if m.feature == FeatureChat {
			return m.removeSelectedChatReaction()
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
		if m.createSpaceMode {
			displayName, members := parseChatSpaceSetupInput(value)
			m.input.SetValue("")
			m.focusedPane = paneList
			m.input.Blur()
			m.clearReplyContext()
			return m, func() tea.Msg {
				var space api.Space
				var err error
				if len(members) > 0 {
					space, err = m.client.SetupChatSpace(m.ctx, displayName, members)
				} else {
					space, err = m.client.CreateChatSpace(m.ctx, displayName)
				}
				return chatSpaceCreatedMsg{space: space, err: err}
			}
		}
		if m.editMessageName != "" {
			messageName := m.editMessageName
			messageID := m.editMessageID
			m.input.SetValue("")
			m.focusedPane = paneList
			m.input.Blur()
			m.clearReplyContext()
			return m, func() tea.Msg {
				msg, err := m.client.EditChatMessage(m.ctx, messageName, value)
				return chatEditedMsg{messageID: messageID, message: msg, err: err}
			}
		}
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

func spaceFromMessageName(value string) string {
	if idx := strings.Index(value, "/messages/"); idx > 0 {
		return value[:idx]
	}
	return ""
}

func normalizeUserKey(value string) string {
	return api.NormalizeUserID(value)
}

func (m *Model) markSelfUser(value string) {
	key := normalizeUserKey(value)
	if key == "" {
		return
	}
	m.selfUserIDs[key] = true
}

func (m Model) isSelfUserID(value string) bool {
	key := normalizeUserKey(value)
	if key == "" {
		return false
	}
	if key == "me" {
		return true
	}
	return m.inferredSelfUserIDs()[key]
}

func (m Model) inferredSelfUserIDs() map[string]bool {
	return api.InferSelfUserIDs(m.spaces, m.membersBySpace, m.selfUserIDs)
}

func (m Model) stripSelfFromSpaceTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := splitSpaceTitleParts(value)
	if len(parts) <= 1 {
		if m.isSelfSpaceLabel(value) {
			return ""
		}
		return value
	}
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || m.isSelfSpaceLabel(part) {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, ", ")
}

func splitSpaceTitleParts(value string) []string {
	raw := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ';', '|', '\n', '\r', '·', '•':
			return true
		default:
			return false
		}
	})
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func (m Model) isSelfSpaceLabel(value string) bool {
	needle := normalizeSpaceLabel(value)
	if needle == "" {
		return false
	}
	for userID := range m.inferredSelfUserIDs() {
		if normalizeSpaceLabel(userID) == needle {
			return true
		}
		if label := m.userLabels[userID]; normalizeSpaceLabel(label) == needle {
			return true
		}
	}
	return false
}

func normalizeSpaceLabel(value string) string {
	value = strings.TrimSpace(api.UserIDFromName(value))
	value = strings.Join(strings.Fields(value), " ")
	return strings.ToLower(value)
}

func (m *Model) normalizeUserCaches() {
	if m.userLabels != nil {
		normalized := make(map[string]string, len(m.userLabels))
		for userID, label := range m.userLabels {
			key := normalizeUserKey(userID)
			if key == "" {
				continue
			}
			normalized[key] = label
		}
		m.userLabels = normalized
	}
	if m.selfUserIDs != nil {
		normalized := make(map[string]bool, len(m.selfUserIDs))
		for userID, isSelf := range m.selfUserIDs {
			if !isSelf {
				continue
			}
			key := normalizeUserKey(userID)
			if key != "" {
				normalized[key] = true
			}
		}
		m.selfUserIDs = normalized
	}
	m.selfUserIDs = api.InferSelfUserIDs(m.spaces, m.membersBySpace, m.selfUserIDs)
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

func (m Model) loadProgressCmd() tea.Cmd {
	progress := m.loadProgress
	if progress == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-progress
		if !ok {
			return loadProgressDoneMsg{}
		}
		return msg
	}
}

func sendLoadProgress(progress chan loadProgressMsg, step int, label string) {
	if progress == nil {
		return
	}
	select {
	case progress <- loadProgressMsg{Step: step, Total: len(workspaceInitialLoadStages), Label: label}:
	default:
	}
}

func (m Model) loadAllCmd() tea.Cmd {
	return m.loadAllWithProgressCmd(nil)
}

func (m Model) loadAllWithProgressCmd(progress chan loadProgressMsg) tea.Cmd {
	return func() tea.Msg {
		if progress != nil {
			defer close(progress)
		}
		ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
		defer cancel()

		sendLoadProgress(progress, 1, "Auth")
		auth, authErr := m.client.AuthStatus(ctx)
		sendLoadProgress(progress, 2, "Chat spaces")
		spaces, spacesErr := m.client.ChatSpaces(ctx)
		selectedSpace := ""
		if len(spaces.Items) > 0 {
			selectedSpace = spaces.Items[clamp(m.selected[FeatureChat], len(spaces.Items))].Name
		}
		sendLoadProgress(progress, 3, "Current chat")
		messages, messagesErr := m.client.ChatMessages(ctx, selectedSpace, "")
		sendLoadProgress(progress, 4, "Mail labels")
		labels, labelsErr := m.client.MailLabels(ctx)
		sendLoadProgress(progress, 5, "Inbox")
		threads, threadsErr := m.client.MailThreads(ctx, api.MailQuery{Label: "Inbox"})
		sendLoadProgress(progress, 6, "Calendars")
		calendars, calendarsErr := m.client.CalendarLists(ctx)
		calendarID := selectedCalendarID(calendars.Items, m.calendarIndex)
		sendLoadProgress(progress, 7, "Calendar events")
		events, eventsErr := m.client.CalendarEvents(ctx, api.CalendarQuery{CalendarID: calendarID})
		sendLoadProgress(progress, 8, "Meet")
		meet, meetErr := m.client.MeetSpaces(ctx)
		sendLoadProgress(progress, 9, "Task lists")
		taskLists, taskListsErr := m.client.TaskLists(ctx)
		tasks := api.Page[api.TaskItem]{}
		taskListID := ""
		var tasksErr error
		sendLoadProgress(progress, 10, "Tasks")
		if len(taskLists.Items) > 0 {
			taskListID = taskLists.Items[clamp(m.taskListIndex, len(taskLists.Items))].ID
			tasks, tasksErr = m.client.Tasks(ctx, api.TaskQuery{TaskListID: taskListID})
		}
		sendLoadProgress(progress, 11, "Drive files")
		driveFiles, driveErr := m.client.DriveFiles(ctx, api.DriveQuery{})
		sendLoadProgress(progress, 12, "Docs")
		docFiles, docsErr := m.client.Docs(ctx, api.DriveQuery{})
		doc := api.DocDocument{}
		var docErr error
		sendLoadProgress(progress, 13, "Document preview")
		if len(docFiles.Items) > 0 {
			doc = api.DocDocument{ID: docFiles.Items[clamp(m.selected[FeatureDocs], len(docFiles.Items))].ID}
			doc, docErr = m.client.Doc(ctx, doc.ID)
		}

		err := firstErr(authErr, spacesErr, messagesErr, labelsErr, threadsErr, calendarsErr, eventsErr, meetErr, taskListsErr, tasksErr, driveErr, docsErr, docErr)
		return loadedMsg{
			auth: auth, spaces: spaces, messages: messages, labels: labels, threads: threads, events: events, calendars: calendars, calendarID: calendarID, meet: meet,
			taskLists: taskLists, tasks: tasks, taskListID: taskListID, driveFiles: driveFiles, docFiles: docFiles, doc: doc,
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
		folder := m.mailFolderByName(m.mailFolder)
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			labels, labelsErr := m.client.MailLabels(ctx)
			threads, threadsErr := m.client.MailThreads(ctx, mailQueryForFolder(folder, search, ""))
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
		calendarID := m.selectedCalendar().ID
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			calendars, calendarsErr := m.client.CalendarLists(ctx)
			if calendarID == "" {
				calendarID = selectedCalendarID(calendars.Items, m.calendarIndex)
			}
			events, eventsErr := m.client.CalendarEvents(ctx, api.CalendarQuery{CalendarID: calendarID, Search: search})
			return featureRefreshedMsg{feature: FeatureCalendar, calendars: calendars, calendarID: calendarID, events: events, err: firstErr(calendarsErr, eventsErr)}
		}
	case FeatureMeet:
		m.loading = true
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			meet, err := m.client.MeetSpaces(ctx)
			return featureRefreshedMsg{feature: FeatureMeet, meet: meet, err: err}
		}
	case FeatureTasks:
		m.loading = true
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			taskLists, taskListsErr := m.client.TaskLists(ctx)
			tasks := api.Page[api.TaskItem]{}
			taskListID := ""
			var tasksErr error
			if len(taskLists.Items) > 0 {
				taskListID = taskLists.Items[clamp(m.taskListIndex, len(taskLists.Items))].ID
				tasks, tasksErr = m.client.Tasks(ctx, api.TaskQuery{TaskListID: taskListID})
			}
			return featureRefreshedMsg{feature: FeatureTasks, taskLists: taskLists, tasks: tasks, taskListID: taskListID, err: firstErr(taskListsErr, tasksErr)}
		}
	case FeatureDrive:
		m.loading = true
		search := m.search
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			files, err := m.client.DriveFiles(ctx, api.DriveQuery{Search: search})
			return featureRefreshedMsg{feature: FeatureDrive, driveFiles: files, err: err}
		}
	case FeatureDocs:
		m.loading = true
		search := m.search
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
			defer cancel()

			files, filesErr := m.client.Docs(ctx, api.DriveQuery{Search: search})
			doc := api.DocDocument{}
			var docErr error
			if len(files.Items) > 0 {
				doc, docErr = m.client.Doc(ctx, files.Items[clamp(m.selected[FeatureDocs], len(files.Items))].ID)
			}
			return featureRefreshedMsg{feature: FeatureDocs, docFiles: files, doc: doc, err: firstErr(filesErr, docErr)}
		}
	default:
		return m, nil
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
		search := m.search
		folder := m.mailFolderByName(m.mailFolder)
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.MailThreads(m.ctx, mailQueryForFolder(folder, search, token))
			return featureLoadedMsg{feature: FeatureMail, items: page.Items, next: page.NextPageToken, err: err}
		}
	case FeatureCalendar:
		if m.calendarNext == "" {
			m.toast = "no more events"
			return m, nil
		}
		token := m.calendarNext
		calendarID := m.selectedCalendar().ID
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.CalendarEvents(m.ctx, api.CalendarQuery{CalendarID: calendarID, Search: m.search, PageToken: token})
			return featureLoadedMsg{feature: FeatureCalendar, items: page.Items, next: page.NextPageToken, err: err}
		}
	case FeatureTasks:
		if m.taskNext == "" {
			m.toast = "no more tasks"
			return m, nil
		}
		list := m.selectedTaskList()
		token := m.taskNext
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.Tasks(m.ctx, api.TaskQuery{TaskListID: list.ID, PageToken: token})
			return featureLoadedMsg{feature: FeatureTasks, items: page.Items, next: page.NextPageToken, err: err}
		}
	case FeatureDrive:
		if m.driveNext == "" {
			m.toast = "no more drive files"
			return m, nil
		}
		token := m.driveNext
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.DriveFiles(m.ctx, api.DriveQuery{Search: m.search, PageToken: token})
			return featureLoadedMsg{feature: FeatureDrive, items: page.Items, next: page.NextPageToken, err: err}
		}
	case FeatureDocs:
		if m.docNext == "" {
			m.toast = "no more docs"
			return m, nil
		}
		token := m.docNext
		m.loading = true
		return m, func() tea.Msg {
			page, err := m.client.Docs(m.ctx, api.DriveQuery{Search: m.search, PageToken: token})
			return featureLoadedMsg{feature: FeatureDocs, items: page.Items, next: page.NextPageToken, err: err}
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

	// Mail's Gmail-style layout puts the sidebar on the left, so the reading
	// pane and composer are sized against the wider right column instead of
	// the shared 30/70 split below.
	if m.feature == FeatureMail {
		sidebarW := mailSidebarWidth(w)
		mainW := max(30, w-sidebarW-leftHBorder-detailHBorder)
		actionHeight := max(3, min(8, strings.Count(m.input.Value(), "\n")+3))
		// The composer pane only takes height while it is focused; otherwise
		// the reading pane fills the whole right column.
		mainContentH := max(5, h-statusH-detailVBorder)
		if m.focusedPane == paneAction {
			mainContentH = max(5, h-statusH-detailVBorder-actionHeight-actionVBorder)
		}
		mailDetailWidth := max(10, mainW-detailHPad)
		if m.cfg.VimMode {
			mailDetailWidth = max(10, mailDetailWidth-2)
		}
		m.detail.Width = mailDetailWidth
		m.detail.Height = max(3, mainContentH)
		m.input.SetWidth(max(10, mainW-actionHPad))
		m.input.SetHeight(max(2, actionHeight))
		return
	}

	if m.feature == FeatureDocs {
		mainW := max(20, w-detailHBorder)
		mainH := max(5, h-statusH-detailVBorder)
		detailWidth := max(10, mainW-detailHPad)
		if m.cfg.VimMode {
			detailWidth = max(10, detailWidth-2)
		}
		m.detail.Width = detailWidth
		m.detail.Height = max(3, mainH)
		m.input.SetWidth(max(10, mainW-actionHPad))
		m.input.SetHeight(2)
		return
	}

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
	m.taskListIndex = clamp(m.taskListIndex, len(m.taskLists))
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
	return m.loadSelectedItem()
}

func (m Model) loadSelectedItem() (Model, tea.Cmd) {
	switch m.feature {
	case FeatureChat:
		return m.loadSelectedChat()
	case FeatureDocs:
		return m.loadSelectedDoc()
	default:
		return m, nil
	}
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
	case FeatureTasks:
		return len(m.tasks)
	case FeatureDrive:
		return len(m.driveFiles)
	case FeatureDocs:
		return len(m.docFiles)
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

func (m *Model) selectSpaceByName(spaceName string) bool {
	if spaceName == "" {
		m.clampSelections()
		return false
	}
	for index, space := range m.visibleSpaces() {
		if space.Name == spaceName {
			m.selected[FeatureChat] = index
			return true
		}
	}
	m.clampSelections()
	return false
}

func (m *Model) promoteSpaceToTop(spaceName string) {
	if spaceName == "" || len(m.spaces) < 2 {
		return
	}
	selectedName := m.selectedSpace().Name
	for index, space := range m.spaces {
		if space.Name != spaceName {
			continue
		}
		if index > 0 {
			copy(m.spaces[1:index+1], m.spaces[:index])
			m.spaces[0] = space
		}
		m.selectSpaceByName(selectedName)
		return
	}
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

func (m Model) selectedCalendar() api.CalendarListItem {
	if len(m.calendars) == 0 {
		return api.CalendarListItem{ID: "primary", Summary: "Primary", Primary: true}
	}
	return m.calendars[clamp(m.calendarIndex, len(m.calendars))]
}

func (m Model) selectedMeet() api.MeetSpace {
	if len(m.meetSpaces) == 0 {
		return api.MeetSpace{}
	}
	return m.meetSpaces[clamp(m.selected[FeatureMeet], len(m.meetSpaces))]
}

func (m Model) selectedTaskList() api.TaskList {
	if len(m.taskLists) == 0 {
		return api.TaskList{}
	}
	return m.taskLists[clamp(m.taskListIndex, len(m.taskLists))]
}

func (m Model) selectedTask() api.TaskItem {
	if len(m.tasks) == 0 {
		return api.TaskItem{}
	}
	return m.tasks[clamp(m.selected[FeatureTasks], len(m.tasks))]
}

func (m Model) selectedDriveFile() api.DriveFile {
	if len(m.driveFiles) == 0 {
		return api.DriveFile{}
	}
	return m.driveFiles[clamp(m.selected[FeatureDrive], len(m.driveFiles))]
}

func (m Model) selectedDocFile() api.DriveFile {
	if len(m.docFiles) == 0 {
		return api.DriveFile{}
	}
	return m.docFiles[clamp(m.selected[FeatureDocs], len(m.docFiles))]
}

func (m Model) openSelectedDoc() (Model, tea.Cmd) {
	m.focusedPane = paneDetail
	return m.loadSelectedDoc()
}

func (m Model) loadSelectedDoc() (Model, tea.Cmd) {
	file := m.selectedDocFile()
	if file.ID == "" {
		m.doc = api.DocDocument{}
		m.docLoadingID = ""
		return m, nil
	}
	if m.doc.ID == file.ID && m.doc.Body != "" {
		return m, nil
	}
	m.doc = api.DocDocument{ID: file.ID, Title: file.Name}
	m.docLoadingID = file.ID
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()

		doc, err := m.client.Doc(ctx, file.ID)
		return docLoadedMsg{documentID: file.ID, doc: doc, err: err}
	}
}

func indexOfTaskList(lists []api.TaskList, id string) int {
	if id == "" {
		return 0
	}
	for i, list := range lists {
		if list.ID == id {
			return i
		}
	}
	return 0
}

func indexOfCalendar(calendars []api.CalendarListItem, id string) int {
	if id == "" {
		return 0
	}
	for i, calendar := range calendars {
		if calendar.ID == id {
			return i
		}
	}
	return 0
}

func selectedCalendarID(calendars []api.CalendarListItem, index int) string {
	if len(calendars) == 0 {
		return "primary"
	}
	return calendars[clamp(index, len(calendars))].ID
}

func (m Model) moveCalendar(delta int) (Model, tea.Cmd) {
	if len(m.calendars) == 0 {
		m.toast = "no calendars"
		return m, nil
	}
	next := clamp(m.calendarIndex+delta, len(m.calendars))
	if next == m.calendarIndex {
		return m, nil
	}
	m.calendarIndex = next
	m.selected[FeatureCalendar] = 0
	return m.loadSelectedCalendar()
}

func (m Model) loadSelectedCalendar() (Model, tea.Cmd) {
	calendar := m.selectedCalendar()
	if calendar.ID == "" {
		m.events = nil
		m.calendarNext = ""
		return m, nil
	}
	m.loading = true
	m.events = nil
	m.calendarNext = ""
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()

		page, err := m.client.CalendarEvents(ctx, api.CalendarQuery{CalendarID: calendar.ID, Search: m.search})
		return featureRefreshedMsg{
			feature:    FeatureCalendar,
			calendars:  api.Page[api.CalendarListItem]{Items: m.calendars},
			calendarID: calendar.ID,
			events:     page,
			err:        err,
		}
	}
}

func (m Model) moveTaskList(delta int) (Model, tea.Cmd) {
	if len(m.taskLists) == 0 {
		m.toast = "no task lists"
		return m, nil
	}
	next := clamp(m.taskListIndex+delta, len(m.taskLists))
	if next == m.taskListIndex {
		return m, nil
	}
	m.taskListIndex = next
	m.selected[FeatureTasks] = 0
	return m.loadSelectedTaskList()
}

func (m Model) loadSelectedTaskList() (Model, tea.Cmd) {
	list := m.selectedTaskList()
	if list.ID == "" {
		m.tasks = nil
		m.taskNext = ""
		return m, nil
	}
	m.loading = true
	m.tasks = nil
	m.taskNext = ""
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()

		page, err := m.client.Tasks(ctx, api.TaskQuery{TaskListID: list.ID})
		return featureRefreshedMsg{
			feature:    FeatureTasks,
			taskLists:  api.Page[api.TaskList]{Items: m.taskLists},
			tasks:      page,
			taskListID: list.ID,
			err:        err,
		}
	}
}

// mailFolderByName resolves a folder display name to its definition, falling
// back to the Inbox so the inbox is always reachable.
func (m Model) mailFolderByName(name string) api.MailLabel {
	for _, folder := range m.mailFolderList() {
		if strings.EqualFold(folder.Name, name) {
			return folder
		}
	}
	return mailSystemFolderDefs[0]
}

// mailFolderIndex returns the position of a folder name in the sidebar list.
func (m Model) mailFolderIndex(name string) int {
	for i, folder := range m.mailFolderList() {
		if strings.EqualFold(folder.Name, name) {
			return i
		}
	}
	return 0
}

// mailQueryForFolder builds the Gmail thread query for a folder, carrying the
// resolved label IDs / search expression so custom labels and folders like
// Spam or All Mail fetch correctly.
func mailQueryForFolder(folder api.MailLabel, search, pageToken string) api.MailQuery {
	return api.MailQuery{
		Label:            folder.Name,
		LabelIDs:         folder.LabelIDs,
		LabelQuery:       folder.Query,
		IncludeSpamTrash: folder.IncludeSpamTrash,
		Search:           search,
		PageToken:        pageToken,
	}
}

// focusMailSidebar moves focus onto the folder rail, placing the cursor on the
// folder that is currently loaded.
func (m *Model) focusMailSidebar() {
	m.focusedPane = paneMailSidebar
	m.mailFolderCursor = m.mailFolderIndex(m.mailFolder)
}

func (m *Model) moveMailFolderCursor(delta int) {
	folders := m.mailFolderList()
	if len(folders) == 0 {
		m.mailFolderCursor = 0
		return
	}
	m.mailFolderCursor = clamp(m.mailFolderCursor+delta, len(folders))
}

// selectMailFolder loads the folder under the sidebar cursor and moves focus
// to the inbox list so the user can start browsing it immediately.
func (m Model) selectMailFolder() (Model, tea.Cmd) {
	folders := m.mailFolderList()
	if len(folders) == 0 {
		return m, nil
	}
	folder := folders[clamp(m.mailFolderCursor, len(folders))]
	m.mailFolder = folder.Name
	m.search = ""
	m.selected[FeatureMail] = 0
	m.focusedPane = paneList
	return m.loadMailFolder(folder)
}

// loadMailFolder fetches the thread list for a folder, replacing the inbox.
func (m Model) loadMailFolder(folder api.MailLabel) (Model, tea.Cmd) {
	m.loading = true
	m.mailThreads = nil
	m.mailNext = ""
	labels := m.mailLabels
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
		defer cancel()

		page, err := m.client.MailThreads(ctx, mailQueryForFolder(folder, "", ""))
		return featureRefreshedMsg{
			feature: FeatureMail,
			labels:  labels,
			threads: page,
			err:     err,
		}
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

func (m *Model) applyTask(task api.TaskItem) {
	if task.ID == "" {
		return
	}
	for i := range m.tasks {
		if m.tasks[i].ID == task.ID {
			m.tasks[i] = task
			return
		}
	}
	m.tasks = append(m.tasks, task)
}

func (m *Model) removeTask(taskListID, taskID string) {
	if taskID == "" {
		return
	}
	out := m.tasks[:0]
	for _, task := range m.tasks {
		if task.ID == taskID && (taskListID == "" || task.TaskListID == "" || task.TaskListID == taskListID) {
			continue
		}
		out = append(out, task)
	}
	m.tasks = out
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

func (m Model) downloadSelectedDriveFile() (Model, tea.Cmd) {
	file := m.selectedDriveFile()
	if file.ID == "" {
		return m, nil
	}
	if strings.HasPrefix(file.MimeType, "application/vnd.google-apps.") {
		m.toast = "Google-native files open in Docs tab"
		return m, nil
	}
	attachment := api.Attachment{
		ID:           file.ID,
		ResourceName: "drive/files/" + file.ID,
		Name:         file.Name,
		ContentType:  file.MimeType,
	}
	return m.downloadAttachment(attachment)
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

func (m Model) toggleSelectedUnread() (Model, tea.Cmd) {
	thread := m.selectedMail()
	if thread.ID == "" {
		return m, nil
	}
	unread := !thread.Unread
	label := "marked read"
	if unread {
		label = "marked unread"
	}
	return m, func() tea.Msg {
		updated, err := m.client.SetMailUnread(m.ctx, thread.ID, unread)
		return mailActionMsg{thread: updated, err: err, label: label}
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

func (m Model) toggleSelectedTaskCompleted() (Model, tea.Cmd) {
	list := m.selectedTaskList()
	task := m.selectedTask()
	if list.ID == "" || task.ID == "" {
		return m, nil
	}
	completed := !strings.EqualFold(task.Status, "completed")
	return m, func() tea.Msg {
		updated, err := m.client.SetTaskCompleted(m.ctx, list.ID, task.ID, completed)
		label := "task unchecked"
		if completed {
			label = "task completed"
		}
		return taskActionMsg{task: updated, taskListID: list.ID, taskID: task.ID, err: err, label: label}
	}
}

func (m Model) deleteSelectedTask() (Model, tea.Cmd) {
	list := m.selectedTaskList()
	task := m.selectedTask()
	if list.ID == "" || task.ID == "" {
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.DeleteTask(m.ctx, list.ID, task.ID)
		return taskActionMsg{taskListID: list.ID, taskID: task.ID, deleted: true, err: err, label: "task deleted"}
	}
}

func (m Model) moveSelectedEventToNextCalendar() (Model, tea.Cmd) {
	event := m.selectedEvent()
	if event.ID == "" {
		return m, nil
	}
	if len(m.calendars) < 2 {
		m.toast = "no other calendar"
		return m, nil
	}
	source := event.CalendarID
	if source == "" {
		source = m.selectedCalendar().ID
	}
	current := indexOfCalendar(m.calendars, source)
	destination := m.calendars[(current+1)%len(m.calendars)]
	if destination.ID == source {
		m.toast = "no other calendar"
		return m, nil
	}
	return m, func() tea.Msg {
		updated, err := m.client.MoveEvent(m.ctx, event.ID, source, destination.ID)
		return eventActionMsg{event: updated, err: err, label: "event moved to " + destination.Summary}
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
	joinURL := space.JoinURL()
	if joinURL == "" {
		m.toast = "no Meet URL available"
		return m, nil
	}
	return m, func() tea.Msg {
		err := openURL(joinURL)
		return meetActionMsg{space: space, err: err, label: "opening browser"}
	}
}

func (m Model) copyMeetLink() (Model, tea.Cmd) {
	space := m.selectedMeet()
	joinURL := space.JoinURL()
	if joinURL == "" {
		m.toast = "no Meet URL to copy"
		return m, nil
	}
	return m, func() tea.Msg {
		err := copyText(joinURL)
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
		text = m.selectedMeet().JoinURL()
	case FeatureTasks:
		task := m.selectedTask()
		text = task.Title
		if task.Notes != "" {
			text = task.Title + "\n\n" + task.Notes
		}
	case FeatureDrive:
		file := m.selectedDriveFile()
		text = file.Name
		if file.WebViewLink != "" {
			text = file.Name + "\n" + file.WebViewLink
		}
	case FeatureDocs:
		text = m.doc.Body
		if text == "" {
			text = m.selectedDocFile().WebViewLink
		}
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
	spaceName := space.SpaceResourceName()
	if spaceName == "" {
		m.toast = "no Meet space to end"
		return m, nil
	}
	if !space.IsActive() {
		m.toast = "no active conference to end"
		return m, nil
	}
	return m, func() tea.Msg {
		err := m.client.EndMeetSpace(m.ctx, spaceName)
		ended := space
		ended.Active = false
		ended.ActiveConference = nil
		if ended.EndTime.IsZero() {
			ended.EndTime = time.Now()
		}
		return meetActionMsg{space: ended, err: err, label: "conference ended"}
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
	case FeatureTasks:
		return "task opened"
	case FeatureDrive:
		return "file opened"
	case FeatureDocs:
		return "document opened"
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
	if m.detailAttachmentAt == nil {
		m.detailAttachmentAt = map[int]api.Attachment{}
	} else {
		for k := range m.detailAttachmentAt {
			delete(m.detailAttachmentAt, k)
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
		fmt.Fprintf(&b, "|meet=%s,%s,%s,%s,%d,%s,%d,%t,%t,%s",
			space.Name,
			space.SpaceName,
			space.MeetingURI,
			space.MeetingCode,
			space.Created.UnixNano(),
			space.AccessType(),
			space.ActiveParticipants,
			space.Recording,
			space.Active,
			activeConference,
		)
	case FeatureTasks:
		list := m.selectedTaskList()
		task := m.selectedTask()
		fmt.Fprintf(&b, "|taskList=%s,%s|task=%s,%s,%s,%s,%d,%d,%d",
			list.ID,
			list.Title,
			task.ID,
			task.Title,
			task.Notes,
			task.Status,
			task.Due.UnixNano(),
			task.Completed.UnixNano(),
			task.Updated.UnixNano(),
		)
	case FeatureDrive:
		file := m.selectedDriveFile()
		fmt.Fprintf(&b, "|drive=%s,%s,%s,%d,%s,%d",
			file.ID,
			file.Name,
			file.MimeType,
			file.ModifiedTime.UnixNano(),
			file.WebViewLink,
			file.Size,
		)
	case FeatureDocs:
		file := m.selectedDocFile()
		fmt.Fprintf(&b, "|docFile=%s,%s,%d|doc=%s,%s,%s,%t",
			file.ID,
			file.Name,
			file.ModifiedTime.UnixNano(),
			m.doc.ID,
			m.doc.Title,
			m.doc.Body,
			m.docLoadingID == file.ID,
		)
		writeDocFingerprint(&b, m.doc)
	}
	return b.String()
}

func writeDocFingerprint(b *strings.Builder, doc api.DocDocument) {
	writeAttachmentFingerprint(b, doc.Attachments)
	for _, block := range doc.Blocks {
		fmt.Fprintf(b, "|block=%s,%d,%d,%s", block.Kind, block.Level, block.ListLevel, block.Text)
		for _, inline := range block.Inlines {
			fmt.Fprintf(b, ",inline=%s,%t,%t,%t,%t,%s",
				inline.Text,
				inline.Bold,
				inline.Italic,
				inline.Underline,
				inline.Strikethrough,
				inline.LinkURL,
			)
		}
		for _, row := range block.Rows {
			fmt.Fprintf(b, ",row=%s", strings.Join(row, "\x1f"))
		}
		if block.Attachment != nil {
			fmt.Fprintf(b, ",image=%s", block.Attachment.PreviewSource())
		}
	}
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
	case FeatureTasks:
		return "tasks:" + m.selectedTaskList().ID + ":" + m.selectedTask().ID
	case FeatureDrive:
		return "drive:" + m.selectedDriveFile().ID
	case FeatureDocs:
		return "docs:" + m.selectedDocFile().ID
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
	case "tasks", "task":
		return string(FeatureTasks)
	case "drive":
		return string(FeatureDrive)
	case "docs", "doc":
		return string(FeatureDocs)
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
