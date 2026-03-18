package loghandler

import (
	"context"
	"log/slog"
	"time"

	nightwatch "github.com/laravel/nightwatch-go"
	"github.com/laravel/nightwatch-go/sensor"
)

// Handler is a slog.Handler that captures log records as Nightwatch telemetry.
// It wraps an underlying handler, so logs are both recorded and passed through.
//
// Usage:
//
//	nw, _ := nightwatch.New(nightwatch.WithToken("..."))
//	handler := loghandler.New(nw, slog.NewJSONHandler(os.Stdout, nil))
//	logger := slog.New(handler)
type Handler struct {
	next slog.Handler
	nw   *nightwatch.Nightwatch
}

// New creates a new Nightwatch slog handler that wraps the given handler.
func New(nw *nightwatch.Nightwatch, next slog.Handler) *Handler {
	return &Handler{
		next: next,
		nw:   nw,
	}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	// Pass through to the underlying handler first
	err := h.next.Handle(ctx, record)

	// Capture telemetry (never block or fail the log operation)
	h.capture(ctx, record)

	return err
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{
		next: h.next.WithAttrs(attrs),
		nw:   h.nw,
	}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{
		next: h.next.WithGroup(name),
		nw:   h.nw,
	}
}

func (h *Handler) capture(ctx context.Context, record slog.Record) {
	defer func() {
		if r := recover(); r != nil {
			// Monitoring must never crash the application
			_ = r
		}
	}()

	if !h.nw.Config().Enabled {
		return
	}

	state := nightwatch.StateFromContext(ctx)
	if state == nil {
		return
	}

	// Check minimum log level
	if !h.shouldCapture(record.Level) {
		return
	}

	level := slogLevelToPSR3(record.Level)

	// Collect attributes into a map
	attrs := make(map[string]any)
	record.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})

	var ts time.Time
	if record.Time.IsZero() {
		ts = time.Now()
	} else {
		ts = record.Time
	}

	logRecord := sensor.LogRecord(state, level, record.Message, attrs, nil, ts)
	_ = h.nw.Ingester().Write(logRecord)
}

func (h *Handler) shouldCapture(level slog.Level) bool {
	minLevel := h.nw.Config().LogLevel
	switch minLevel {
	case "error":
		return level >= slog.LevelError
	case "warning", "warn":
		return level >= slog.LevelWarn
	case "info":
		return level >= slog.LevelInfo
	default: // "debug" or empty
		return true
	}
}

// slogLevelToPSR3 maps Go slog levels to PSR-3 log level strings.
func slogLevelToPSR3(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "error"
	case level >= slog.LevelWarn:
		return "warning"
	case level >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}
