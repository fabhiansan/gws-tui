package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fabhiansan/gws-tui/internal/api"
	"github.com/fabhiansan/gws-tui/internal/tui/notify"
)

type Options struct {
	SocketPath         string
	CachePath          string
	DraftDir           string
	ImageCacheDir      string
	NotifyDesktop      bool
	NotifySound        bool
	NotifySoundFile    string
	AutoSubscribeChats bool
	AutoSubscribeMax   int
}

type Server struct {
	client api.WorkspaceClient
	opts   Options

	ctx    context.Context
	cancel context.CancelFunc

	startedAt time.Time

	mu             sync.Mutex
	nextSessionID  uint64
	sessions       map[*Session]bool
	chatCancels    map[string]context.CancelFunc
	managedChats   map[string]bool
	pinnedSpaces   map[string]bool
	autoSpaces     map[string]bool
	snapshot       api.WorkspaceSnapshot
	snapshotLoaded bool
	cacheLock      *api.SnapshotLock
}

type Session struct {
	server     *Server
	conn       net.Conn
	id         uint64
	pid        int
	tty        string
	attachedAt time.Time

	sendMu sync.Mutex
	mu     sync.Mutex
	topics map[string]bool
}

func NewServer(client api.WorkspaceClient, opts Options) *Server {
	snapshot, ok := api.LoadWorkspaceSnapshot(opts.CachePath)
	pinned := map[string]bool{}
	for _, name := range snapshot.PinnedSpaces {
		if name != "" {
			pinned[name] = true
		}
	}
	return &Server{
		client:         client,
		opts:           opts,
		startedAt:      time.Now(),
		sessions:       map[*Session]bool{},
		chatCancels:    map[string]context.CancelFunc{},
		managedChats:   map[string]bool{},
		pinnedSpaces:   pinned,
		autoSpaces:     map[string]bool{},
		snapshot:       snapshot,
		snapshotLoaded: ok,
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if s.opts.SocketPath == "" {
		return errors.New("socket path required")
	}
	if err := prepareSocket(s.opts.SocketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", s.opts.SocketPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.opts.SocketPath, 0o600); err != nil {
		_ = listener.Close()
		return err
	}
	defer os.Remove(s.opts.SocketPath)
	return s.Serve(ctx, listener)
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	defer s.cancel()
	defer listener.Close()
	if s.opts.CachePath != "" {
		lock, err := api.LockWorkspaceSnapshot(s.opts.CachePath)
		if err != nil {
			return err
		}
		s.cacheLock = lock
		defer s.cacheLock.Release()
	}
	go s.bootstrap()

	errCh := make(chan error, 1)
	go func() {
		<-s.ctx.Done()
		_ = listener.Close()
	}()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				errCh <- err
				return
			}
			session := s.newSession(conn)
			go session.run()
		}
	}()

	select {
	case <-s.ctx.Done():
		s.shutdownSessions()
		s.flushSnapshot()
		return nil
	case err := <-errCh:
		if s.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
			s.shutdownSessions()
			s.flushSnapshot()
			return nil
		}
		s.shutdownSessions()
		s.flushSnapshot()
		return err
	}
}

func (s *Server) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Server) bootstrap() {
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	s.mu.Lock()
	loaded := s.snapshotLoaded && s.snapshot.HasData()
	s.mu.Unlock()
	if !loaded {
		if err := s.refreshSnapshot(ctx); err != nil {
			s.logError("bootstrap snapshot", err)
			return
		}
	} else {
		// Snapshot loaded from disk — refill the image cache for any
		// attachments that were missed (e.g. a previous daemon shut down
		// mid-download). cacheAttachments is idempotent: existing files
		// are skipped via fileExists.
		s.mu.Lock()
		snapshot := s.snapshot.Clone()
		s.mu.Unlock()
		for _, page := range snapshot.ChatMessagesBySpace {
			s.cacheAttachments(page.Items)
		}
	}
	s.seedReadMarkers()
	s.restorePinnedSubscriptions()
	s.autoSubscribeTopSpaces()
}

// seedReadMarkers initializes LastReadBySpace from the latest message in each
// space. Runs once after upgrade so users don't see every space light up as
// unread the first time the daemon starts with this feature.
func (s *Server) seedReadMarkers() {
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		for spaceName, page := range snapshot.ChatMessagesBySpace {
			if _, seen := snapshot.LastReadBySpace[spaceName]; seen {
				continue
			}
			var latest time.Time
			for _, msg := range page.Items {
				if msg.CreateTime.After(latest) {
					latest = msg.CreateTime
				}
			}
			snapshot.LastReadBySpace[spaceName] = latest
		}
	})
}

// restorePinnedSubscriptions resubscribes spaces the user pinned in a previous
// session so the indicator and live notifications come back after a restart.
func (s *Server) restorePinnedSubscriptions() {
	s.mu.Lock()
	names := make([]string, 0, len(s.pinnedSpaces))
	for name := range s.pinnedSpaces {
		names = append(names, name)
	}
	s.mu.Unlock()
	for _, name := range names {
		s.addManagedChatSubscription(name)
	}
}

