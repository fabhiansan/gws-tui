package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type CommandClient struct {
	path     string
	subMu    sync.Mutex
	lastSeen map[string]time.Time

	// closeCtx is cancelled by Close; it bounds the lifetime of the shared
	// chat event stream and its renewal loop.
	closeCtx    context.Context
	closeCancel context.CancelFunc

	chatEventMu       sync.Mutex
	chatEventOpts     ChatEventOptions
	chatEventResolved bool
	chatEventHub      *chatEventHub
}

func NewCommandClient(path string) *CommandClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &CommandClient{path: path, closeCtx: ctx, closeCancel: cancel}
}

func (c *CommandClient) AuthStatus(ctx context.Context) (AuthStatus, error) {
	var out AuthStatus
	err := c.runJSON(ctx, &out, "auth", "status")
	return out, err
}

func (c *CommandClient) DownloadAttachment(ctx context.Context, attachment Attachment, outputPath string) error {
	resourceName := attachment.MediaResourceName()
	if resourceName == "" {
		return errors.New("attachment media resource is missing")
	}
	if strings.HasPrefix(resourceName, "gmail/") {
		return c.downloadGmailAttachment(ctx, resourceName, outputPath)
	}
	if strings.HasPrefix(resourceName, "drive/files/") {
		return c.downloadDriveFile(ctx, strings.TrimPrefix(resourceName, "drive/files/"), outputPath)
	}
	if outputPath == "" {
		return errors.New("output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(outputPath), ".gws-media-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	params, _ := json.Marshal(map[string]string{"resourceName": resourceName, "alt": "media"})
	command := exec.CommandContext(ctx, c.path, "chat", "media", "download", "--params", string(params), "--output", filepath.Base(tmpPath))
	command.Dir = filepath.Dir(tmpPath)
	payload, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gws media download failed: %s", strings.TrimSpace(string(payload)))
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return err
	}
	return nil
}

// Close stops the shared chat event stream and removes the Workspace Events
// subscription it provisioned. Cancelling closeCtx makes the +subscribe helper
// receive SIGTERM, so its --cleanup handler deletes the Pub/Sub topic and
// subscription; the Workspace Events subscription is not covered by --cleanup,
// so it is deleted here explicitly. The teardown runs on a fresh context
// because closeCtx is cancelled first.
func (c *CommandClient) Close() error {
	c.chatEventMu.Lock()
	hub := c.chatEventHub
	c.chatEventMu.Unlock()
	if c.closeCancel != nil {
		c.closeCancel()
	}
	if hub != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		c.deleteChatEventSubscriptions(ctx)
		cancel()
	}
	return nil
}

func (c *CommandClient) runJSON(ctx context.Context, out any, args ...string) error {
	if c.path == "" {
		return errors.New("gws path is empty")
	}
	command := exec.CommandContext(ctx, c.path, args...)
	payload, err := command.Output()
	if err != nil {
		return commandError(args, err)
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode `gws %s`: %w (raw=%s)", strings.Join(args, " "), err, truncate(string(payload), 400))
	}
	return nil
}

// commandError turns a failed `gws` invocation into a diagnostic error. For a
// non-zero exit it names the subcommand, the exit code, and the upstream stderr
// verbatim; the full stderr is also logged as a structured record so a
// truncated UI status line never hides the real cause. Non-exit failures
// (binary missing, context cancelled) are wrapped with the subcommand so the
// caller still knows which command died.
func commandError(args []string, err error) error {
	joined := strings.Join(args, " ")
	exit, ok := err.(*exec.ExitError)
	if !ok {
		return fmt.Errorf("`gws %s`: %w", joined, err)
	}
	stderr := strings.TrimSpace(string(exit.Stderr))
	slog.Error("gws command failed",
		"command", joined,
		"exit_code", exit.ExitCode(),
		"stderr", stderr,
	)
	if stderr == "" {
		return fmt.Errorf("`gws %s` exited %d", joined, exit.ExitCode())
	}
	return fmt.Errorf("`gws %s` exited %d: %s", joined, exit.ExitCode(), stderr)
}

// runVoid runs a gws subcommand and ignores its stdout. Use for endpoints
// that return google.protobuf.Empty (e.g. endActiveConference) where parsing
// the response would fail or yield no useful data.
func (c *CommandClient) runVoid(ctx context.Context, args ...string) error {
	if c.path == "" {
		return errors.New("gws path is empty")
	}
	command := exec.CommandContext(ctx, c.path, args...)
	if _, err := command.Output(); err != nil {
		return commandError(args, err)
	}
	return nil
}

func parseRFC3339(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	return time.Time{}
}

func lastSegment(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "/")
	return parts[len(parts)-1]
}

// upstreamErrorLine extracts the most useful diagnostic line from an upstream
// gws command's stderr (or combined output). The CLI prints progress chatter
// ("Using keyring backend: ...", "Creating Pub/Sub topic: ...") on the same
// stream as real failures, so the first line is usually noise. Genuine
// failures are prefixed with "error" (e.g. "error[auth]: ..."), so prefer
// such a line; otherwise fall back to the last non-empty line.
func upstreamErrorLine(output string) string {
	var last string
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "error") {
			return line
		}
		last = line
	}
	return last
}

func fallback(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
