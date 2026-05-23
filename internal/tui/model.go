package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/fabhiansan/gws-tui/internal/api"
	"github.com/fabhiansan/gws-tui/internal/tui/theme"
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

	focusedPane pane
	loading     bool
	// featureLoading marks panes whose first startup fetch is still in flight,
	// so the list pane shows "Loading…" instead of an empty-state placeholder
	// while the progressive fan-out catches up.
	featureLoading map[Feature]bool
	// featureLoaded marks panes that already have data. Lazily-loaded features
	// (Drive, Docs) consult it so they fetch only once, on first visit.
	featureLoaded     map[Feature]bool
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

	// calendarView switches Calendar between the upcoming agenda and the
	// month grid. calendarCursor is the day the month grid highlights;
	// monthEvents holds the events fetched for calendarMonth (the first day
	// of the grid's month, zero when no month has been loaded yet).
	calendarView   calViewMode
	calendarCursor time.Time
	monthEvents    []api.CalendarEvent
	calendarMonth  time.Time
	// calendarDayEventCursor is the highlighted event within calendarCursor's
	// day while the month grid is focused.
	calendarDayEventCursor int
	calendarFeedback       calendarActivityFeedback

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

	selected             map[Feature]int
	persisted            persistedState
	cache                workspaceCache
	cacheLoaded          bool
	imageFiles           map[string]string
	imageLoading         map[string]bool
	imageErrors          map[string]string
	imageRenders         map[string]inlineImageRender
	imageFramePend       map[string]bool
	imageVersion         int
	imagePlacement       int
	modal                *composeModal
	helpVisible          bool
	helpScroll           int
	helpPending          string
	messagesVisible      bool
	messageLog           []messageLogEntry
	messageLogScroll     int
	messageLogCursor     int
	messageLogCol        int
	messageLogAnchor     int
	messageLogAnchorCol  int
	messageLogVisual     bool
	messageLogVisualLine bool
	messageLogPending    string
	daemonEvents         <-chan api.DaemonEvent
	vimComposer          vimMode
	vimPending           string
	vimRegister          string
	vimRegisterLine      bool

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
	auth          api.AuthStatus
	spaces        api.Page[api.Space]
	messages      api.Page[api.ChatMessage]
	labels        []api.MailLabel
	threads       api.Page[api.MailThread]
	events        api.Page[api.CalendarEvent]
	calendars     api.Page[api.CalendarListItem]
	calendarID    string
	calendarMonth time.Time
	meet          api.Page[api.MeetSpace]
	taskLists     api.Page[api.TaskList]
	tasks         api.Page[api.TaskItem]
	taskListID    string
	driveFiles    api.Page[api.DriveFile]
	docFiles      api.Page[api.DriveFile]
	doc           api.DocDocument
	err           error
	authRequired  bool
}

// authLoadedMsg carries the result of the standalone auth probe fired during
// the progressive cold-start fan-out.
type authLoadedMsg struct {
	auth api.AuthStatus
	err  error
}

// chatSectionLoadedMsg carries the Chat pane's startup data — the space list
// plus the messages of the initially-selected space.
type chatSectionLoadedMsg struct {
	spaces      api.Page[api.Space]
	messages    api.Page[api.ChatMessage]
	spaceName   string
	err         error
	messagesErr error
}

type featureLoadedMsg struct {
	feature Feature
	items   any
	next    string
	err     error
}

type featureRefreshedMsg struct {
	feature       Feature
	labels        []api.MailLabel
	threads       api.Page[api.MailThread]
	events        api.Page[api.CalendarEvent]
	calendars     api.Page[api.CalendarListItem]
	calendarID    string
	calendarMonth time.Time
	meet          api.Page[api.MeetSpace]
	taskLists     api.Page[api.TaskList]
	tasks         api.Page[api.TaskItem]
	taskListID    string
	driveFiles    api.Page[api.DriveFile]
	docFiles      api.Page[api.DriveFile]
	doc           api.DocDocument
	err           error
	// startup marks a refresh issued by the progressive cold-start fan-out (or
	// a lazy first-visit load) rather than a user-triggered refresh. It flips
	// the pane's featureLoading/featureLoaded flags and suppresses the toast.
	startup bool
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
	event        api.CalendarEvent
	err          error
	label        string
	rsvpResponse string
	deleted      bool
}

type calendarActivityFeedback struct {
	eventID  string
	response string
}

// calendarMonthLoadedMsg carries the events fetched for the month grid. It is
// kept separate from the agenda's events so toggling views never refetches.
type calendarMonthLoadedMsg struct {
	month  time.Time
	events []api.CalendarEvent
	err    error
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