// autoSubscribeTopSpaces opens chat loops for the most recently active spaces
// so notifications and unread tracking keep working while no TUI is attached.
// "Most recently active" = highest CreateTime across that space's prefetched
// message page; spaces with no prefetched messages rank last.
func (s *Server) autoSubscribeTopSpaces() {
	if !s.opts.AutoSubscribeChats || s.opts.AutoSubscribeMax <= 0 {
		return
	}
	s.mu.Lock()
	snapshot := s.snapshot.Clone()
	s.mu.Unlock()
	if len(snapshot.Spaces) == 0 {
		return
	}
	type ranked struct {
		name     string
		lastSeen time.Time
	}
	scored := make([]ranked, 0, len(snapshot.Spaces))
	for _, space := range snapshot.Spaces {
		if space.Name == "" {
			continue
		}
		var latest time.Time
		if page, ok := snapshot.ChatMessagesBySpace[space.Name]; ok {
			for _, msg := range page.Items {
				if msg.CreateTime.After(latest) {
					latest = msg.CreateTime
				}
			}
		}
		scored = append(scored, ranked{name: space.Name, lastSeen: latest})
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].lastSeen.After(scored[j].lastSeen)
	})
	limit := s.opts.AutoSubscribeMax
	if limit > len(scored) {
		limit = len(scored)
	}
	s.mu.Lock()
	s.autoSpaces = map[string]bool{}
	for i := 0; i < limit; i++ {
		s.autoSpaces[scored[i].name] = true
	}
	s.mu.Unlock()
	for i := 0; i < limit; i++ {
		s.addManagedChatSubscription(scored[i].name)
	}
}

func (s *Server) newSession(conn net.Conn) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSessionID++
	session := &Session{
		server:     s,
		conn:       conn,
		id:         s.nextSessionID,
		attachedAt: time.Now(),
		topics:     map[string]bool{},
	}
	s.sessions[session] = true
	return session
}

func (session *Session) run() {
	defer session.close()
	for {
		env, err := api.ReadFrame(session.conn)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				session.server.logError("read frame", err)
			}
			return
		}
		if env.Kind != "request" {
			_ = session.sendResponse(env.ID, nil, errors.New("expected request"))
			continue
		}
		result, err := session.server.dispatch(session, env.Method, env.Params)
		_ = session.sendResponse(env.ID, result, err)
	}
}

func (session *Session) close() {
	_ = session.conn.Close()
	session.server.removeSession(session)
}

func (session *Session) sendResponse(id uint64, result any, err error) error {
	var raw json.RawMessage
	var marshalErr error
	if err == nil && result != nil {
		raw, marshalErr = api.MarshalRaw(result)
		if marshalErr != nil {
			err = marshalErr
		}
	}
	env := api.Envelope{ID: id, Kind: "response", Result: raw}
	if err != nil {
		env.Error = &api.ProtocolError{Message: err.Error()}
	}
	session.sendMu.Lock()
	defer session.sendMu.Unlock()
	return api.WriteFrame(session.conn, env)
}

func (session *Session) sendEvent(topic string, payload any) error {
	raw, err := api.MarshalRaw(payload)
	if err != nil {
		return err
	}
	session.sendMu.Lock()
	defer session.sendMu.Unlock()
	return api.WriteFrame(session.conn, api.Envelope{
		Kind:    "event",
		Topic:   topic,
		Payload: raw,
	})
}

