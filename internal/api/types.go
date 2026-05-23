package api

import (
	"context"
	"strings"
	"time"
)

type Page[T any] struct {
	Items         []T    `json:"items"`
	NextPageToken string `json:"nextPageToken,omitempty"`
}

type AuthStatus struct {
	AuthMethod                 string `json:"auth_method"`
	ClientConfig               string `json:"client_config,omitempty"`
	ClientConfigExists         bool   `json:"client_config_exists"`
	CredentialSource           string `json:"credential_source,omitempty"`
	EncryptedCredentials       string `json:"encrypted_credentials,omitempty"`
	EncryptedCredentialsExists bool   `json:"encrypted_credentials_exists"`
	EncryptionValid            bool   `json:"encryption_valid"`
	KeyringBackend             string `json:"keyring_backend,omitempty"`
	PlainCredentials           string `json:"plain_credentials,omitempty"`
	PlainCredentialsExists     bool   `json:"plain_credentials_exists"`
	ProjectID                  string `json:"project_id,omitempty"`
	Storage                    string `json:"storage,omitempty"`
	TokenCacheExists           bool   `json:"token_cache_exists"`
	Error                      string `json:"error,omitempty"`
}

func (s AuthStatus) Valid() bool {
	return s.AuthMethod != "" && (s.TokenCacheExists || s.EncryptionValid || s.PlainCredentialsExists)
}

type Member struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

type SpaceMember struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName,omitempty"`
	Type        string `json:"type,omitempty"`
}

type Person struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email,omitempty"`
}

type Space struct {
	Name           string    `json:"name"`
	DisplayName    string    `json:"displayName,omitempty"`
	FormattedName  string    `json:"formattedName,omitempty"`
	SpaceType      string    `json:"spaceType,omitempty"`
	Unread         bool      `json:"unread,omitempty"`
	Live           bool      `json:"live,omitempty"`
	LastActiveTime time.Time `json:"lastActiveTime,omitempty"`
	LastReadTime   time.Time `json:"lastReadTime,omitempty"`
	Members        []Member  `json:"members,omitempty"`
}

type SpaceReadState struct {
	Name         string    `json:"name"`
	LastReadTime time.Time `json:"lastReadTime,omitempty"`
}

func (s Space) UsesMemberLabels() bool {
	switch strings.ToUpper(strings.TrimSpace(s.SpaceType)) {
	case "DIRECT_MESSAGE", "DM", "GROUP_CHAT":
		return true
	default:
		return false
	}
}

func NormalizeUserID(value string) string {
	value = strings.TrimSpace(UserIDFromName(value))
	if value == "" {
		return ""
	}
	return strings.ToLower(value)
}

func InferSelfUserIDs(spaces []Space, membersBySpace map[string][]SpaceMember, existing map[string]bool) map[string]bool {
	out := make(map[string]bool, len(existing)+1)
	for userID, isSelf := range existing {
		if !isSelf {
			continue
		}
		if key := NormalizeUserID(userID); key != "" {
			out[key] = true
		}
	}
	counts := map[string]int{}
	for _, space := range spaces {
		if !space.UsesMemberLabels() {
			continue
		}
		seen := map[string]bool{}
		for _, member := range membersBySpace[space.Name] {
			if member.Type != "" && member.Type != "HUMAN" {
				continue
			}
			key := NormalizeUserID(member.UserID)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			counts[key]++
		}
	}
	bestID := ""
	bestCount := 0
	secondCount := 0
	for userID, count := range counts {
		if count > bestCount {
			secondCount = bestCount
			bestID = userID
			bestCount = count
		} else if count > secondCount {
			secondCount = count
		}
	}
	if bestID != "" && bestCount >= 2 && bestCount > secondCount {
		out[bestID] = true
	}
	return out
}

func (s Space) Title() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	if s.FormattedName != "" {
		return s.FormattedName
	}
	if s.Name != "" {
		return s.Name
	}
	return "unknown space"
}

// LocalAttachment is a pending upload from the TUI: a file on the local
// filesystem the daemon should push through `chat media upload` before
// embedding the returned resourceName in messages.create.
type LocalAttachment struct {
	Path        string `json:"path"`
	ContentType string `json:"contentType,omitempty"`
	Name        string `json:"name,omitempty"`
}

type Attachment struct {
	ID           string `json:"id,omitempty"`
	ResourceName string `json:"resourceName,omitempty"`
	Name         string `json:"name,omitempty"`
	ContentType  string `json:"contentType,omitempty"`
	URL          string `json:"url,omitempty"`
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
	DownloadURL  string `json:"downloadUrl,omitempty"`
	LocalPath    string `json:"localPath,omitempty"`
}

