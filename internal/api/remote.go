package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

var ErrRemoteClosed = errors.New("remote client closed")

type RemoteClient struct {
	socketPath string
	conn       net.Conn

	writeMu     sync.Mutex
	mu          sync.Mutex
	nextID      uint64
	pending     map[uint64]chan remoteReply
	chat        map[string][]chan ChatMessage
	events      map[string][]chan DaemonEvent
	closed      bool
	manualClose bool
	closeErr    error
	done        chan struct{}
}

type remoteReply struct {
	result json.RawMessage
	err    error
}

type RemoteClientOption func(*RemoteClient)

func NewRemoteClient(socketPath string) (*RemoteClient, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, err
	}
	client := NewRemoteClientConn(conn)
	client.socketPath = socketPath
	return client, nil
}

func NewRemoteClientConn(conn net.Conn) *RemoteClient {
	client := &RemoteClient{
		conn:    conn,
		pending: map[uint64]chan remoteReply{},
		chat:    map[string][]chan ChatMessage{},
		events:  map[string][]chan DaemonEvent{},
		done:    make(chan struct{}),
	}
	go client.readLoop()
	return client
}

func (c *RemoteClient) AuthStatus(ctx context.Context) (AuthStatus, error) {
	var out AuthStatus
	err := c.request(ctx, "AuthStatus", nil, &out)
	return out, err
}

func (c *RemoteClient) ChatSpaces(ctx context.Context) (Page[Space], error) {
	var out Page[Space]
	err := c.request(ctx, "ChatSpaces", nil, &out)
	return out, err
}

func (c *RemoteClient) ChatMessages(ctx context.Context, spaceName, pageToken string) (Page[ChatMessage], error) {
	var out Page[ChatMessage]
	err := c.request(ctx, "ChatMessages", ChatMessagesParams{SpaceName: spaceName, PageToken: pageToken}, &out)
	return out, err
}

func (c *RemoteClient) SendChatMessage(ctx context.Context, spaceName, text, threadID string, attachments []LocalAttachment) (ChatMessage, error) {
	var out ChatMessage
	err := c.request(ctx, "SendChatMessage", SendChatMessageParams{SpaceName: spaceName, Text: text, ThreadID: threadID, Attachments: attachments}, &out)
	return out, err
}

func (c *RemoteClient) SubscribeChat(ctx context.Context, spaceName string) (<-chan ChatMessage, error) {
	if spaceName == "" {
		return nil, errors.New("space name required")
	}
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}
	out := make(chan ChatMessage, 16)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		close(out)
		return nil, c.closeErrOrDefault()
	}
	c.chat[spaceName] = append(c.chat[spaceName], out)
	c.mu.Unlock()

	if err := c.SubscribeTopics(ctx, []string{ChatMessageTopic(spaceName)}); err != nil {
		c.removeChatSubscription(spaceName, out)
		close(out)
		return nil, err
	}
	return out, nil
}

func (c *RemoteClient) ChatMembers(ctx context.Context, spaceName string) ([]SpaceMember, error) {
	var out []SpaceMember
	err := c.request(ctx, "ChatMembers", SpaceNameParams{SpaceName: spaceName}, &out)
	return out, err
}

func (c *RemoteClient) PeopleGet(ctx context.Context, userID string) (Person, error) {
	var out Person
	err := c.request(ctx, "PeopleGet", UserIDParams{UserID: userID}, &out)
	return out, err
}

func (c *RemoteClient) DownloadAttachment(ctx context.Context, attachment Attachment, outputPath string) error {
	return c.request(ctx, "DownloadAttachment", DownloadAttachmentParams{Attachment: attachment, OutputPath: outputPath}, nil)
}

func (c *RemoteClient) MailLabels(ctx context.Context) ([]MailLabel, error) {
	var out []MailLabel
	err := c.request(ctx, "MailLabels", nil, &out)
	return out, err
}

func (c *RemoteClient) MailThreads(ctx context.Context, query MailQuery) (Page[MailThread], error) {
	var out Page[MailThread]
	err := c.request(ctx, "MailThreads", MailThreadsParams{Query: query}, &out)
	return out, err
}

func (c *RemoteClient) SendMail(ctx context.Context, draft MailDraft) (MailThread, error) {
	var out MailThread
	err := c.request(ctx, "SendMail", SendMailParams{Draft: draft}, &out)
	return out, err
}

func (c *RemoteClient) ArchiveMail(ctx context.Context, threadID string) error {
	return c.request(ctx, "ArchiveMail", ThreadIDParams{ThreadID: threadID}, nil)
}

func (c *RemoteClient) TrashMail(ctx context.Context, threadID string) error {
	return c.request(ctx, "TrashMail", ThreadIDParams{ThreadID: threadID}, nil)
}

func (c *RemoteClient) ToggleStar(ctx context.Context, threadID string) (MailThread, error) {
	var out MailThread
	err := c.request(ctx, "ToggleStar", ThreadIDParams{ThreadID: threadID}, &out)
	return out, err
}