func (s *Server) dispatch(session *Session, method string, params json.RawMessage) (any, error) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	switch method {
	case "Ping":
		return nil, nil
	case "DaemonStatus":
		return s.status(), nil
	case "Snapshot":
		return s.getSnapshot(ctx)
	case "SubscribeTopics":
		var p api.SubscribeTopicsParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		s.subscribe(session, p.Topics)
		return nil, nil
	case "ClientHello":
		var p api.ClientHelloParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		session.mu.Lock()
		session.pid = p.PID
		session.tty = p.TTY
		session.mu.Unlock()
		return nil, nil
	case "DraftSave":
		var p api.DraftSaveParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.saveDraft(p.Key, p.Payload)
	case "DraftLoad":
		var p api.DraftLoadParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		payload, ok, err := s.loadDraft(p.Key)
		return api.DraftLoadResult{Found: ok, Payload: payload}, err
	case "AuthStatus":
		status, err := s.client.AuthStatus(ctx)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.Auth = status })
			s.broadcast("auth.changed", status)
		}
		return status, err
	case "ChatSpaces":
		page, err := s.client.ChatSpaces(ctx)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.Spaces = page.Items })
			s.stampLiveOnSpaces(page.Items)
		}
		return page, err
	case "ChatMessages":
		var p api.ChatMessagesParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		page, err := s.client.ChatMessages(ctx, p.SpaceName, p.PageToken)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
				snapshot.ChatMessagesBySpace[p.SpaceName] = page
			})
			s.cacheAttachments(page.Items)
			s.broadcast("chat.refreshed", map[string]string{"space": p.SpaceName})
		}
		return page, err
	case "SendChatMessage":
		var p api.SendChatMessageParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		msg, err := s.client.SendChatMessage(ctx, p.SpaceName, p.Text, p.ThreadID, p.Attachments)
		if err == nil {
			s.handleChatMessage(msg, false)
		}
		return msg, err
	case "EditChatMessage":
		var p api.EditChatMessageParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		msg, err := s.client.EditChatMessage(ctx, p.MessageName, p.Text)
		if err == nil {
			if msg.Space == "" {
				msg.Space = chatSpaceFromMessageName(p.MessageName)
			}
			s.handleChatMessage(msg, false)
		}
		return msg, err
	case "DeleteChatMessage":
		var p api.ChatMessageNameParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		err := s.client.DeleteChatMessage(ctx, p.MessageName)
		if err == nil {
			s.removeChatMessage(p.MessageName)
			s.broadcast("chat.message.deleted", map[string]string{"name": p.MessageName})
		}
		return nil, err
	case "CreateChatSpace":
		var p api.CreateChatSpaceParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		space, err := s.client.CreateChatSpace(ctx, p.DisplayName)
		if err == nil {
			s.applyChatSpace(space)
			s.broadcast("chat.space", space)
		}
		return space, err
	case "SetupChatSpace":
		var p api.SetupChatSpaceParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		space, err := s.client.SetupChatSpace(ctx, p.DisplayName, p.Members)
		if err == nil {
			s.applyChatSpace(space)
			s.broadcast("chat.space", space)
		}
		return space, err
	case "AddChatReaction":
		var p api.ChatReactionParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		name, err := s.client.AddChatReaction(ctx, p.MessageName, p.Emoji)
		if err == nil {
			s.broadcast("chat.reaction", map[string]string{"name": name, "message": p.MessageName, "emoji": p.Emoji, "action": "added"})
		}
		return name, err
	case "DeleteChatReaction":
		var p api.ChatReactionParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		err := s.client.DeleteChatReaction(ctx, p.ReactionName)
		if err == nil {
			s.broadcast("chat.reaction", map[string]string{"name": p.ReactionName, "action": "deleted"})
		}
		return nil, err
	case "ChatMembers":
		var p api.SpaceNameParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		members, err := s.client.ChatMembers(ctx, p.SpaceName)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
				snapshot.MembersBySpace[p.SpaceName] = members
			})
		}
		return members, err
	case "PeopleGet":
		var p api.UserIDParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		person, err := s.client.PeopleGet(ctx, p.UserID)
		if err == nil && person.UserID != "" {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
				if person.DisplayName != "" {
					snapshot.UserLabels[person.UserID] = person.DisplayName
				}
				if person.UserID == "me" {
					snapshot.SelfUserIDs[person.UserID] = true
				}
			})
		}
		return person, err
	case "DownloadAttachment":
		var p api.DownloadAttachmentParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.downloadAttachment(ctx, p.Attachment, p.OutputPath)
	case "MailLabels":
		labels, err := s.client.MailLabels(ctx)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.MailLabels = labels })
		}
		return labels, err
	case "MailThreads":
		var p api.MailThreadsParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		page, err := s.client.MailThreads(ctx, p.Query)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.MailThreads = page })
		}
		return page, err
	case "SendMail":
		var p api.SendMailParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		thread, err := s.client.SendMail(ctx, p.Draft)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
				snapshot.MailThreads.Items = append([]api.MailThread{thread}, snapshot.MailThreads.Items...)
			})
			s.broadcast("mail.changed", thread)
		}
		return thread, err
	case "MailDrafts":
		var p api.MailDraftsParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		page, err := s.client.MailDrafts(ctx, p.PageToken)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.MailDrafts = page })
		}
		return page, err
	case "CreateMailDraft":
		var p api.SendMailParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		draft, err := s.client.CreateMailDraft(ctx, p.Draft)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
				snapshot.MailDrafts.Items = append([]api.MailDraftItem{draft}, snapshot.MailDrafts.Items...)
			})
			s.broadcast("mail.changed", map[string]string{"draft_id": draft.ID, "action": "draft_created"})
		}
		return draft, err
	case "SendMailDraft":
		var p api.MailDraftIDParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		thread, err := s.client.SendMailDraft(ctx, p.DraftID)
		if err == nil {
			s.applyMailThread(thread)
			s.broadcast("mail.changed", thread)
		}
		return thread, err
	case "ArchiveMail":
		var p api.ThreadIDParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		err := s.client.ArchiveMail(ctx, p.ThreadID)
		if err == nil {
			s.broadcast("mail.changed", map[string]string{"thread_id": p.ThreadID, "action": "archived"})
		}
		return nil, err
	case "TrashMail":
		var p api.ThreadIDParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		err := s.client.TrashMail(ctx, p.ThreadID)
		if err == nil {
			s.broadcast("mail.changed", map[string]string{"thread_id": p.ThreadID, "action": "trashed"})
		}
		return nil, err
	case "ToggleStar":
		var p api.ThreadIDParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		thread, err := s.client.ToggleStar(ctx, p.ThreadID)
		if err == nil {
			s.applyMailThread(thread)
			s.broadcast("mail.changed", thread)
		}
		return thread, err
	case "SetMailUnread":
		var p api.SetMailUnreadParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		thread, err := s.client.SetMailUnread(ctx, p.ThreadID, p.Unread)
		if err == nil {
			s.applyMailThread(thread)
			s.broadcast("mail.changed", thread)
		}
		return thread, err
	case "CalendarLists":
		page, err := s.client.CalendarLists(ctx)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.CalendarLists = page.Items })
		}
		return page, err
	case "CalendarEvents":
		var p api.CalendarEventsParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		page, err := s.client.CalendarEvents(ctx, p.Query)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
				snapshot.Events = page
				snapshot.CalendarID = p.Query.CalendarID
			})
		}
		return page, err
	case "QuickAddEvent":
		var p api.QuickAddEventParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		event, err := s.client.QuickAddEvent(ctx, p.Text)
		if err == nil {
			s.applyEvent(event)
			s.broadcast("calendar.changed", event)
		}
		return event, err
	case "CreateEvent":
		var p api.CreateEventParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		event, err := s.client.CreateEvent(ctx, p.Draft)
		if err == nil {
			s.applyEvent(event)
			s.broadcast("calendar.changed", event)
		}
		return event, err
	case "UpdateEvent":
		var p api.UpdateEventParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		event, err := s.client.UpdateEvent(ctx, p.EventID, p.Draft)
		if err == nil {
			s.applyEvent(event)
			s.broadcast("calendar.changed", event)
		}
		return event, err
	case "MoveEvent":
		var p api.MoveEventParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		event, err := s.client.MoveEvent(ctx, p.EventID, p.SourceCalendarID, p.DestinationCalendarID)
		if err == nil {
			s.applyEvent(event)
			s.broadcast("calendar.changed", event)
		}
		return event, err
	case "RSVPEvent":
		var p api.RSVPEventParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		event, err := s.client.RSVPEvent(ctx, p.EventID, p.Response)
		if err == nil {
			s.applyEvent(event)
			s.broadcast("calendar.changed", event)
		}
		return event, err
	case "DeleteEvent":
		var p api.EventIDParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		err := s.client.DeleteEvent(ctx, p.EventID)
		if err == nil {
			s.broadcast("calendar.changed", map[string]string{"event_id": p.EventID, "action": "deleted"})
		}
		return nil, err
	case "MeetSpaces":
		page, err := s.client.MeetSpaces(ctx)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.MeetSpaces = page.Items })
		}
		return page, err
	case "CreateMeetSpace":
		var p api.CreateMeetSpaceParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		space, err := s.client.CreateMeetSpace(ctx, p.Title)
		if err == nil {
			s.applyMeetSpace(space)
			s.broadcast("meet.changed", space)
		}
		return space, err
	case "EndMeetSpace":
		var p api.MeetSpaceNameParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		err := s.client.EndMeetSpace(ctx, p.Name)
		if err == nil {
			s.broadcast("meet.changed", map[string]string{"name": p.Name, "action": "ended"})
		}
		return nil, err
	case "TaskLists":
		page, err := s.client.TaskLists(ctx)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.TaskLists = page.Items })
		}
		return page, err
	case "Tasks":
		var p api.TasksParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		page, err := s.client.Tasks(ctx, p.Query)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
				snapshot.Tasks = page
				snapshot.TaskListID = p.Query.TaskListID
			})
		}
		return page, err
	case "DriveFiles":
		var p api.DriveFilesParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		page, err := s.client.DriveFiles(ctx, p.Query)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.DriveFiles = page })
		}
		return page, err
	case "Docs":
		var p api.DriveFilesParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		page, err := s.client.Docs(ctx, p.Query)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.DocFiles = page })
		}
		return page, err
	case "Doc":
		var p api.DocIDParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		doc, err := s.client.Doc(ctx, p.DocumentID)
		if err == nil {
			s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) { snapshot.Doc = doc })
		}
		return doc, err
	case "PinChatSpace":
		var p api.SpaceNameParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.pinChatSpace(p.SpaceName)
	case "UnpinChatSpace":
		var p api.SpaceNameParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.unpinChatSpace(p.SpaceName)
	case "MarkChatRead":
		var p api.SpaceNameParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.markChatRead(p.SpaceName)
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func (s *Server) getSnapshot(ctx context.Context) (api.WorkspaceSnapshot, error) {
	s.mu.Lock()
	loaded := s.snapshotLoaded && s.snapshot.HasData()
	if loaded {
		s.snapshot.EnsureMaps()
		snapshot := s.snapshot.Clone()
		s.stampLiveLocked(snapshot.Spaces)
		s.mu.Unlock()
		return snapshot, nil
	}
	s.mu.Unlock()
	if err := s.refreshSnapshot(ctx); err != nil {
		return api.WorkspaceSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.EnsureMaps()
	snapshot := s.snapshot.Clone()
	s.stampLiveLocked(snapshot.Spaces)
	return snapshot, nil
}

// stampLiveOnSpaces marks Space.Live=true for every space the daemon is
// currently holding a chat loop open for. This is what surfaces the "circle"
// indicator in the TUI after a reconnect.
func (s *Server) stampLiveOnSpaces(spaces []api.Space) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stampLiveLocked(spaces)
}