type ChatMessage struct {
	ID           string       `json:"id"`
	Name         string       `json:"name,omitempty"`
	Space        string       `json:"space"`
	SenderID     string       `json:"senderId"`
	SenderName   string       `json:"senderName"`
	Text         string       `json:"text"`
	Attachments  []Attachment `json:"attachments,omitempty"`
	Cards        []ChatCard   `json:"cards,omitempty"`
	CreateTime   time.Time    `json:"createTime"`
	ThreadID     string       `json:"threadId,omitempty"`
	ParentID     string       `json:"parentId,omitempty"`
	Pending      bool         `json:"pending,omitempty"`
	FromRealtime bool         `json:"fromRealtime,omitempty"`
}

// ChatCard is the TUI's flattened view of a Google Chat Card v2 payload. The
// upstream model wraps widgets in sections; we lose nothing useful by walking
// sections eagerly and exposing widgets as a single ordered slice, with the
// section header (if any) preserved on the widget itself.
type ChatCard struct {
	ID      string       `json:"id,omitempty"`
	Header  *CardHeader  `json:"header,omitempty"`
	Widgets []CardWidget `json:"widgets,omitempty"`
}

type CardHeader struct {
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
	ImageURL string `json:"imageUrl,omitempty"`
	ImageAlt string `json:"imageAlt,omitempty"`
}

// CardWidgetKind tags the concrete payload inside CardWidget. A widget always
// has exactly one non-nil payload — Kind is what the renderer dispatches on
// without having to nil-check every field.
type CardWidgetKind string

const (
	CardWidgetDecoratedText CardWidgetKind = "decoratedText"
	CardWidgetTextParagraph CardWidgetKind = "textParagraph"
	CardWidgetButtonList    CardWidgetKind = "buttonList"
	CardWidgetImage         CardWidgetKind = "image"
	CardWidgetDivider       CardWidgetKind = "divider"
	CardWidgetColumns       CardWidgetKind = "columns"
	CardWidgetGrid          CardWidgetKind = "grid"
	CardWidgetUnknown       CardWidgetKind = "unknown"
)

type CardWidget struct {
	Kind          CardWidgetKind       `json:"kind"`
	SectionHeader string               `json:"sectionHeader,omitempty"`
	DecoratedText *DecoratedTextWidget `json:"decoratedText,omitempty"`
	TextParagraph *TextParagraphWidget `json:"textParagraph,omitempty"`
	ButtonList    *ButtonListWidget    `json:"buttonList,omitempty"`
	Image         *ImageWidget         `json:"image,omitempty"`
	Columns       *ColumnsWidget       `json:"columns,omitempty"`
	Grid          *GridWidget          `json:"grid,omitempty"`
	// UnknownType records the JSON key for widgets the TUI does not model
	// (e.g. selectionInput, dateTimePicker, chipList). Renderer shows a
	// placeholder so the user knows the bot included something we dropped.
	UnknownType string `json:"unknownType,omitempty"`
}

type DecoratedTextWidget struct {
	TopLabel    string    `json:"topLabel,omitempty"`
	Text        string    `json:"text,omitempty"`
	BottomLabel string    `json:"bottomLabel,omitempty"`
	Icon        *CardIcon `json:"icon,omitempty"`
	StartIcon   *CardIcon `json:"startIcon,omitempty"`
	WrapText    bool      `json:"wrapText,omitempty"`
	URL         string    `json:"url,omitempty"`
}

type TextParagraphWidget struct {
	Text string `json:"text,omitempty"`
}

type ButtonListWidget struct {
	Buttons []CardButton `json:"buttons,omitempty"`
}