func (c *RemoteClient) CalendarEvents(ctx context.Context, query CalendarQuery) (Page[CalendarEvent], error) {
	var out Page[CalendarEvent]
	err := c.request(ctx, "CalendarEvents", CalendarEventsParams{Query: query}, &out)
	return out, err
}

func (c *RemoteClient) QuickAddEvent(ctx context.Context, text string) (CalendarEvent, error) {
	var out CalendarEvent
	err := c.request(ctx, "QuickAddEvent", QuickAddEventParams{Text: text}, &out)
	return out, err
}

func (c *RemoteClient) CreateEvent(ctx context.Context, draft EventDraft) (CalendarEvent, error) {
	var out CalendarEvent
	err := c.request(ctx, "CreateEvent", CreateEventParams{Draft: draft}, &out)
	return out, err
}

func (c *RemoteClient) RSVPEvent(ctx context.Context, eventID, response string) (CalendarEvent, error) {
	var out CalendarEvent
	err := c.request(ctx, "RSVPEvent", RSVPEventParams{EventID: eventID, Response: response}, &out)
	return out, err
}

func (c *RemoteClient) DeleteEvent(ctx context.Context, eventID string) error {
	return c.request(ctx, "DeleteEvent", EventIDParams{EventID: eventID}, nil)
}

func (c *RemoteClient) MeetSpaces(ctx context.Context) (Page[MeetSpace], error) {
	var out Page[MeetSpace]
	err := c.request(ctx, "MeetSpaces", nil, &out)
	return out, err
}

func (c *RemoteClient) CreateMeetSpace(ctx context.Context, title string) (MeetSpace, error) {
	var out MeetSpace
	err := c.request(ctx, "CreateMeetSpace", CreateMeetSpaceParams{Title: title}, &out)
	return out, err
}

func (c *RemoteClient) EndMeetSpace(ctx context.Context, name string) error {
	return c.request(ctx, "EndMeetSpace", MeetSpaceNameParams{Name: name}, nil)
}

func (c *RemoteClient) PinChatSpace(ctx context.Context, spaceName string) error {
	return c.request(ctx, "PinChatSpace", SpaceNameParams{SpaceName: spaceName}, nil)
}

func (c *RemoteClient) UnpinChatSpace(ctx context.Context, spaceName string) error {
	return c.request(ctx, "UnpinChatSpace", SpaceNameParams{SpaceName: spaceName}, nil)
}

func (c *RemoteClient) MarkChatRead(ctx context.Context, spaceName string) error {
	return c.request(ctx, "MarkChatRead", SpaceNameParams{SpaceName: spaceName}, nil)
}

func (c *RemoteClient) Snapshot(ctx context.Context) (WorkspaceSnapshot, error) {
	var out WorkspaceSnapshot
	err := c.request(ctx, "Snapshot", nil, &out)
	return out, err
}

func (c *RemoteClient) SubscribeTopics(ctx context.Context, topics []string) error {
	return c.request(ctx, "SubscribeTopics", SubscribeTopicsParams{Topics: topics}, nil)
}

func (c *RemoteClient) SubscribeEvents(ctx context.Context, topics []string) (<-chan DaemonEvent, error) {
	if len(topics) == 0 {
		return nil, errors.New("at least one topic required")
	}
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}
	out := make(chan DaemonEvent, 16)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		close(out)
		return nil, c.closeErrOrDefault()
	}
	for _, topic := range topics {
		c.events[topic] = append(c.events[topic], out)
	}
	c.mu.Unlock()
	if err := c.SubscribeTopics(ctx, topics); err != nil {
		c.removeEventSubscription(topics, out)
		close(out)
		return nil, err
	}
	return out, nil
}

func (c *RemoteClient) ClientHello(ctx context.Context, pid int, tty string) error {
	return c.request(ctx, "ClientHello", ClientHelloParams{PID: pid, TTY: tty}, nil)
}

func (c *RemoteClient) Ping(ctx context.Context) error {
	return c.request(ctx, "Ping", nil, nil)
}

func (c *RemoteClient) DraftSave(ctx context.Context, key string, payload map[string]any) error {
	return c.request(ctx, "DraftSave", DraftSaveParams{Key: key, Payload: payload}, nil)
}

func (c *RemoteClient) DraftLoad(ctx context.Context, key string) (map[string]any, bool, error) {
	var out DraftLoadResult
	if err := c.request(ctx, "DraftLoad", DraftLoadParams{Key: key}, &out); err != nil {
		return nil, false, err
	}
	return out.Payload, out.Found, nil
}

func (c *RemoteClient) DaemonStatus(ctx context.Context) (DaemonStatus, error) {
	var out DaemonStatus
	err := c.request(ctx, "DaemonStatus", nil, &out)
	return out, err
}

func (c *RemoteClient) Close() error {
	c.mu.Lock()
	c.manualClose = true
	c.mu.Unlock()
	c.closeWithError(ErrRemoteClosed)
	return nil
}