func (s *Server) stampLiveLocked(spaces []api.Space) {
	for i := range spaces {
		name := spaces[i].Name
		if s.managedChats[name] {
			spaces[i].Live = true
		}
		spaces[i].Unread = s.spaceHasUnreadLocked(name)
	}
}

// spaceHasUnreadLocked reports whether the latest message in the space was
// authored by someone other than the user AFTER the user's last-read marker.
// Caller must hold s.mu.
func (s *Server) spaceHasUnreadLocked(spaceName string) bool {
	if spaceName == "" {
		return false
	}
	page, ok := s.snapshot.ChatMessagesBySpace[spaceName]
	if !ok || len(page.Items) == 0 {
		return false
	}
	lastRead := s.snapshot.LastReadBySpace[spaceName]
	for i := len(page.Items) - 1; i >= 0; i-- {
		msg := page.Items[i]
		// SenderID arrives as "users/<id>" but SelfUserIDs is keyed by the
		// bare numeric id, so strip the resource prefix before the lookup.
		bareID := api.UserIDFromName(msg.SenderID)
		if bareID != "" && s.snapshot.SelfUserIDs[bareID] {
			continue
		}
		if msg.CreateTime.After(lastRead) {
			return true
		}
	}
	return false
}

func (s *Server) pinChatSpace(spaceName string) error {
	if spaceName == "" {
		return errors.New("space name required")
	}
	s.mu.Lock()
	if s.pinnedSpaces[spaceName] {
		s.mu.Unlock()
		return nil
	}
	s.pinnedSpaces[spaceName] = true
	s.mu.Unlock()
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		snapshot.PinnedSpaces = appendUnique(snapshot.PinnedSpaces, spaceName)
	})
	s.addManagedChatSubscription(spaceName)
	s.broadcast("chat.pinned", map[string]string{"space": spaceName, "action": "pinned"})
	return nil
}

