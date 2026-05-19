package api

import (
	"context"
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
	UserID string `json:"userId"`
	Type   string `json:"type,omitempty"`
}

type Person struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email,omitempty"`
}

type Space struct {
	Name          string   `json:"name"`
	DisplayName   string   `json:"displayName,omitempty"`
	FormattedName string   `json:"formattedName,omitempty"`
	SpaceType     string   `json:"spaceType,omitempty"`
	Unread        bool     `json:"unread,omitempty"`
	Live          bool     `json:"live,omitempty"`
	Members       []Member `json:"members,omitempty"`
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

type MailQuery struct {
	Label     string
	Search    string
	PageToken string
}

type MailThread struct {
	ID          string       `json:"id"`
	Sender      string       `json:"sender"`
	SenderEmail string       `json:"senderEmail,omitempty"`
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

type CalendarQuery struct {
	Search    string
	WeekStart time.Time
	PageToken string
}

type CalendarEvent struct {
	ID          string    `json:"id"`
	Summary     string    `json:"summary"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Location    string    `json:"location,omitempty"`
	HangoutLink string    `json:"hangoutLink,omitempty"`
	Attendees   []string  `json:"attendees,omitempty"`
	Description string    `json:"description,omitempty"`
	RSVP        string    `json:"rsvp,omitempty"`
	Type        string    `json:"type,omitempty"`
}

type EventDraft struct {
	Summary     string
	Start       time.Time
	End         time.Time
	Location    string
	Attendees   []string
	Description string
}

type MeetSpace struct {
	Name               string                `json:"name"`
	MeetingURI         string                `json:"meetingUri"`
	MeetingCode        string                `json:"meetingCode,omitempty"`
	Created            time.Time             `json:"created,omitempty"`
	Type               string                `json:"type,omitempty"`
	ActiveParticipants int                   `json:"activeParticipants,omitempty"`
	Recording          bool                  `json:"recording"`
	Active             bool                  `json:"active"`
	Config             *MeetSpaceConfig      `json:"config,omitempty"`
	ActiveConference   *MeetActiveConference `json:"activeConference,omitempty"`
}

type MeetSpaceConfig struct {
	AccessType       string `json:"accessType,omitempty"`
	EntryPointAccess string `json:"entryPointAccess,omitempty"`
}

type MeetActiveConference struct {
	ConferenceRecord string `json:"conferenceRecord,omitempty"`
}

func (s MeetSpace) AccessType() string {
	if s.Config != nil && s.Config.AccessType != "" {
		return s.Config.AccessType
	}
	return s.Type
}

func (s MeetSpace) IsActive() bool {
	if s.ActiveConference != nil && s.ActiveConference.ConferenceRecord != "" {
		return true
	}
	return s.Active
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
	SubscribeChat(ctx context.Context, spaceName string) (<-chan ChatMessage, error)
	ChatMembers(ctx context.Context, spaceName string) ([]SpaceMember, error)
	PeopleGet(ctx context.Context, userID string) (Person, error)
	DownloadAttachment(ctx context.Context, attachment Attachment, outputPath string) error

	MailLabels(context.Context) ([]MailLabel, error)
	MailThreads(context.Context, MailQuery) (Page[MailThread], error)
	SendMail(context.Context, MailDraft) (MailThread, error)
	ArchiveMail(context.Context, string) error
	TrashMail(context.Context, string) error
	ToggleStar(context.Context, string) (MailThread, error)

	CalendarEvents(context.Context, CalendarQuery) (Page[CalendarEvent], error)
	QuickAddEvent(ctx context.Context, text string) (CalendarEvent, error)
	CreateEvent(context.Context, EventDraft) (CalendarEvent, error)
	RSVPEvent(ctx context.Context, eventID, response string) (CalendarEvent, error)
	DeleteEvent(context.Context, string) error

	MeetSpaces(context.Context) (Page[MeetSpace], error)
	CreateMeetSpace(context.Context, string) (MeetSpace, error)
	EndMeetSpace(context.Context, string) error
	Close() error
}

type ClientOptions struct {
	UpstreamPath string
}

func NewDefaultClient(opts ClientOptions) WorkspaceClient {
	return NewCommandClient(opts.UpstreamPath)
}