func (c *RemoteClient) Reconnect(ctx context.Context) error {
	if c.socketPath == "" {
		return c.closeErrOrDefault()
	}
	c.mu.Lock()
	if !c.closed {
		c.mu.Unlock()
		return nil
	}
	if c.manualClose {
		err := c.closeErrOrDefaultLocked()
		c.mu.Unlock()
		return err
	}
	c.mu.Unlock()

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if !c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	c.conn = conn
	c.pending = map[uint64]chan remoteReply{}
	c.chat = map[string][]chan ChatMessage{}
	c.events = map[string][]chan DaemonEvent{}
	c.closed = false
	c.closeErr = nil
	c.done = make(chan struct{})
	c.mu.Unlock()

	go c.readLoop()
	return nil
}

func (c *RemoteClient) request(ctx context.Context, method string, params any, result any) error {
	if err := c.ensureConnected(ctx); err != nil {
		return err
	}
	payload, err := MarshalRaw(params)
	if err != nil {
		return err
	}
	reply := make(chan remoteReply, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return c.closeErrOrDefault()
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = reply
	c.mu.Unlock()

	env := Envelope{ID: id, Kind: "request", Method: method, Params: payload}
	c.writeMu.Lock()
	err = WriteFrame(c.conn, env)
	c.writeMu.Unlock()
	if err != nil {
		c.removePending(id)
		c.closeWithError(err)
		return err
	}

	select {
	case got := <-reply:
		if got.err != nil {
			return got.err
		}
		if result == nil || len(got.result) == 0 {
			return nil
		}
		return json.Unmarshal(got.result, result)
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	case <-c.done:
		c.removePending(id)
		return c.closeErrOrDefault()
	}
}

func (c *RemoteClient) ensureConnected(ctx context.Context) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		return nil
	}
	return c.Reconnect(ctx)
}

func (c *RemoteClient) readLoop() {
	for {
		env, err := ReadFrame(c.conn)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
				c.closeWithError(ErrRemoteClosed)
				return
			}
			c.closeWithError(err)
			return
		}
		switch env.Kind {
		case "response":
			c.handleResponse(env)
		case "event":
			c.handleEvent(env)
		}
	}
}

func (c *RemoteClient) handleResponse(env Envelope) {
	reply := c.removePending(env.ID)
	if reply == nil {
		return
	}
	if env.Error != nil {
		reply <- remoteReply{err: env.Error}
		return
	}
	reply <- remoteReply{result: env.Result}
}

func (c *RemoteClient) handleEvent(env Envelope) {
	c.mu.Lock()
	eventTargets := append([]chan DaemonEvent(nil), c.events[env.Topic]...)
	c.mu.Unlock()
	for _, ch := range eventTargets {
		select {
		case ch <- DaemonEvent{Topic: env.Topic, Payload: env.Payload}:
		default:
		}
	}
	if env.Topic != "chat.message" {
		return
	}
	var msg ChatMessage
	if err := json.Unmarshal(env.Payload, &msg); err != nil || msg.Space == "" {
		return
	}
	c.mu.Lock()
	targets := append([]chan ChatMessage(nil), c.chat[msg.Space]...)
	c.mu.Unlock()
	for _, ch := range targets {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (c *RemoteClient) removePending(id uint64) chan remoteReply {
	c.mu.Lock()
	defer c.mu.Unlock()
	reply := c.pending[id]
	delete(c.pending, id)
	return reply
}

func (c *RemoteClient) removeChatSubscription(spaceName string, ch chan ChatMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	subs := c.chat[spaceName]
	for i := range subs {
		if subs[i] == ch {
			c.chat[spaceName] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(c.chat[spaceName]) == 0 {
		delete(c.chat, spaceName)
	}
}

func (c *RemoteClient) removeEventSubscription(topics []string, ch chan DaemonEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, topic := range topics {
		subs := c.events[topic]
		for i := range subs {
			if subs[i] == ch {
				c.events[topic] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(c.events[topic]) == 0 {
			delete(c.events, topic)
		}
	}
}

func (c *RemoteClient) closeWithError(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.closeErr = err
	pending := c.pending
	c.pending = map[uint64]chan remoteReply{}
	chat := c.chat
	c.chat = map[string][]chan ChatMessage{}
	events := c.events
	c.events = map[string][]chan DaemonEvent{}
	conn := c.conn
	close(c.done)
	c.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	for _, reply := range pending {
		reply <- remoteReply{err: err}
	}
	for _, subs := range chat {
		for _, ch := range subs {
			close(ch)
		}
	}
	closedEvents := map[chan DaemonEvent]bool{}
	for _, subs := range events {
		for _, ch := range subs {
			if !closedEvents[ch] {
				close(ch)
				closedEvents[ch] = true
			}
		}
	}
}

func (c *RemoteClient) closeErrOrDefault() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeErrOrDefaultLocked()
}

func (c *RemoteClient) closeErrOrDefaultLocked() error {
	if c.closeErr != nil {
		return c.closeErr
	}
	return ErrRemoteClosed
}

func ChatMessageTopic(spaceName string) string {
	return fmt.Sprintf("chat.message:%s", spaceName)
}