func (s *Server) unpinChatSpace(spaceName string) error {
	if spaceName == "" {
		return errors.New("space name required")
	}
	s.mu.Lock()
	if !s.pinnedSpaces[spaceName] {
		s.mu.Unlock()
		return nil
	}
	delete(s.pinnedSpaces, spaceName)
	// If the auto-subscribe ranker also wants this space, leave the loop
	// running; otherwise tear it down so we stop hitting the upstream.
	if !s.autoSpaces[spaceName] {
		delete(s.managedChats, spaceName)
		if cancel := s.chatCancels[spaceName]; cancel != nil {
			// Also confirm no session is still subscribed before cancelling.
			stillWanted := false
			for sess := range s.sessions {
				sess.mu.Lock()
				if sess.topics[api.ChatMessageTopic(spaceName)] {
					stillWanted = true
				}
				sess.mu.Unlock()
				if stillWanted {
					break
				}
			}
			if !stillWanted {
				cancel()
				delete(s.chatCancels, spaceName)
			}
		}
	}
	s.mu.Unlock()
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		snapshot.PinnedSpaces = removeString(snapshot.PinnedSpaces, spaceName)
	})
	s.broadcast("chat.pinned", map[string]string{"space": spaceName, "action": "unpinned"})
	return nil
}

// resolveSenderName converts a sender resource name (e.g. "users/115178...")
// into a human display name, consulting the in-memory UserLabels cache first
// and falling back to a synchronous PeopleGet lookup. Result is cached.
// Returns "" if no lookup was needed or no better name could be found.
func (s *Server) resolveSenderName(msg api.ChatMessage) string {
	senderID := msg.SenderID
	if senderID == "" {
		return ""
	}
	// If the upstream already gave us a real display name (anything other
	// than the raw resource name), trust it.
	if msg.SenderName != "" && msg.SenderName != senderID {
		return msg.SenderName
	}
	// UserLabels and SelfUserIDs are keyed by the bare numeric id (e.g.
	// "115178..."), while msg.SenderID arrives with the "users/" prefix.
	// Normalize before any cache lookup or write, otherwise we always miss
	// the cache, fall through to PeopleGet on every message, and never see
	// labels that the TUI already resolved.
	bareID := api.UserIDFromName(senderID)
	s.mu.Lock()
	if s.snapshot.UserLabels != nil {
		if cached, ok := s.snapshot.UserLabels[bareID]; ok && cached != "" && cached != bareID {
			s.mu.Unlock()
			return cached
		}
	}
	s.mu.Unlock()
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	lookupCtx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	person, err := s.client.PeopleGet(lookupCtx, bareID)
	if err != nil {
		return ""
	}
	name := person.DisplayName
	if name == "" {
		name = person.Email
	}
	if name == "" || name == bareID {
		return ""
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		snapshot.UserLabels[bareID] = name
	})
	return name
}

