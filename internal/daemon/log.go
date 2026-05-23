package daemon

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"github.com/fabhiansan/gws-tui/internal/api"
)

// logError records a daemon-side failure. It stays silent once the server
// context is cancelled so a shutdown does not flush a burst of spurious errors
// from in-flight goroutines. The emitted record points at the caller's source
// location, not at this helper.
func (s *Server) logError(label string, err error) {
	if err == nil {
		return
	}
	if s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	s.logAttrs(slog.LevelError, label, slog.String("error", err.Error()))
}

// logInfo records a daemon-side status line (startup, mode resolution, etc.).
func (s *Server) logInfo(msg string, attrs ...slog.Attr) {
	s.logAttrs(slog.LevelInfo, msg, attrs...)
}

// logRequestError records a dispatch failure together with the request that
// triggered it: the method, the request id, the originating session, how long
// the call ran, and a bounded copy of the params so a bad request can be
// reproduced from the log alone.
func (s *Server) logRequestError(session *Session, env api.Envelope, dur time.Duration, err error) {
	if err == nil {
		return
	}
	if s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	var sessionID uint64
	if session != nil {
		sessionID = session.id
	}
	s.logAttrs(slog.LevelError, "request failed",
		slog.String("method", env.Method),
		slog.Uint64("request_id", env.ID),
		slog.Uint64("session", sessionID),
		slog.Duration("duration", dur),
		slog.String("params", truncateForLog(string(env.Params), 300)),
		slog.String("error", err.Error()),
	)
}

// logAttrs emits one slog record attributed to its caller's caller, so the thin
// wrappers above (logError, logInfo, logRequestError) do not all collapse the
// "source" attribute onto the same line. The skip of 3 covers runtime.Callers,
// logAttrs itself, and the wrapper that called it.
func (s *Server) logAttrs(level slog.Level, msg string, attrs ...slog.Attr) {
	logger := slog.Default()
	if !logger.Enabled(context.Background(), level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	rec := slog.NewRecord(time.Now(), level, msg, pcs[0])
	rec.AddAttrs(attrs...)
	_ = logger.Handler().Handle(context.Background(), rec)
}

// truncateForLog bounds a value (rune-safe) so a large params blob never bloats
// a single log line.
func truncateForLog(s string, limit int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}
