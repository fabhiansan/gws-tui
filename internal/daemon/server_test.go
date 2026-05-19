package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fabhiansan/gws-tui/internal/api"
)

func TestServerFansOutSingleChatSubscriptionToMultipleClients(t *testing.T) {
	dir := t.TempDir()
	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := &pushChatClient{
		WorkspaceClient: newTestWorkspaceClient(),
		events:          make(chan api.ChatMessage, 1),
		started:         make(chan struct{}),
	}
	server := NewServer(backend, Options{
		SocketPath: socketPath,
		CachePath:  filepath.Join(dir, "cache.json"),
		DraftDir:   filepath.Join(dir, "drafts"),
	})
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()

	clientA, err := api.NewRemoteClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer clientA.Close()
	clientB, err := api.NewRemoteClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer clientB.Close()

	chA, err := clientA.SubscribeChat(ctx, "spaces/engineering")
	if err != nil {
		t.Fatal(err)
	}
	chB, err := clientB.SubscribeChat(ctx, "spaces/engineering")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("server did not start backend chat subscription")
	}

	backend.events <- api.ChatMessage{
		ID:         "live-1",
		Space:      "spaces/engineering",
		SenderName: "Alice",
		Text:       "hello from daemon",
		CreateTime: time.Now(),
	}

	assertChatEvent(t, chA, "live-1")
	assertChatEvent(t, chB, "live-1")
	if backend.SubscribeCalls("spaces/engineering") != 1 {
		t.Fatalf("expected one backend subscription loop for engineering, got %d", backend.SubscribeCalls("spaces/engineering"))
	}

	status, err := clientA.DaemonStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Clients) != 2 {
		t.Fatalf("expected two clients in status, got %#v", status.Clients)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestServerSnapshotAndDraftRoundTrip(t *testing.T) {
	dir := t.TempDir()
	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewServer(newTestWorkspaceClient(), Options{
		SocketPath: socketPath,
		CachePath:  filepath.Join(dir, "cache.json"),
		DraftDir:   filepath.Join(dir, "drafts"),
	})
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()

	client, err := api.NewRemoteClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ProtocolVersion != api.ProtocolVersion || len(snapshot.Spaces) == 0 || len(snapshot.MailThreads.Items) == 0 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if _, err := os.Stat(filepath.Join(dir, "cache.json")); err != nil {
		t.Fatalf("snapshot should be persisted to cache: %v", err)
	}

	if err := client.DraftSave(ctx, "client-1:mail:thread-1", map[string]any{"body": "draft body"}); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := client.DraftLoad(ctx, "client-1:mail:thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || loaded["body"] != "draft body" {
		t.Fatalf("unexpected draft: found=%v payload=%#v", ok, loaded)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestServerDoesNotStartLiveSubscriptionsWithoutClients(t *testing.T) {
	dir := t.TempDir()
	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := &pushChatClient{
		WorkspaceClient: newTestWorkspaceClient(),
		events:          make(chan api.ChatMessage, 1),
		started:         make(chan struct{}),
	}
	server := NewServer(backend, Options{
		SocketPath: socketPath,
		CachePath:  filepath.Join(dir, "cache.json"),
	})
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()

	select {
	case <-backend.started:
		t.Fatal("server started chat subscription without any connected clients")
	case <-time.After(250 * time.Millisecond):
	}
	if calls := backend.SubscribeCalls("spaces/engineering"); calls != 0 {
		t.Fatalf("expected no backend subscription before clients connect, got %d", calls)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestServerStopsPollingWhenLastSubscriberDisconnects(t *testing.T) {
	dir := t.TempDir()
	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := &pushChatClient{
		WorkspaceClient: newTestWorkspaceClient(),
		events:          make(chan api.ChatMessage, 1),
		started:         make(chan struct{}),
	}
	server := NewServer(backend, Options{
		SocketPath: socketPath,
		CachePath:  filepath.Join(dir, "cache.json"),
	})
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()

	client, err := api.NewRemoteClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubscribeChat(ctx, "spaces/engineering"); err != nil {
		t.Fatal(err)
	}
	waitForSubscription(t, backend, "spaces/engineering")

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return len(server.chatCancels) == 0
	}, "chat loop was not cancelled after client disconnected")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestServerAutoSubscribesTopSpacesAndSurvivesClientDisconnect(t *testing.T) {
	dir := t.TempDir()
	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := &pushChatClient{
		WorkspaceClient: newTestWorkspaceClient(),
		events:          make(chan api.ChatMessage),
		started:         make(chan struct{}),
	}
	server := NewServer(backend, Options{
		SocketPath:         socketPath,
		CachePath:          filepath.Join(dir, "cache.json"),
		AutoSubscribeChats: true,
		AutoSubscribeMax:   2,
	})
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()

	// Top 2 test spaces by most-recent message are spaces/alice
	// (DM, 35min ago) and spaces/engineering (100min ago). Bootstrap
	// should subscribe both.
	waitFor(t, 2*time.Second, func() bool {
		return backend.SubscribeCalls("spaces/alice") >= 1 &&
			backend.SubscribeCalls("spaces/engineering") >= 1
	}, "bootstrap did not auto-subscribe top spaces")

	// design and random are below the top-2 cutoff and must not be
	// auto-subscribed.
	if backend.SubscribeCalls("spaces/design") != 0 {
		t.Fatalf("spaces/design should not be auto-subscribed, got %d calls", backend.SubscribeCalls("spaces/design"))
	}

	// Attach a client, then close it. The auto-subscribed loops must
	// survive because they're daemon-managed, not session-driven.
	client, err := api.NewRemoteClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	// Give removeSession a beat to run, then assert both managed loops
	// are still alive.
	time.Sleep(100 * time.Millisecond)
	server.mu.Lock()
	_, aliceAlive := server.chatCancels["spaces/alice"]
	_, engAlive := server.chatCancels["spaces/engineering"]
	server.mu.Unlock()
	if !aliceAlive || !engAlive {
		t.Fatalf("managed loops were cancelled after client disconnect: alice=%v engineering=%v", aliceAlive, engAlive)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestServerPersistsPinAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache.json")
	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "daemon.sock")

	// First server lifetime: client pins a space, then shuts down.
	ctx1, cancel1 := context.WithCancel(context.Background())
	listener1, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	backend1 := &pushChatClient{
		WorkspaceClient: newTestWorkspaceClient(),
		events:          make(chan api.ChatMessage),
		started:         make(chan struct{}),
	}
	server1 := NewServer(backend1, Options{
		SocketPath: socketPath,
		CachePath:  cachePath,
	})
	done1 := make(chan error, 1)
	go func() { done1 <- server1.Serve(ctx1, listener1) }()

	client1, err := api.NewRemoteClient(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	pinCtx, pinCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := client1.PinChatSpace(pinCtx, "spaces/design"); err != nil {
		pinCancel()
		t.Fatalf("pin failed: %v", err)
	}
	pinCancel()
	// Pin should have opened a backend subscription for design.
	waitForSubscription(t, backend1, "spaces/design")

	// Snapshot should report Live=true for the pinned space so the TUI
	// renders the indicator.
	snapCtx, snapCancel := context.WithTimeout(context.Background(), 2*time.Second)
	snap, err := client1.Snapshot(snapCtx)
	snapCancel()
	if err != nil {
		t.Fatal(err)
	}
	foundLive := false
	for _, s := range snap.Spaces {
		if s.Name == "spaces/design" && s.Live {
			foundLive = true
			break
		}
	}
	if !foundLive {
		t.Fatal("snapshot did not stamp Live on pinned space")
	}

	_ = client1.Close()
	cancel1()
	<-done1

	// Second server lifetime: same cache path. Pin should be restored
	// before any client subscribes.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	listener2, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	backend2 := &pushChatClient{
		WorkspaceClient: newTestWorkspaceClient(),
		events:          make(chan api.ChatMessage),
		started:         make(chan struct{}),
	}
	server2 := NewServer(backend2, Options{
		SocketPath: socketPath,
		CachePath:  cachePath,
	})
	done2 := make(chan error, 1)
	go func() { done2 <- server2.Serve(ctx2, listener2) }()

	waitFor(t, 2*time.Second, func() bool {
		return backend2.SubscribeCalls("spaces/design") >= 1
	}, "pinned space was not auto-resubscribed after restart")

	cancel2()
	select {
	case err := <-done2:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestListenAndServeRemovesSocketOnShutdown(t *testing.T) {
	dir := t.TempDir()
	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "daemon.sock")
	ctx, cancel := context.WithCancel(context.Background())
	server := NewServer(newTestWorkspaceClient(), Options{
		SocketPath: socketPath,
		CachePath:  filepath.Join(dir, "cache.json"),
	})
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe(ctx) }()

	waitForSocket(t, socketPath)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket should be removed after shutdown, stat err=%v", err)
	}
}

type pushChatClient struct {
	api.WorkspaceClient
	events  chan api.ChatMessage
	started chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   map[string]int
}

func (c *pushChatClient) SubscribeChat(_ context.Context, spaceName string) (<-chan api.ChatMessage, error) {
	c.mu.Lock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[spaceName]++
	c.mu.Unlock()
	c.once.Do(func() { close(c.started) })
	return c.events, nil
}

func (c *pushChatClient) SubscribeCalls(spaceName string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[spaceName]
}

func assertChatEvent(t *testing.T, ch <-chan api.ChatMessage, id string) {
	t.Helper()
	select {
	case msg := <-ch:
		if msg.ID != id {
			t.Fatalf("message id=%q want %q", msg.ID, id)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", id)
	}
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s was not created", socketPath)
}

func waitForSubscription(t *testing.T, backend *pushChatClient, spaceName string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if backend.SubscribeCalls(spaceName) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("backend did not subscribe to %s", spaceName)
}

func waitFor(t *testing.T, timeout time.Duration, predicate func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "gwsd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