// notifyTitleForSpace builds a desktop notification title that includes the
// space's display name when known, falling back to "gws chat".
func (s *Server) notifyTitleForSpace(spaceName string) string {
	if spaceName == "" {
		return "gws chat"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, space := range s.snapshot.Spaces {
		if space.Name != spaceName {
			continue
		}
		if title := space.Title(); title != "" {
			return "gws chat · " + title
		}
		break
	}
	return "gws chat"
}

func (s *Server) markChatRead(spaceName string) error {
	if spaceName == "" {
		return errors.New("space name required")
	}
	now := time.Now()
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		snapshot.LastReadBySpace[spaceName] = now
	})
	s.broadcast("chat.read", map[string]string{"space": spaceName})
	return nil
}

func appendUnique(slice []string, value string) []string {
	for _, v := range slice {
		if v == value {
			return slice
		}
	}
	return append(slice, value)
}

func removeString(slice []string, value string) []string {
	for i, v := range slice {
		if v == value {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

func (s *Server) refreshSnapshot(ctx context.Context) error {
	auth, authErr := s.client.AuthStatus(ctx)
	spaces, spacesErr := s.client.ChatSpaces(ctx)
	labels, labelsErr := s.client.MailLabels(ctx)
	threads, threadsErr := s.client.MailThreads(ctx, api.MailQuery{Label: "Inbox"})
	events, eventsErr := s.client.CalendarEvents(ctx, api.CalendarQuery{})
	meet, meetErr := s.client.MeetSpaces(ctx)
	taskLists, taskListsErr := s.client.TaskLists(ctx)
	tasks := api.Page[api.TaskItem]{}
	taskListID := ""
	var tasksErr error
	if len(taskLists.Items) > 0 {
		taskListID = taskLists.Items[0].ID
		tasks, tasksErr = s.client.Tasks(ctx, api.TaskQuery{TaskListID: taskListID})
	}
	driveFiles, driveErr := s.client.DriveFiles(ctx, api.DriveQuery{})
	docFiles, docsErr := s.client.Docs(ctx, api.DriveQuery{})
	doc := api.DocDocument{}
	var docErr error
	if len(docFiles.Items) > 0 {
		doc, docErr = s.client.Doc(ctx, docFiles.Items[0].ID)
	}
	if err := firstErr(authErr, spacesErr, labelsErr, threadsErr, eventsErr, meetErr, taskListsErr, tasksErr, driveErr, docsErr, docErr); err != nil {
		return err
	}
	messagesBySpace := s.prefetchChatMessages(ctx, spaces.Items)
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		snapshot.Auth = auth
		snapshot.Spaces = spaces.Items
		for name, page := range messagesBySpace {
			snapshot.ChatMessagesBySpace[name] = page
		}
		snapshot.MailLabels = labels
		snapshot.MailThreads = threads
		snapshot.Events = events
		snapshot.MeetSpaces = meet.Items
		snapshot.TaskLists = taskLists.Items
		snapshot.Tasks = tasks
		snapshot.TaskListID = taskListID
		snapshot.DriveFiles = driveFiles
		snapshot.DocFiles = docFiles
		snapshot.Doc = doc
	})
	for _, page := range messagesBySpace {
		s.cacheAttachments(page.Items)
	}
	return nil
}

func (s *Server) prefetchChatMessages(ctx context.Context, spaces []api.Space) map[string]api.Page[api.ChatMessage] {
	const concurrency = 4
	results := make(map[string]api.Page[api.ChatMessage])
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for _, space := range spaces {
		if space.Name == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(name string) {
			defer wg.Done()
			defer func() { <-sem }()
			page, err := s.client.ChatMessages(ctx, name, "")
			if err != nil {
				s.logError("prefetch chat "+name, err)
				return
			}
			mu.Lock()
			results[name] = page
			mu.Unlock()
		}(space.Name)
	}
	wg.Wait()
	return results
}

func (s *Server) subscribe(session *Session, topics []string) {
	session.mu.Lock()
	defer session.mu.Unlock()
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" || session.topics[topic] {
			continue
		}
		session.topics[topic] = true
		if spaceName, ok := chatSpaceFromTopic(topic); ok {
			s.addChatSubscription(spaceName)
		}
	}
}

func (s *Server) removeSession(session *Session) {
	s.mu.Lock()
	delete(s.sessions, session)
	// Cancel chat loops for spaces no remaining session subscribes to AND
	// that aren't daemon-managed (auto-subscribed). Daemon-managed loops
	// keep running so notifications and unread tracking persist while the
	// TUI is closed.
	wanted := make(map[string]bool)
	for sess := range s.sessions {
		sess.mu.Lock()
		for topic := range sess.topics {
			if spaceName, ok := chatSpaceFromTopic(topic); ok {
				wanted[spaceName] = true
			}
		}
		sess.mu.Unlock()
	}
	for spaceName, cancel := range s.chatCancels {
		if wanted[spaceName] || s.managedChats[spaceName] {
			continue
		}
		cancel()
		delete(s.chatCancels, spaceName)
	}
	s.mu.Unlock()
}

func (s *Server) addChatSubscription(spaceName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureChatLoopLocked(spaceName)
}

func (s *Server) addManagedChatSubscription(spaceName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.managedChats[spaceName] = true
	s.ensureChatLoopLocked(spaceName)
}

