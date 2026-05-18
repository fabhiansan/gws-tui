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

type ChatMessage struct {
	ID           string    `json:"id"`
	Name         string    `json:"name,omitempty"`
	Space        string    `json:"space"`
	SenderID     string    `json:"senderId"`
	SenderName   string    `json:"senderName"`
	Text         string    `json:"text"`
	CreateTime   time.Time `json:"createTime"`
	ThreadID     string    `json:"threadId,omitempty"`
	ParentID     string    `json:"parentId,omitempty"`
	Pending      bool      `json:"pending,omitempty"`
	FromRealtime bool      `json:"fromRealtime,omitempty"`
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
	ID          string    `json:"id"`
	Sender      string    `json:"sender"`
	SenderEmail string    `json:"senderEmail,omitempty"`
	Subject     string    `json:"subject"`
	Snippet     string    `json:"snippet"`
	Date        time.Time `json:"date"`
	Body        string    `json:"body"`
	Unread      bool      `json:"unread"`
	Starred     bool      `json:"starred"`
	Labels      []string  `json:"labelIds,omitempty"`
	QuotedLines int       `json:"quotedLines,omitempty"`
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
	Name               string    `json:"name"`
	MeetingURI         string    `json:"meetingUri"`
	MeetingCode        string    `json:"meetingCode,omitempty"`
	Created            time.Time `json:"created"`
	Type               string    `json:"type,omitempty"`
	ActiveParticipants int       `json:"activeParticipants,omitempty"`
	Recording          bool      `json:"recording"`
	Active             bool      `json:"active"`
}

type WorkspaceClient interface {
	AuthStatus(context.Context) (AuthStatus, error)
	ChatSpaces(context.Context) (Page[Space], error)
	ChatMessages(ctx context.Context, spaceName, pageToken string) (Page[ChatMessage], error)
	SendChatMessage(ctx context.Context, spaceName, text string) (ChatMessage, error)
	SubscribeChat(ctx context.Context, spaceName string) (<-chan ChatMessage, error)
	ChatMembers(ctx context.Context, spaceName string) ([]SpaceMember, error)
	PeopleGet(ctx context.Context, userID string) (Person, error)

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
	UpstreamPath  string
	ForceFixture  bool
	FixtureReason string
}

func NewDefaultClient(opts ClientOptions) WorkspaceClient {
	fixture := NewFixtureClient()
	if opts.ForceFixture || opts.UpstreamPath == "" {
		fixture.reason = opts.FixtureReason
		return fixture
	}
	return &HybridClient{
		primary:  NewCommandClient(opts.UpstreamPath),
		fallback: fixture,
	}
}
