package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	// ChatEvents enables real-time chat delivery via the Workspace Events API
	// when the account has a usable Google Cloud project. ChatEventsProject
	// and ChatEventsSubscription override auto-detection.
	ChatEvents             bool
	ChatEventsProject      string
	ChatEventsSubscription string
}

type Server struct {
	client api.WorkspaceClient
	opts   Options

	ctx    context.Context
	cancel context.CancelFunc

	startedAt time.Time

	mu               sync.Mutex
	nextSessionID    uint64
	sessions         map[*Session]bool
	chatCancels      map[string]context.CancelFunc
	managedChats     map[string]bool
	pinnedSpaces     map[string]bool
	autoSpaces       map[string]bool
	historyPrefetch  map[string]bool
	chatDeliveryMode string
	snapshot         api.WorkspaceSnapshot
	snapshotLoaded   bool
	cacheLock        *api.SnapshotLock
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
		client:          client,
		opts:            opts,
		startedAt:       time.Now(),
		sessions:        map[*Session]bool{},
		chatCancels:     map[string]context.CancelFunc{},
		managedChats:    map[string]bool{},
		pinnedSpaces:    pinned,
		autoSpaces:      map[string]bool{},
		historyPrefetch: map[string]bool{},
		snapshot:        snapshot,
		snapshotLoaded:  ok,
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
	// A cold snapshot load fans out across every Chat space, mailbox, and
	// calendar; on a large workspace, or when the Google APIs are slow, a tight
	// budget aborts bootstrap and leaves the daemon with no chat setup at all
	// (configureChatEvents below never runs). bootstrap runs in its own
	// goroutine, so a generous budget costs nothing but a slightly later first
	// paint on a cold start.
	ctx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
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
	s.configureChatEvents()
	s.restorePinnedSubscriptions()
	s.autoSubscribeTopSpaces()
}

// configureChatEvents resolves the chat delivery strategy (real-time Workspace
// Events stream vs. per-space polling) before any chat loop is opened, so the
// loops attach to whichever transport ends up active. The probe and project
// lookup happen here, off the request path.
func (s *Server) configureChatEvents() {
	configurer, ok := s.client.(api.ChatEventConfigurer)
	if !ok {
		return
	}
	configurer.ConfigureChatEvents(api.ChatEventOptions{
		Disabled:     !s.opts.ChatEvents,
		Project:      s.opts.ChatEventsProject,
		Subscription: s.opts.ChatEventsSubscription,
	})
	mode := configurer.PrepareChatEvents()
	s.mu.Lock()
	s.chatDeliveryMode = mode
	s.mu.Unlock()
	s.logInfo("chat delivery mode resolved", slog.String("mode", mode))
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

// autoSubscribeTopSpaces opens chat loops so notifications and unread tracking
// keep working while no TUI is attached. In real-time mode the underlying
// Workspace Events stream already covers every space, so the daemon can watch
// all spaces. In polling mode it keeps the existing top-N limit.
func (s *Server) autoSubscribeTopSpaces() {
	if !s.opts.AutoSubscribeChats || s.opts.AutoSubscribeMax <= 0 {
		return
	}
	s.mu.Lock()
	snapshot := s.snapshot.Clone()
	deliveryMode := s.chatDeliveryMode
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
		latest := space.LastActiveTime
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
	if deliveryMode == "realtime" {
		limit = len(scored)
	}
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
		started := time.Now()
		result, err := session.server.dispatch(session, env.Method, env.Params)
		if err != nil {
			session.server.logRequestError(session, env, time.Since(started), err)
		}
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
		if unread, ok := s.spaceHasUnreadLocked(name); ok {
			spaces[i].Unread = unread
		}
	}
}

// spaceHasUnreadLocked reports whether the latest message in the space was
// authored by someone other than the user AFTER the user's last-read marker.
// Caller must hold s.mu.
func (s *Server) spaceHasUnreadLocked(spaceName string) (bool, bool) {
	return api.InferChatUnread(s.snapshot, spaceName)
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
		self := api.InferSelfUserIDs(s.snapshot.Spaces, s.snapshot.MembersBySpace, s.snapshot.SelfUserIDs)
		if title := notificationSpaceLabel(space, s.snapshot.MembersBySpace[spaceName], s.snapshot.UserLabels, self); title != "" {
			return "gws chat · " + title
		}
		break
	}
	return "gws chat"
}

func notificationSpaceLabel(space api.Space, members []api.SpaceMember, labels map[string]string, self map[string]bool) string {
	if space.UsesMemberLabels() && len(members) > 0 {
		parts := make([]string, 0, len(members))
		for _, member := range members {
			if member.Type != "" && member.Type != "HUMAN" {
				continue
			}
			if isSelfUserID(member.UserID, self) {
				continue
			}
			label := labelForMember(member, labels)
			if label != "" {
				parts = append(parts, label)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, ", ")
		}
	}
	if space.UsesMemberLabels() {
		if title := stripSelfFromSpaceTitle(space.DisplayName, labels, self); title != "" {
			return title
		}
		if title := stripSelfFromSpaceTitle(space.FormattedName, labels, self); title != "" {
			return title
		}
	}
	return space.Title()
}