func (s *Server) ensureChatLoopLocked(spaceName string) {
	if s.chatCancels[spaceName] != nil {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.chatCancels[spaceName] = cancel
	go s.chatLoop(ctx, spaceName)
}

func (s *Server) chatLoop(ctx context.Context, spaceName string) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		ch, err := s.client.SubscribeChat(ctx, spaceName)
		if err != nil {
			if !sleepContext(ctx, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			}
			continue
		}
		backoff = time.Second
		for msg := range ch {
			s.handleChatMessage(msg, true)
		}
		if !sleepContext(ctx, 100*time.Millisecond) {
			return
		}
	}
}

func (s *Server) handleChatMessage(msg api.ChatMessage, fireNotify bool) {
	if msg.ID == "" || msg.Space == "" {
		return
	}
	// Resolve sender ID -> display name before persisting & broadcasting so
	// the TUI's chat view and the desktop notification both show the real
	// name instead of "users/115178...". Mutates msg in place so the same
	// resolved name flows to the snapshot, the broadcast, and the notify.
	if resolved := s.resolveSenderName(msg); resolved != "" {
		msg.SenderName = resolved
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		page := snapshot.ChatMessagesBySpace[msg.Space]
		page.Items = upsertChatMessage(page.Items, msg)
		snapshot.ChatMessagesBySpace[msg.Space] = page
	})
	s.cacheAttachments([]api.ChatMessage{msg})
	if fireNotify {
		title := s.notifyTitleForSpace(msg.Space)
		body := strings.TrimSpace(msg.SenderName + ": " + msg.Text)
		notify.Send(title, body, notify.Options{
			Desktop:   s.opts.NotifyDesktop,
			Sound:     s.opts.NotifySound,
			SoundFile: s.opts.NotifySoundFile,
		})
		s.broadcast("notify", map[string]string{
			"title": title,
			"body":  body,
			"space": msg.Space,
		})
	}
	s.broadcast("chat.message", msg)
}

func (s *Server) removeChatMessage(messageName string) {
	if messageName == "" {
		return
	}
	spaceName := chatSpaceFromMessageName(messageName)
	messageID := chatMessageIDFromName(messageName)
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		removeFromPage := func(page api.Page[api.ChatMessage]) api.Page[api.ChatMessage] {
			out := page.Items[:0]
			for _, msg := range page.Items {
				if msg.Name == messageName || (messageID != "" && msg.ID == messageID) {
					continue
				}
				out = append(out, msg)
			}
			page.Items = out
			return page
		}
		if spaceName != "" {
			if page, ok := snapshot.ChatMessagesBySpace[spaceName]; ok {
				snapshot.ChatMessagesBySpace[spaceName] = removeFromPage(page)
			}
			return
		}
		for name, page := range snapshot.ChatMessagesBySpace {
			snapshot.ChatMessagesBySpace[name] = removeFromPage(page)
		}
	})
}

func (s *Server) applyChatSpace(space api.Space) {
	if space.Name == "" {
		return
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		for i := range snapshot.Spaces {
			if snapshot.Spaces[i].Name == space.Name {
				snapshot.Spaces[i] = space
				return
			}
		}
		snapshot.Spaces = append([]api.Space{space}, snapshot.Spaces...)
	})
}

func chatSpaceFromMessageName(name string) string {
	if idx := strings.Index(name, "/messages/"); idx > 0 {
		return name[:idx]
	}
	return ""
}

func chatMessageIDFromName(name string) string {
	if idx := strings.LastIndex(name, "/messages/"); idx >= 0 {
		return name[idx+len("/messages/"):]
	}
	return ""
}

func (s *Server) broadcast(topic string, payload any) {
	s.mu.Lock()
	sessions := make([]*Session, 0, len(s.sessions))
	for session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.mu.Unlock()
	for _, session := range sessions {
		if !session.wants(topic, payload) {
			continue
		}
		_ = session.sendEvent(topic, payload)
	}
}

func (session *Session) wants(topic string, payload any) bool {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.topics[topic] {
		return true
	}
	if topic == "chat.message" {
		if msg, ok := payload.(api.ChatMessage); ok {
			return session.topics[api.ChatMessageTopic(msg.Space)]
		}
	}
	return false
}

func (s *Server) status() api.DaemonStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	clients := make([]api.ClientInfo, 0, len(s.sessions))
	for session := range s.sessions {
		session.mu.Lock()
		pid := session.pid
		tty := session.tty
		topics := make([]string, 0, len(session.topics))
		for topic := range session.topics {
			topics = append(topics, topic)
		}
		session.mu.Unlock()
		sort.Strings(topics)
		clients = append(clients, api.ClientInfo{
			ID:         session.id,
			PID:        pid,
			TTY:        tty,
			AttachedAt: session.attachedAt,
			Topics:     topics,
		})
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].ID < clients[j].ID })
	return api.DaemonStatus{
		ProtocolVersion: api.ProtocolVersion,
		PID:             os.Getpid(),
		SocketPath:      s.opts.SocketPath,
		UptimeSeconds:   int64(time.Since(s.startedAt).Seconds()),
		Clients:         clients,
	}
}