type CardButton struct {
	Text     string `json:"text,omitempty"`
	AltText  string `json:"altText,omitempty"`
	URL      string `json:"url,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

type ImageWidget struct {
	URL     string `json:"url,omitempty"`
	AltText string `json:"altText,omitempty"`
}

type ColumnsWidget struct {
	// Columns is the per-column widget list. Terminals don't have the width
	// to render true columns, so the renderer prints them stacked with a
	// separator. Keeping the structure preserves order if we ever pick up
	// the slack on a wider layout.
	Columns [][]CardWidget `json:"columns,omitempty"`
}

type GridWidget struct {
	Title string     `json:"title,omitempty"`
	Items []GridItem `json:"items,omitempty"`
}

type GridItem struct {
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
	ImageURL string `json:"imageUrl,omitempty"`
}

type CardIcon struct {
	KnownIcon string `json:"knownIcon,omitempty"`
	IconURL   string `json:"iconUrl,omitempty"`
	AltText   string `json:"altText,omitempty"`
}

type MailLabel struct {
	Name             string   `json:"name"`
	LabelIDs         []string `json:"label_ids,omitempty"`
	Query            string   `json:"q,omitempty"`
	IncludeSpamTrash bool     `json:"include_spam_trash,omitempty"`
}

// MailQuery describes one Gmail thread-list fetch. Label is the folder's
// display name (kept for UI/state); LabelIDs, LabelQuery, and IncludeSpamTrash
// are the resolved request parameters so custom labels and folders like Spam
// or All Mail fetch correctly instead of relying on a name-to-ID guess.
type MailQuery struct {
	Label            string
	LabelIDs         []string `json:"labelIds,omitempty"`
	LabelQuery       string   `json:"labelQuery,omitempty"`
	IncludeSpamTrash bool     `json:"includeSpamTrash,omitempty"`
	Search           string
	PageToken        string
}

type MailThread struct {
	ID          string       `json:"id"`
	Sender      string       `json:"sender"`
	SenderEmail string       `json:"senderEmail,omitempty"`
	To          string       `json:"to,omitempty"`
	Cc          string       `json:"cc,omitempty"`
	Subject     string       `json:"subject"`
	Snippet     string       `json:"snippet"`
	Date        time.Time    `json:"date"`
	Body        string       `json:"body"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Unread      bool         `json:"unread"`
	Starred     bool         `json:"starred"`
	Labels      []string     `json:"labelIds,omitempty"`
	QuotedLines int          `json:"quotedLines,omitempty"`
}

type MailDraft struct {
	To       string `json:"to"`
	Cc       string `json:"cc,omitempty"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	ThreadID string `json:"threadId,omitempty"`
}

type MailDraftItem struct {
	ID        string    `json:"id"`
	MessageID string    `json:"messageId,omitempty"`
	ThreadID  string    `json:"threadId,omitempty"`
	To        string    `json:"to,omitempty"`
	Subject   string    `json:"subject,omitempty"`
	Snippet   string    `json:"snippet,omitempty"`
	Date      time.Time `json:"date,omitempty"`
}

type CalendarQuery struct {
	CalendarID string
	Search     string
	// TimeMin and TimeMax bound the fetch window. When both are zero the
	// client defaults to "from now" so the agenda shows upcoming events;
	// the month grid sets them to the visible month's first and last day.
	TimeMin   time.Time
	TimeMax   time.Time
	PageToken string
}

type CalendarListItem struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	Primary     bool   `json:"primary,omitempty"`
	AccessRole  string `json:"accessRole,omitempty"`
}

type CalendarEvent struct {
	ID          string    `json:"id"`
	CalendarID  string    `json:"calendarId,omitempty"`
	Summary     string    `json:"summary"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Location    string    `json:"location,omitempty"`
	HangoutLink string    `json:"hangoutLink,omitempty"`
	Attendees   []string  `json:"attendees,omitempty"`
	Description string    `json:"description,omitempty"`
	RSVP        string    `json:"rsvp,omitempty"`
	Type        string    `json:"type,omitempty"`
	// AllDay marks events that came from a date-only start (no clock time).
	AllDay bool `json:"allDay,omitempty"`
	// Recurring marks an instance expanded from a recurring event series.
	Recurring bool `json:"recurring,omitempty"`
}

type EventDraft struct {
	CalendarID  string
	Summary     string
	Start       time.Time
	End         time.Time
	Location    string
	Attendees   []string
	Description string
}

type MeetSpace struct {
	Name        string `json:"name"`
	SpaceName   string `json:"spaceName,omitempty"`
	MeetingURI  string `json:"meetingUri"`
	MeetingCode string `json:"meetingCode,omitempty"`
	// Title and InvitedEmails are filled by cross-referencing the conference
	// against the calendar event that shares its Meet link; they stay empty
	// for ad-hoc meetings that have no calendar event.
	Title              string                `json:"title,omitempty"`
	Created            time.Time             `json:"created,omitempty"`
	StartTime          time.Time             `json:"startTime,omitempty"`
	EndTime            time.Time             `json:"endTime,omitempty"`
	Type               string                `json:"type,omitempty"`
	ActiveParticipants int                   `json:"activeParticipants,omitempty"`
	Recording          bool                  `json:"recording"`
	Active             bool                  `json:"active"`
	Config             *MeetSpaceConfig      `json:"config,omitempty"`
	ActiveConference   *MeetActiveConference `json:"activeConference,omitempty"`
	Participants       []MeetParticipant     `json:"participants,omitempty"`
	InvitedEmails      []string              `json:"invitedEmails,omitempty"`
	Recordings         []MeetArtifact        `json:"recordings,omitempty"`
	Transcripts        []MeetArtifact        `json:"transcripts,omitempty"`
}

