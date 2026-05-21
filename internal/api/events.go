package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
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

func chatEventsLogf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gws: chat events: "+format+"\n", args...)
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
	if err := c.probeChatEvents(project, subscription); err != nil {
		chatEventsLogf("real-time probe failed (%v) — polling each space every 5s", err)
		chatEventsLogf("enable the Pub/Sub and Workspace Events APIs on your project for instant chat")
		return nil
	}
	c.chatEventHub = newChatEventHub(c, project, subscription)
	if subscription != "" {
		chatEventsLogf("real-time delivery enabled via subscription %s", subscription)
	} else {
		chatEventsLogf("real-time delivery enabled via project %s", project)
	}
	return c.chatEventHub
}

// probeChatEvents verifies the events pipeline really works before the daemon
// commits to it: it runs a single `+subscribe --once` pull, which provisions
// the Pub/Sub plumbing and surfaces auth/API errors. --no-ack leaves any
// pending message for the real stream to redeliver.
func (c *CommandClient) probeChatEvents(project, subscription string) error {
	if c.path == "" {
		return errors.New("gws path is empty")
	}
	ctx, cancel := context.WithTimeout(c.closeCtx, 45*time.Second)
	defer cancel()
	args := chatEventSubscribeArgs(project, subscription, "--once", "--no-ack")
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Stdout = io.Discard
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := firstLine(stderr.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

func chatEventSubscribeArgs(project, subscription string, extra ...string) []string {
	args := []string{
		"events", "+subscribe",
		"--target", chatEventsTargetAll,
		"--event-types", chatEventTypeCreated,
		"--format", "json",
	}
	if subscription != "" {
		args = append(args, "--subscription", subscription)
	} else {
		args = append(args, "--project", project)
	}
	return append(args, extra...)
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
	go h.renewLoop(ctx)
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
	cmd := exec.CommandContext(ctx, h.client.path, chatEventSubscribeArgs(h.project, h.subscription)...)
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

// renewLoop keeps the Workspace Events subscription alive. A subscription that
// carries full message data expires within hours, so renew well ahead of time:
// a 30-minute tick with a 2-hour window stays comfortably ahead of expiry even
// for the shortest-lived subscription kinds.
func (h *chatEventHub) renewLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.renewOnce(ctx)
		}
	}
}

func (h *chatEventHub) renewOnce(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(rctx, h.client.path,
		"events", "+renew", "--all", "--within", "2h", "--format", "json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		chatEventsLogf("subscription renewal failed: %v: %s", err, firstLine(string(out)))
		return
	}
	chatEventsLogf("subscriptions renewed")
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