func (s *Server) updateSnapshot(update func(*api.WorkspaceSnapshot)) {
	s.mu.Lock()
	s.snapshot.EnsureMaps()
	update(&s.snapshot)
	s.snapshotLoaded = true
	snapshot := s.snapshot.Clone()
	s.mu.Unlock()
	_ = api.SaveWorkspaceSnapshot(s.opts.CachePath, snapshot)
}

func (s *Server) flushSnapshot() {
	s.mu.Lock()
	loaded := s.snapshotLoaded
	var snapshot api.WorkspaceSnapshot
	if loaded {
		snapshot = s.snapshot.Clone()
	}
	s.mu.Unlock()
	if loaded {
		_ = api.SaveWorkspaceSnapshot(s.opts.CachePath, snapshot)
	}
}

func (s *Server) shutdownSessions() {
	s.mu.Lock()
	sessions := make([]*Session, 0, len(s.sessions))
	for session := range s.sessions {
		sessions = append(sessions, session)
	}
	cancels := make([]context.CancelFunc, 0, len(s.chatCancels))
	for _, cancel := range s.chatCancels {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	for _, session := range sessions {
		_ = session.conn.Close()
	}
}

func (s *Server) applyMailThread(thread api.MailThread) {
	if thread.ID == "" {
		return
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		for i := range snapshot.MailThreads.Items {
			if snapshot.MailThreads.Items[i].ID == thread.ID {
				snapshot.MailThreads.Items[i] = thread
				return
			}
		}
		snapshot.MailThreads.Items = append([]api.MailThread{thread}, snapshot.MailThreads.Items...)
	})
}

func (s *Server) applyEvent(event api.CalendarEvent) {
	if event.ID == "" {
		return
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		for i := range snapshot.Events.Items {
			if snapshot.Events.Items[i].ID == event.ID {
				snapshot.Events.Items[i] = event
				return
			}
		}
		snapshot.Events.Items = append(snapshot.Events.Items, event)
	})
}

func (s *Server) applyMeetSpace(space api.MeetSpace) {
	if space.Name == "" {
		return
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		for i := range snapshot.MeetSpaces {
			if snapshot.MeetSpaces[i].Name == space.Name {
				snapshot.MeetSpaces[i] = space
				return
			}
		}
		snapshot.MeetSpaces = append([]api.MeetSpace{space}, snapshot.MeetSpaces...)
	})
}

func (s *Server) cacheAttachments(messages []api.ChatMessage) {
	if s.opts.ImageCacheDir == "" {
		return
	}
	for _, msg := range messages {
		for _, attachment := range api.NormalizeAttachments(msg.Attachments) {
			if !attachment.IsImage() {
				continue
			}
			outputPath := attachment.CachePath(s.opts.ImageCacheDir)
			if outputPath == "" || fileExists(outputPath) {
				continue
			}
			go func(attachment api.Attachment, outputPath string) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := s.downloadAttachment(ctx, attachment, outputPath); err == nil {
					s.broadcast("image.cached", map[string]string{
						"source": attachment.PreviewSource(),
						"path":   outputPath,
					})
				}
			}(attachment, outputPath)
		}
	}
}

func (s *Server) downloadAttachment(ctx context.Context, attachment api.Attachment, outputPath string) error {
	if outputPath == "" {
		return errors.New("output path required")
	}
	if attachment.MediaResourceName() != "" {
		return s.client.DownloadAttachment(ctx, attachment, outputPath)
	}
	source := attachment.PreviewSource()
	if !isHTTPURL(source) {
		return fmt.Errorf("unsupported image source: %s", source)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gws-tui-daemon")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("image request failed: %s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(outputPath), ".gws-image-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 10<<20)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, outputPath)
}

func (s *Server) saveDraft(key string, payload map[string]any) error {
	if s.opts.DraftDir == "" {
		return errors.New("draft dir not configured")
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("draft key required")
	}
	if err := os.MkdirAll(s.opts.DraftDir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.opts.DraftDir, safeDraftName(key)+".json"), append(raw, '\n'), 0o600)
}

func (s *Server) loadDraft(key string) (map[string]any, bool, error) {
	if s.opts.DraftDir == "" || strings.TrimSpace(key) == "" {
		return nil, false, nil
	}
	raw, err := os.ReadFile(filepath.Join(s.opts.DraftDir, safeDraftName(key)+".json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false, err
	}
	return payload, true, nil
}

func (s *Server) logError(label string, err error) {
	if err != nil && s.ctx != nil && s.ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "gws daemon: %s: %v\n", label, err)
	}
}

func prepareSocket(socketPath string) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", socketPath, 150*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("daemon already running at %s", socketPath)
	}
	if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "connection refused") {
		_ = os.Remove(socketPath)
	}
	return nil
}

func decode(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func upsertChatMessage(items []api.ChatMessage, msg api.ChatMessage) []api.ChatMessage {
	for i := range items {
		if items[i].ID == msg.ID {
			items[i] = msg
			return items
		}
	}
	items = append(items, msg)
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreateTime.Before(items[j].CreateTime)
	})
	return items
}

func chatSpaceFromTopic(topic string) (string, bool) {
	spaceName, ok := strings.CutPrefix(topic, "chat.message:")
	return spaceName, ok && spaceName != ""
}

func safeDraftName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func isHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