func labelForMember(member api.SpaceMember, labels map[string]string) string {
	key := normalizeUserKey(member.UserID)
	if key != "" {
		if label := labels[key]; label != "" && label != key {
			return label
		}
	}
	if member.DisplayName != "" && !strings.HasPrefix(member.DisplayName, "users/") {
		return member.DisplayName
	}
	return labelForUser(member.UserID, labels)
}

func labelForUser(userID string, labels map[string]string) string {
	key := normalizeUserKey(userID)
	if key == "" {
		return ""
	}
	if label := labels[key]; label != "" {
		return label
	}
	if label := labels["users/"+key]; label != "" {
		return label
	}
	return key
}

func normalizeUserKey(value string) string {
	return api.NormalizeUserID(value)
}

func isSelfUserID(value string, self map[string]bool) bool {
	key := normalizeUserKey(value)
	if key == "" {
		return false
	}
	if key == "me" {
		return true
	}
	return self[key]
}

func (s *Server) isSelfChatMessage(msg api.ChatMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	self := api.InferSelfUserIDs(s.snapshot.Spaces, s.snapshot.MembersBySpace, s.snapshot.SelfUserIDs)
	return isSelfUserID(msg.SenderID, self)
}

func stripSelfFromSpaceTitle(value string, labels map[string]string, self map[string]bool) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := splitSpaceTitleParts(value)
	if len(parts) <= 1 {
		if isSelfSpaceLabel(value, labels, self) {
			return ""
		}
		return value
	}
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || isSelfSpaceLabel(part, labels, self) {
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

func isSelfSpaceLabel(value string, labels map[string]string, self map[string]bool) bool {
	needle := normalizeSpaceLabel(value)
	if needle == "" {
		return false
	}
	for userID := range self {
		if normalizeSpaceLabel(userID) == needle {
			return true
		}
		if label := labels[userID]; normalizeSpaceLabel(label) == needle {
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

func (s *Server) markChatRead(ctx context.Context, spaceName string) error {
	if spaceName == "" {
		return errors.New("space name required")
	}
	now := time.Now()
	var err error
	if reader, ok := s.client.(api.ChatReader); ok {
		err = reader.MarkChatRead(ctx, spaceName)
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		api.MarkChatRead(snapshot, spaceName, now)
	})
	s.broadcast("chat.read", map[string]string{"space": spaceName})
	return err
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

func calendarSnapshotMonth(query api.CalendarQuery) time.Time {
	if query.TimeMin.IsZero() || query.TimeMax.IsZero() {
		return time.Time{}
	}
	return time.Date(query.TimeMin.Year(), query.TimeMin.Month(), 1, 0, 0, 0, 0, query.TimeMin.Location())
}

func (s *Server) refreshSnapshot(ctx context.Context) error {
	auth, authErr := s.client.AuthStatus(ctx)
	if authErr == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
			snapshot.Auth = auth
		})
	}
	spaces, spacesErr := s.client.ChatSpaces(ctx)
	if spacesErr == nil {
		s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
			snapshot.Spaces = spaces.Items
			api.SyncLastReadMarkersFromSpaces(snapshot, spaces.Items)
		})
		s.prefetchChatMessagesInBackground(spaces.Items)
	}
	labels, labelsErr := s.client.MailLabels(ctx)
	threads, threadsErr := s.client.MailThreads(ctx, api.MailQuery{Label: "Inbox"})
	now := time.Now()
	calendarMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	events, eventsErr := s.client.CalendarEvents(ctx, api.CalendarQuery{TimeMin: calendarMonth, TimeMax: calendarMonth.AddDate(0, 1, 0)})
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
	refreshErr := s.logRefreshErrors(
		refreshError{"auth", authErr},
		refreshError{"chat spaces", spacesErr},
		refreshError{"mail labels", labelsErr},
		refreshError{"mail inbox", threadsErr},
		refreshError{"calendar events", eventsErr},
		refreshError{"meet spaces", meetErr},
		refreshError{"task lists", taskListsErr},
		refreshError{"tasks", tasksErr},
		refreshError{"drive files", driveErr},
		refreshError{"doc files", docsErr},
		refreshError{"doc", docErr},
	)
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		if labelsErr == nil {
			snapshot.MailLabels = labels
		}
		if threadsErr == nil {
			api.ApplyMailPage(snapshot, "Inbox", threads)
		}
		if eventsErr == nil {
			snapshot.Events = events
			snapshot.CalendarMonth = calendarMonth
		}
		if meetErr == nil {
			snapshot.MeetSpaces = meet.Items
		}
		if taskListsErr == nil {
			snapshot.TaskLists = taskLists.Items
			snapshot.TaskListID = taskListID
			if tasksErr == nil {
				snapshot.Tasks = tasks
			}
		}
		if driveErr == nil {
			snapshot.DriveFiles = driveFiles
		}
		if docsErr == nil {
			snapshot.DocFiles = docFiles
			if docErr == nil {
				snapshot.Doc = doc
			}
		}
	})
	s.mu.Lock()
	hasData := s.snapshot.HasData()
	s.mu.Unlock()
	if hasData {
		return nil
	}
	return refreshErr
}