type MeetSpaceConfig struct {
	AccessType       string `json:"accessType,omitempty"`
	EntryPointAccess string `json:"entryPointAccess,omitempty"`
}

type MeetActiveConference struct {
	ConferenceRecord string `json:"conferenceRecord,omitempty"`
}

type MeetParticipant struct {
	Name        string    `json:"name"`
	DisplayName string    `json:"displayName,omitempty"`
	User        string    `json:"user,omitempty"`
	JoinTime    time.Time `json:"joinTime,omitempty"`
	LeaveTime   time.Time `json:"leaveTime,omitempty"`
}

type MeetArtifact struct {
	Name      string    `json:"name"`
	State     string    `json:"state,omitempty"`
	File      string    `json:"file,omitempty"`
	StartTime time.Time `json:"startTime,omitempty"`
	EndTime   time.Time `json:"endTime,omitempty"`
}

type TaskList struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated,omitempty"`
}

type TaskItem struct {
	ID         string    `json:"id"`
	TaskListID string    `json:"taskListId,omitempty"`
	Title      string    `json:"title"`
	Notes      string    `json:"notes,omitempty"`
	Status     string    `json:"status,omitempty"`
	Due        time.Time `json:"due,omitempty"`
	Completed  time.Time `json:"completed,omitempty"`
	Updated    time.Time `json:"updated,omitempty"`
	Parent     string    `json:"parent,omitempty"`
	Position   string    `json:"position,omitempty"`
}

type TaskQuery struct {
	TaskListID string
	PageToken  string
}

type DriveFile struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	MimeType     string    `json:"mimeType,omitempty"`
	ModifiedTime time.Time `json:"modifiedTime,omitempty"`
	WebViewLink  string    `json:"webViewLink,omitempty"`
	Size         int64     `json:"size,omitempty"`
	Parents      []string  `json:"parents,omitempty"`
}

type DriveQuery struct {
	Search    string
	PageToken string
	MimeType  string
}

