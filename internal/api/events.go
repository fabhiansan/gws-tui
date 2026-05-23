package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// chatEventsTargetAll subscribes to every Chat space the authenticated
	// user belongs to through a single Workspace Events subscription, instead
	// of one subscription (and one subprocess) per space.
	chatEventsTargetAll = "//chat.googleapis.com/spaces/-"
	// chatEventTypeCreated is the CloudEvents type for a new Chat message.
	chatEventTypeCreated = "google.workspace.chat.message.v1.created"
)

// ChatEventOptions configures how the daemon receives new chat messages. When
// real-time delivery is available a single `gws events +subscribe` stream
// feeds every space at once; otherwise each space is polled every 5 seconds.
type ChatEventOptions struct {
	// Disabled forces polling even when a Google Cloud project is available.
	Disabled bool
	// Project overrides the GCP project used for the Pub/Sub plumbing. When
	// empty the project is read from GWS_EVENTS_PROJECT, then from the
	// authenticated session reported by `gws auth status`.
	Project string
	// Subscription reuses an existing Pub/Sub subscription instead of letting
	// the helper provision one. When empty it falls back to
	// GWS_EVENTS_SUBSCRIPTION.
	Subscription string
}

// ChatEventConfigurer is implemented by clients that can deliver chat messages
// in real time through the Google Workspace Events API. Only CommandClient
// implements it; RemoteClient delegates to the daemon, which owns the stream.
type ChatEventConfigurer interface {
	ConfigureChatEvents(opts ChatEventOptions)
	// PrepareChatEvents resolves the delivery strategy ahead of the first
	// SubscribeChat call and returns "realtime" or "polling".
	PrepareChatEvents() string
}

// chatEventsLogf records one chat-events status line. It routes through the
// shared slog logger so the line lands in daemon.log with a timestamp and a
// level alongside the rest of the daemon's diagnostics.
func chatEventsLogf(format string, args ...any) {
	slog.Info("chat events: " + fmt.Sprintf(format, args...))
}

// ConfigureChatEvents records real-time delivery preferences. Call it before
// the first SubscribeChat (or PrepareChatEvents) call.
func (c *CommandClient) ConfigureChatEvents(opts ChatEventOptions) {
	c.chatEventMu.Lock()
	c.chatEventOpts = opts
	c.chatEventMu.Unlock()
}

// PrepareChatEvents resolves the chat delivery strategy ahead of time so the
// first SubscribeChat call does not pay for the project lookup and viability
// probe. Safe to call more than once; the result is cached.
func (c *CommandClient) PrepareChatEvents() string {
	if c.resolveChatHub() != nil {
		return "realtime"
	}
	return "polling"
}

// resolveChatHub decides — once — whether real-time chat delivery is usable
// and, if so, builds the shared event hub. A nil return means callers should
// fall back to polling.
func (c *CommandClient) resolveChatHub() *chatEventHub {
	c.chatEventMu.Lock()
	defer c.chatEventMu.Unlock()
	if c.chatEventResolved {
		return c.chatEventHub
	}
	c.chatEventResolved = true

	opts := c.chatEventOpts
	if opts.Disabled {
		chatEventsLogf("disabled by configuration — polling each space every 5s")
		return nil
	}
	project := strings.TrimSpace(opts.Project)
	subscription := strings.TrimSpace(opts.Subscription)
	if project == "" {
		project = strings.TrimSpace(os.Getenv("GWS_EVENTS_PROJECT"))
	}
	if subscription == "" {
		subscription = strings.TrimSpace(os.Getenv("GWS_EVENTS_SUBSCRIPTION"))
	}
	if project == "" && subscription == "" {
		ctx, cancel := context.WithTimeout(c.closeCtx, 15*time.Second)
		if st, err := c.AuthStatus(ctx); err == nil {
			project = strings.TrimSpace(st.ProjectID)
		}
		cancel()
	}
	if project == "" && subscription == "" {
		chatEventsLogf("no Google Cloud project detected — polling each space every 5s")
		return nil
	}
	if err := c.probeChatEvents(); err != nil {
		chatEventsLogf("real-time probe failed (%v) — polling each space every 5s", err)
		chatEventsLogf("enable the Pub/Sub and Workspace Events APIs on your project for instant chat")
		return nil
	}
	c.chatEventHub = newChatEventHub(c, project, subscription)
	if subscription != "" {
		chatEventsLogf("real-time delivery starting via subscription %s", subscription)
	} else {
		chatEventsLogf("real-time delivery starting via project %s", project)
	}
	return c.chatEventHub
}

// chatEventSubscription is one Workspace Events subscription as reported by
// `gws events subscriptions list`.
type chatEventSubscription struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	ExpireTime string `json:"expireTime"`
}

// chatSubscriptionsList returns the caller's Workspace Events subscriptions for
// new Chat messages. It is read-only — it neither creates nor deletes anything —
// so it is safe both as a reachability probe and as a live health check.
func (c *CommandClient) chatSubscriptionsList(ctx context.Context) ([]chatEventSubscription, error) {
	if c.path == "" {
		return nil, errors.New("gws path is empty")
	}
	filter := fmt.Sprintf(`{"filter":"event_types:\"%s\""}`, chatEventTypeCreated)
	var resp struct {
		Subscriptions []chatEventSubscription `json:"subscriptions"`
	}
	if err := c.runJSON(ctx, &resp, "events", "subscriptions", "list",
		"--params", filter, "--format", "json"); err != nil {
		return nil, err
	}
	return resp.Subscriptions, nil
}

