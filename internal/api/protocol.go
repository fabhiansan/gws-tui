package api

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	ProtocolVersion = 2
	MaxFrameBytes   = 16 << 20
)

type Envelope struct {
	ID      uint64          `json:"id,omitempty"`
	Kind    string          `json:"kind"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ProtocolError  `json:"error,omitempty"`
	Topic   string          `json:"topic,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type DaemonEvent struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type ProtocolError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type ChatMessagesParams struct {
	SpaceName string `json:"space_name"`
	PageToken string `json:"page_token,omitempty"`
}

type SendChatMessageParams struct {
	SpaceName   string            `json:"space_name"`
	Text        string            `json:"text"`
	ThreadID    string            `json:"thread_id,omitempty"`
	Attachments []LocalAttachment `json:"attachments,omitempty"`
}

type EditChatMessageParams struct {
	MessageName string `json:"message_name"`
	Text        string `json:"text"`
}

type ChatMessageNameParams struct {
	MessageName string `json:"message_name"`
}

type CreateChatSpaceParams struct {
	DisplayName string `json:"display_name"`
}

type SetupChatSpaceParams struct {
	DisplayName string   `json:"display_name"`
	Members     []string `json:"members,omitempty"`
}

type ChatReactionParams struct {
	MessageName  string `json:"message_name,omitempty"`
	ReactionName string `json:"reaction_name,omitempty"`
	Emoji        string `json:"emoji,omitempty"`
}

type SpaceNameParams struct {
	SpaceName string `json:"space_name"`
}

type UserIDParams struct {
	UserID string `json:"user_id"`
}

type DownloadAttachmentParams struct {
	Attachment Attachment `json:"attachment"`
	OutputPath string     `json:"output_path"`
}

type MailThreadsParams struct {
	Query MailQuery `json:"query"`
}

type SendMailParams struct {
	Draft MailDraft `json:"draft"`
}

type MailDraftsParams struct {
	PageToken string `json:"page_token,omitempty"`
}

type MailDraftIDParams struct {
	DraftID string `json:"draft_id"`
}

type ThreadIDParams struct {
	ThreadID string `json:"thread_id"`
}

type SetMailUnreadParams struct {
	ThreadID string `json:"thread_id"`
	Unread   bool   `json:"unread"`
}

type ToggleMailLabelParams struct {
	ThreadID string `json:"thread_id"`
	LabelID  string `json:"label_id"`
}

type CalendarEventsParams struct {
	Query CalendarQuery `json:"query"`
}

type QuickAddEventParams struct {
	Text string `json:"text"`
}

type CreateEventParams struct {
	Draft EventDraft `json:"draft"`
}

type UpdateEventParams struct {
	EventID string     `json:"event_id"`
	Draft   EventDraft `json:"draft"`
}

type MoveEventParams struct {
	EventID               string `json:"event_id"`
	SourceCalendarID      string `json:"source_calendar_id"`
	DestinationCalendarID string `json:"destination_calendar_id"`
}

type RSVPEventParams struct {
	EventID  string `json:"event_id"`
	Response string `json:"response"`
}

type EventIDParams struct {
	EventID string `json:"event_id"`
}

type CreateMeetSpaceParams struct {
	Title string `json:"title"`
}

type MeetSpaceNameParams struct {
	Name string `json:"name"`
}

type TasksParams struct {
	Query TaskQuery `json:"query"`
}

type DriveFilesParams struct {
	Query DriveQuery `json:"query"`
}

type DocIDParams struct {
	DocumentID string `json:"document_id"`
}

type SubscribeTopicsParams struct {
	Topics []string `json:"topics"`
}

type ClientHelloParams struct {
	PID int    `json:"pid"`
	TTY string `json:"tty,omitempty"`
}

type DraftSaveParams struct {
	Key     string         `json:"key"`
	Payload map[string]any `json:"payload"`
}

type DraftLoadParams struct {
	Key string `json:"key"`
}

type DraftLoadResult struct {
	Found   bool           `json:"found"`
	Payload map[string]any `json:"payload,omitempty"`
}

type DaemonStatus struct {
	ProtocolVersion int          `json:"protocol_version"`
	PID             int          `json:"pid"`
	SocketPath      string       `json:"socket_path"`
	UptimeSeconds   int64        `json:"uptime_seconds"`
	SnapshotLoaded  bool         `json:"snapshot_loaded"`
	SnapshotHasData bool         `json:"snapshot_has_data"`
	Clients         []ClientInfo `json:"clients"`
}

type ClientInfo struct {
	ID         uint64    `json:"id"`
	PID        int       `json:"pid,omitempty"`
	TTY        string    `json:"tty,omitempty"`
	AttachedAt time.Time `json:"attached_at"`
	Topics     []string  `json:"topics,omitempty"`
}

func WriteFrame(w io.Writer, env Envelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if len(payload) > MaxFrameBytes {
		return fmt.Errorf("frame exceeds %d bytes", MaxFrameBytes)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func ReadFrame(r io.Reader) (Envelope, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Envelope{}, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 {
		return Envelope{}, errors.New("empty frame")
	}
	if size > MaxFrameBytes {
		return Envelope{}, fmt.Errorf("frame exceeds %d bytes", MaxFrameBytes)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return Envelope{}, err
	}
	var env Envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

func MarshalRaw(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	payload, err := json.Marshal(v)
	return json.RawMessage(payload), err
}