type DocDocument struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Body        string       `json:"body"`
	Blocks      []DocBlock   `json:"blocks,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type DocBlockKind string

const (
	DocBlockParagraph DocBlockKind = "paragraph"
	DocBlockTitle     DocBlockKind = "title"
	DocBlockSubtitle  DocBlockKind = "subtitle"
	DocBlockHeading   DocBlockKind = "heading"
	DocBlockListItem  DocBlockKind = "list_item"
	DocBlockTable     DocBlockKind = "table"
	DocBlockImage     DocBlockKind = "image"
)

type DocBlock struct {
	Kind       DocBlockKind `json:"kind"`
	Text       string       `json:"text,omitempty"`
	Inlines    []DocInline  `json:"inlines,omitempty"`
	Level      int          `json:"level,omitempty"`
	ListLevel  int          `json:"listLevel,omitempty"`
	Rows       [][]string   `json:"rows,omitempty"`
	Attachment *Attachment  `json:"attachment,omitempty"`
}

type DocInline struct {
	Text          string `json:"text"`
	Bold          bool   `json:"bold,omitempty"`
	Italic        bool   `json:"italic,omitempty"`
	Underline     bool   `json:"underline,omitempty"`
	Strikethrough bool   `json:"strikethrough,omitempty"`
	LinkURL       string `json:"linkUrl,omitempty"`
}

func (s MeetSpace) AccessType() string {
	if s.Config != nil && s.Config.AccessType != "" {
		return s.Config.AccessType
	}
	return s.Type
}

func (s MeetSpace) JoinURL() string {
	uri := strings.TrimSpace(s.MeetingURI)
	if strings.HasPrefix(strings.ToLower(uri), "https://meet.google.com/") {
		return uri
	}
	return ""
}

func (s MeetSpace) SpaceResourceName() string {
	for _, value := range []string{s.SpaceName, s.Name, s.MeetingURI} {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "spaces/") {
			return value
		}
	}
	return ""
}

func (s MeetSpace) IsActive() bool {
	if s.ActiveConference != nil && s.ActiveConference.ConferenceRecord != "" {
		return true
	}
	return s.Active
}

// DisplayTitle returns the most human-readable label for a conference: the
// matched calendar event title when present, then the meeting code, and only
// the trailing segment of the resource name as a last resort.
func (s MeetSpace) DisplayTitle() string {
	if title := strings.TrimSpace(s.Title); title != "" {
		return title
	}
	if code := strings.TrimSpace(s.MeetingCode); code != "" {
		return code
	}
	return lastSegment(s.Name)
}

// Pinner is implemented by clients that can ask the daemon to keep a chat
// space subscribed across TUI sessions. Only the RemoteClient implements it;
// the TUI uses a type assertion to detect daemon mode.
type Pinner interface {
	PinChatSpace(ctx context.Context, spaceName string) error
	UnpinChatSpace(ctx context.Context, spaceName string) error
}

// ChatReader is implemented by clients that can mark a chat space as read on
// the daemon. The daemon tracks last-read timestamps to compute Space.Unread.
type ChatReader interface {
	MarkChatRead(ctx context.Context, spaceName string) error
}

type WorkspaceClient interface {
	AuthStatus(context.Context) (AuthStatus, error)
	ChatSpaces(context.Context) (Page[Space], error)
	ChatMessages(ctx context.Context, spaceName, pageToken string) (Page[ChatMessage], error)
	SendChatMessage(ctx context.Context, spaceName, text, threadID string, attachments []LocalAttachment) (ChatMessage, error)
	EditChatMessage(ctx context.Context, messageName, text string) (ChatMessage, error)
	DeleteChatMessage(ctx context.Context, messageName string) error
	CreateChatSpace(ctx context.Context, displayName string) (Space, error)
	SetupChatSpace(ctx context.Context, displayName string, members []string) (Space, error)
	AddChatReaction(ctx context.Context, messageName, emoji string) (string, error)
	DeleteChatReaction(ctx context.Context, reactionName string) error
	SubscribeChat(ctx context.Context, spaceName string) (<-chan ChatMessage, error)
	ChatMembers(ctx context.Context, spaceName string) ([]SpaceMember, error)
	PeopleGet(ctx context.Context, userID string) (Person, error)
	DownloadAttachment(ctx context.Context, attachment Attachment, outputPath string) error

	MailLabels(context.Context) ([]MailLabel, error)
	MailThreads(context.Context, MailQuery) (Page[MailThread], error)
	SendMail(context.Context, MailDraft) (MailThread, error)
	MailDrafts(context.Context, string) (Page[MailDraftItem], error)
	CreateMailDraft(context.Context, MailDraft) (MailDraftItem, error)
	SendMailDraft(context.Context, string) (MailThread, error)
	ArchiveMail(context.Context, string) error
	TrashMail(context.Context, string) error
	ToggleStar(context.Context, string) (MailThread, error)
	SetMailUnread(context.Context, string, bool) (MailThread, error)
	ToggleMailLabel(context.Context, string, string) (MailThread, error)

	CalendarLists(context.Context) (Page[CalendarListItem], error)
	CalendarEvents(context.Context, CalendarQuery) (Page[CalendarEvent], error)
	QuickAddEvent(ctx context.Context, text string) (CalendarEvent, error)
	CreateEvent(context.Context, EventDraft) (CalendarEvent, error)
	UpdateEvent(context.Context, string, EventDraft) (CalendarEvent, error)
	MoveEvent(context.Context, string, string, string) (CalendarEvent, error)
	RSVPEvent(ctx context.Context, eventID, response string) (CalendarEvent, error)
	DeleteEvent(context.Context, string) error

	MeetSpaces(context.Context) (Page[MeetSpace], error)
	CreateMeetSpace(context.Context, string) (MeetSpace, error)
	EndMeetSpace(context.Context, string) error

	TaskLists(context.Context) (Page[TaskList], error)
	Tasks(context.Context, TaskQuery) (Page[TaskItem], error)
	SetTaskCompleted(ctx context.Context, taskListID, taskID string, completed bool) (TaskItem, error)
	DeleteTask(ctx context.Context, taskListID, taskID string) error

	DriveFiles(context.Context, DriveQuery) (Page[DriveFile], error)
	Docs(context.Context, DriveQuery) (Page[DriveFile], error)
	Doc(context.Context, string) (DocDocument, error)
	Close() error
}

type ClientOptions struct {
	UpstreamPath string
}

func NewDefaultClient(opts ClientOptions) WorkspaceClient {
	return NewCommandClient(opts.UpstreamPath)
}