type refreshError struct {
	label string
	err   error
}

func (s *Server) logRefreshErrors(entries ...refreshError) error {
	var first error
	for _, entry := range entries {
		if entry.err == nil {
			continue
		}
		if first == nil {
			first = entry.err
		}
		s.logError("refresh "+entry.label, entry.err)
	}
	return first
}

func (s *Server) prefetchChatMessagesInBackground(spaces []api.Space) {
	names := s.claimChatHistoryPrefetches(spaces)
	if len(names) == 0 {
		return
	}
	go func() {
		const concurrency = 4
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for _, name := range names {
			if s.backgroundContext().Err() != nil {
				s.finishChatHistoryPrefetch(name)
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(name string) {
				defer wg.Done()
				defer func() { <-sem }()
				defer s.finishChatHistoryPrefetch(name)
				ctx, cancel := context.WithTimeout(s.backgroundContext(), 2*time.Minute)
				defer cancel()
				page, err := s.client.ChatMessages(ctx, name, "")
				if err != nil {
					s.logError("prefetch chat "+name, err)
					return
				}
				merged := s.applyBackgroundChatHistory(name, page)
				s.cacheAttachments(merged.Items)
				s.broadcastChatHistoryLoaded(name, merged)
			}(name)
		}
		wg.Wait()
	}()
}

func (s *Server) backgroundContext() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *Server) claimChatHistoryPrefetches(spaces []api.Space) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.historyPrefetch == nil {
		s.historyPrefetch = map[string]bool{}
	}
	names := make([]string, 0, len(spaces))
	for _, space := range spaces {
		if space.Name == "" || s.historyPrefetch[space.Name] {
			continue
		}
		s.historyPrefetch[space.Name] = true
		names = append(names, space.Name)
	}
	return names
}

func (s *Server) finishChatHistoryPrefetch(spaceName string) {
	s.mu.Lock()
	delete(s.historyPrefetch, spaceName)
	s.mu.Unlock()
}

func (s *Server) applyBackgroundChatHistory(spaceName string, page api.Page[api.ChatMessage]) api.Page[api.ChatMessage] {
	s.mu.Lock()
	merged := api.MergeChatPage(&s.snapshot, spaceName, page)
	s.snapshotLoaded = true
	snapshot := s.snapshot.Clone()
	s.mu.Unlock()
	_ = api.SaveWorkspaceSnapshot(s.opts.CachePath, snapshot)
	return merged
}

func (s *Server) broadcastChatHistoryLoaded(spaceName string, page api.Page[api.ChatMessage]) {
	if spaceName == "" {
		return
	}
	s.broadcast("chat.history.loaded", struct {
		Space string                    `json:"space"`
		Page  api.Page[api.ChatMessage] `json:"page"`
	}{Space: spaceName, Page: page})
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
		api.ApplyChatMessage(snapshot, msg)
	})
	s.cacheAttachments([]api.ChatMessage{msg})
	if fireNotify && !s.isSelfChatMessage(msg) {
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
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		api.RemoveChatMessage(snapshot, messageName)
	})
}

func (s *Server) applyChatSpace(space api.Space) {
	if space.Name == "" {
		return
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		api.ApplyChatSpace(snapshot, space)
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
	snapshotLoaded := s.snapshotLoaded
	snapshotHasData := s.snapshot.HasData()
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
		SnapshotLoaded:  snapshotLoaded,
		SnapshotHasData: snapshotHasData,
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
		api.ApplyMailThread(snapshot, thread)
	})
}

func (s *Server) applyEvent(event api.CalendarEvent) {
	if event.ID == "" {
		return
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		api.ApplyCalendarEvent(snapshot, event)
	})
}

func (s *Server) applyMeetSpace(space api.MeetSpace) {
	if space.Name == "" {
		return
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		api.ApplyMeetSpace(snapshot, space)
	})
}

func (s *Server) applyTask(task api.TaskItem) {
	if task.ID == "" {
		return
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		api.ApplyTask(snapshot, task)
	})
}

func (s *Server) removeTask(taskListID, taskID string) {
	if taskID == "" {
		return
	}
	s.updateSnapshot(func(snapshot *api.WorkspaceSnapshot) {
		api.RemoveTask(snapshot, taskListID, taskID)
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