// deleteChatEventSubscriptions removes every Workspace Events subscription for
// new Chat messages. runStream calls this before each `+subscribe` so the
// helper always provisions a fresh subscription bound to the fresh Pub/Sub
// topic it is about to create. `+subscribe --cleanup` deletes the topic but not
// the Workspace Events subscription, and that subscription has a deterministic
// name — so a stale one left by an earlier run would be silently reused while
// still pointing at the deleted topic, and chat events would be published where
// nothing reads them: a phantom real-time stream.
func (c *CommandClient) deleteChatEventSubscriptions(ctx context.Context) {
	subs, err := c.chatSubscriptionsList(ctx)
	if err != nil {
		return
	}
	for _, sub := range subs {
		if sub.Name == "" {
			continue
		}
		params := fmt.Sprintf(`{"name":"%s"}`, sub.Name)
		_ = c.runVoid(ctx, "events", "subscriptions", "delete", "--params", params)
	}
}

// probeChatEvents decides whether the daemon should attempt real-time delivery.
// It is a read-only reachability check: listing Workspace Events subscriptions
// exercises the Workspace Events API and the stored credentials without
// creating or destroying anything. A non-error response — an empty list
// included — means the API is usable, so the daemon builds the hub and lets
// runStream do the real provisioning; an error (API disabled, broken auth)
// means real-time delivery is unavailable and the caller falls back to polling.
//
// The check cannot prove provisioning succeeds end to end — a missing Pub/Sub
// publisher grant only surfaces when the Workspace Events subscription is
// created — so the hub's maintain loop verifies the live subscription and
// reports an honest status instead of a phantom stream.
func (c *CommandClient) probeChatEvents() error {
	ctx, cancel := context.WithTimeout(c.closeCtx, 20*time.Second)
	defer cancel()
	_, err := c.chatSubscriptionsList(ctx)
	return err
}

// chatEventSubscribeArgs builds the `gws events +subscribe` invocation for the
// real-time stream. --cleanup makes the helper delete the Pub/Sub topic and
// subscription it provisions when the process exits, so a reconnect or restart
// never leaks a fresh pair. --cleanup does not cover the Workspace Events
// subscription; runStream clears that explicitly via deleteChatEventSubscriptions
// before each run.
func chatEventSubscribeArgs(project, subscription string) []string {
	args := []string{
		"events", "+subscribe",
		"--target", chatEventsTargetAll,
		"--event-types", chatEventTypeCreated,
		"--format", "json",
		"--cleanup",
	}
	if subscription != "" {
		args = append(args, "--subscription", subscription)
	} else {
		args = append(args, "--project", project)
	}
	return args
}

// terminateGracefully makes a context-cancelled command receive SIGTERM
// instead of an immediate SIGKILL, so the upstream gws can run its --cleanup
// handler and delete the Pub/Sub resources it provisioned before exiting. If
// the process ignores SIGTERM it is force-killed after the grace period.
func terminateGracefully(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
}

// chatEventHub fans a single Workspace Events stream out to per-space
// subscriber channels. One subprocess and one Pub/Sub subscription serve every
// space the daemon watches.
type chatEventHub struct {
	client       *CommandClient
	project      string
	subscription string

	mu          sync.Mutex
	subscribers map[string]map[chan ChatMessage]struct{}
	count       int
	procCancel  context.CancelFunc
	seen        map[string]time.Time
	// healthy reports whether the live Workspace Events subscription is
	// actually delivering; healthKnown guards the first observation so the
	// initial state is logged exactly once.
	healthy     bool
	healthKnown bool
}

func newChatEventHub(c *CommandClient, project, subscription string) *chatEventHub {
	return &chatEventHub{
		client:       c,
		project:      project,
		subscription: subscription,
		subscribers:  map[string]map[chan ChatMessage]struct{}{},
		seen:         map[string]time.Time{},
	}
}

// subscribe registers a channel for one space. The stream subprocess starts on
// the first subscriber and stops once the last one goes away. The channel is
// closed when ctx is cancelled.
func (h *chatEventHub) subscribe(ctx context.Context, space string) <-chan ChatMessage {
	out := make(chan ChatMessage, 16)
	h.mu.Lock()
	set := h.subscribers[space]
	if set == nil {
		set = map[chan ChatMessage]struct{}{}
		h.subscribers[space] = set
	}
	set[out] = struct{}{}
	h.count++
	if h.procCancel == nil {
		h.startLocked()
	}
	h.mu.Unlock()
	go func() {
		<-ctx.Done()
		h.unsubscribe(space, out)
	}()
	return out
}

func (h *chatEventHub) unsubscribe(space string, out chan ChatMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set := h.subscribers[space]; set != nil {
		if _, ok := set[out]; ok {
			delete(set, out)
			close(out)
			h.count--
			if len(set) == 0 {
				delete(h.subscribers, space)
			}
		}
	}
	if h.count == 0 && h.procCancel != nil {
		h.procCancel()
		h.procCancel = nil
	}
}

// startLocked launches the stream and renewal goroutines. Caller holds h.mu.
func (h *chatEventHub) startLocked() {
	ctx, cancel := context.WithCancel(h.client.closeCtx)
	h.procCancel = cancel
	go h.streamLoop(ctx)
	go h.maintainLoop(ctx)
}

func (h *chatEventHub) streamLoop(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		err := h.runStream(ctx)
		if ctx.Err() != nil {
			return
		}
		// A stream that survived a while is healthy: reset the backoff so a
		// later blip does not inherit a long delay.
		if time.Since(started) > 2*time.Minute {
			backoff = time.Second
		}
		if err != nil {
			chatEventsLogf("stream interrupted (%v) — reconnecting in %s", err, backoff)
		} else {
			chatEventsLogf("stream ended — reconnecting in %s", backoff)
		}
		if !sleepCtx(ctx, backoff) {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (h *chatEventHub) runStream(ctx context.Context) error {
	// Clear any Workspace Events subscription left by an earlier run so
	// +subscribe provisions a fresh one bound to the topic it is about to
	// create — see deleteChatEventSubscriptions for why a stale one is fatal.
	h.client.deleteChatEventSubscriptions(ctx)
	cmd := exec.CommandContext(ctx, h.client.path, chatEventSubscribeArgs(h.project, h.subscription)...)
	terminateGracefully(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		msg, ok := parseChatCloudEvent(scanner.Bytes(), "")
		if !ok || msg.Space == "" {
			continue
		}
		h.dispatch(msg)
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if scanErr != nil {
		return scanErr
	}
	return waitErr
}

// dispatch routes one message to every channel watching its space. Sends are
// non-blocking under the lock so a slow consumer can neither stall the stream
// nor race unsubscribe into a send-on-closed-channel panic.
func (h *chatEventHub) dispatch(msg ChatMessage) {
	msg.FromRealtime = true
	h.mu.Lock()
	defer h.mu.Unlock()
	if msg.ID != "" {
		now := time.Now()
		if seenAt, ok := h.seen[msg.ID]; ok && now.Sub(seenAt) < 10*time.Minute {
			return
		}
		if len(h.seen) > 4096 {
			h.seen = map[string]time.Time{}
		}
		h.seen[msg.ID] = now
	}
	for ch := range h.subscribers[msg.Space] {
		select {
		case ch <- msg:
		default:
		}
	}
}

// maintainLoop keeps the daemon honest about real-time delivery and keeps the
// Workspace Events subscription alive. Every minute it inspects the live
// subscription: it updates the reported health (logged only on a change) and
// renews the subscription once it is within two hours of expiry. The hub is
// never torn down here — streamLoop reconnects on its own; this loop only
// observes and renews, so a transient outage self-heals without a daemon
// restart.
func (h *chatEventHub) maintainLoop(ctx context.Context) {
	// Give the first +subscribe room to provision before the first check, so a
	// healthy startup is not briefly reported as "not delivering".
	if !sleepCtx(ctx, 30*time.Second) {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		h.maintainOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *chatEventHub) maintainOnce(ctx context.Context) {
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	subs, err := h.client.chatSubscriptionsList(lctx)
	if err != nil {
		// A transient list failure is not proof the stream is down; keep the
		// last known health rather than flapping it.
		return
	}
	var active *chatEventSubscription
	for i := range subs {
		if subs[i].State == "ACTIVE" {
			active = &subs[i]
			break
		}
	}
	h.setHealthy(active != nil)
	if active != nil && expiringSoon(active.ExpireTime) {
		h.renew(lctx, active.Name)
	}
}

// expiringSoon reports whether a subscription's expireTime is unset,
// unparseable, or within two hours — all cases where it should be renewed now.
func expiringSoon(expireTime string) bool {
	exp, err := time.Parse(time.RFC3339, expireTime)
	if err != nil {
		return true
	}
	return time.Until(exp) < 2*time.Hour
}

// setHealthy records whether the stream is delivering and logs only on a
// change, so the daemon log states the truth without spamming it.
func (h *chatEventHub) setHealthy(healthy bool) {
	h.mu.Lock()
	changed := !h.healthKnown || h.healthy != healthy
	h.healthy = healthy
	h.healthKnown = true
	h.mu.Unlock()
	if !changed {
		return
	}
	if healthy {
		chatEventsLogf("real-time stream connected — delivering chat events")
	} else {
		chatEventsLogf("real-time stream not delivering yet — provisioning or reconnecting")
	}
}

func (h *chatEventHub) renew(ctx context.Context, name string) {
	if name == "" {
		return
	}
	cmd := exec.CommandContext(ctx, h.client.path,
		"events", "+renew", "--name", name, "--format", "json")
	if out, err := cmd.CombinedOutput(); err != nil && ctx.Err() == nil {
		chatEventsLogf("subscription renewal failed: %v: %s", err, upstreamErrorLine(string(out)))
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
