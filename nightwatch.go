package nightwatch

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// Ingester is the interface for writing telemetry records to the backend.
// Implemented by both Ingest (TCP) and HTTPIngest (direct HTTP).
type Ingester interface {
	Write(record Record) error
	WriteNow(record Record) error
	Flush()
	Digest() error
	Ping() error
}

// Nightwatch is the main entry point for the Go APM client.
// It manages configuration, buffering, and ingestion of telemetry records.
type Nightwatch struct {
	config    *Config
	ingester  Ingester
	tokenHash string
}

// New creates a new Nightwatch instance with the given options.
// By default, uses TCP ingest to a local agent.
func New(opts ...Option) (*Nightwatch, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.Token == "" {
		return nil, fmt.Errorf("nightwatch: token is required (set NIGHTWATCH_TOKEN or use WithToken)")
	}

	tokenHash := TokenHash(cfg.Token)
	buffer := NewRecordsBuffer(cfg.EventBuffer)

	var ingester Ingester
	if cfg.BaseURL != "" {
		// HTTP direct mode — no local agent needed
		hi := NewHTTPIngest(cfg.BaseURL, cfg.Token, cfg.Server, buffer)
		if cfg.Debug {
			hi.SetDebug(true)
		}
		ingester = hi
	} else {
		// TCP mode — requires local agent on port 2407
		ingester = NewIngest(cfg.Address, cfg.ConnectionTimeout, cfg.ReadWriteTimeout, buffer, tokenHash)
	}

	return &Nightwatch{
		config:    cfg,
		ingester:  ingester,
		tokenHash: tokenHash,
	}, nil
}

// BeginRequest creates a new ExecutionState for an incoming HTTP request and returns it.
func (nw *Nightwatch) BeginRequest(r *http.Request) *ExecutionState {
	state := NewExecutionState(nw.config.Deployment, nw.config.Server)
	state.Method = r.Method
	state.URL = r.URL.String()
	state.IP = r.RemoteAddr
	state.RequestSize = int(r.ContentLength)
	if state.RequestSize < 0 {
		state.RequestSize = 0
	}
	return state
}

// EndRequest finalizes an execution, builds the request record, writes it to the buffer,
// and digests. This should be called in a deferred function in the middleware.
func (nw *Nightwatch) EndRequest(state *ExecutionState, statusCode, responseSize int) {
	defer nw.recoverSilently()

	if !nw.config.Enabled {
		return
	}

	state.TransitionStage(StageEnd)
	state.StatusCode = statusCode
	state.ResponseSize = responseSize

	if !nw.shouldSampleRequest() {
		nw.ingester.Flush()
		return
	}

	record := nw.buildRequestRecord(state)

	// Write the request record and digest synchronously.
	// This ensures query + request records are sent in the SAME batch,
	// which is required for the Omni timeline stitching to work.
	// (The PHP agent also sends all records together at request termination.)
	_ = nw.ingester.Write(record)
	_ = nw.ingester.Digest()
}

// ReportError explicitly reports an error as a handled exception.
func (nw *Nightwatch) ReportError(ctx context.Context, err error) {
	defer nw.recoverSilently()

	if !nw.config.Enabled {
		return
	}

	state := StateFromContext(ctx)
	if state == nil {
		return
	}

	record := nw.buildExceptionRecord(state, err, true)
	_ = nw.ingester.Write(record)
}

// ReportPanic builds an exception record from a recovered panic value.
func (nw *Nightwatch) ReportPanic(state *ExecutionState, panicVal any) {
	defer nw.recoverSilently()

	if !nw.config.Enabled {
		return
	}

	var err error
	switch v := panicVal.(type) {
	case error:
		err = v
	case string:
		err = fmt.Errorf("%s", v)
	default:
		err = fmt.Errorf("%v", v)
	}

	record := nw.buildExceptionRecord(state, err, false)
	_ = nw.ingester.WriteNow(record)
}

// TransitionStage transitions the execution state's stage.
func (nw *Nightwatch) TransitionStage(state *ExecutionState, next ExecutionStage) {
	state.TransitionStage(next)
}

// Ingester returns the underlying ingester for direct access.
func (nw *Nightwatch) Ingester() Ingester {
	return nw.ingester
}

// SetIngester replaces the ingester. Useful for wrapping with middleware (logging, spying, etc).
func (nw *Nightwatch) SetIngester(i Ingester) {
	nw.ingester = i
}

// Ingest returns the TCP ingest instance (nil if using HTTP mode).
// Deprecated: Use Ingester() instead.
func (nw *Nightwatch) Ingest() *Ingest {
	if i, ok := nw.ingester.(*Ingest); ok {
		return i
	}
	return nil
}

// Config returns the current configuration.
func (nw *Nightwatch) Config() *Config {
	return nw.config
}

func (nw *Nightwatch) buildRequestRecord(state *ExecutionState) Record {
	return Record{
		"v":         1,
		"t":         RecordTypeRequest,
		"timestamp": state.Timestamp,
		"deploy":    state.Deploy,
		"server":    state.Server,
		"_group":    nw.requestGroupHash(state),
		"trace_id": state.TraceID,
		"user":     state.UserID,
		// NOTE: Do NOT add execution_id here — the ClickHouse requests table
		// likely doesn't have this column (PHP RequestSensor omits it).
		// Adding it causes the insert to fail silently → "not found".
		// The Omni dashboard should use trace_id for stitching (see fix below).
		// Request fields
		"method":        state.Method,
		"url":           state.URL,
		"route_name":    state.RouteName,
		"route_methods": state.RouteMethods,
		"route_domain":  state.RouteDomain,
		"route_path":    state.RoutePath,
		"route_action":  state.RouteAction,
		"ip":            state.IP,
		"duration":      state.TotalDuration(),
		"status_code":   state.StatusCode,
		"request_size":  state.RequestSize,
		"response_size": state.ResponseSize,
		// Stage durations
		"bootstrap":        state.StageDurations[StageBootstrap],
		"before_middleware": state.StageDurations[StageBeforeMiddleware],
		"action":           state.StageDurations[StageAction],
		"render":           state.StageDurations[StageRender],
		"after_middleware":  state.StageDurations[StageAfterMiddleware],
		"sending":          state.StageDurations[StageSending],
		"terminating":      state.StageDurations[StageTerminating],
		// Counters
		"exceptions":        state.Exceptions,
		"logs":              state.Logs,
		"queries":           state.Queries,
		"lazy_loads":        state.LazyLoads,
		"jobs_queued":       state.JobsQueued,
		"mail":              state.Mail,
		"notifications":     state.Notifications,
		"outgoing_requests": state.OutgoingRequests,
		"files_read":        state.FilesRead,
		"files_written":     state.FilesWritten,
		"cache_events":      state.CacheEvents,
		"hydrated_models":   state.HydratedModels,
		"peak_memory_usage": state.PeakMemory(),
		"exception_preview": TinyText(state.ExceptionPreview),
		"context":           "{}",
		"headers":           "{}",
		"payload":           "",
	}
}

func (nw *Nightwatch) buildExceptionRecord(state *ExecutionState, err error, handled bool) Record {
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	stack := string(buf[:n])

	typeName := fmt.Sprintf("%T", err)
	file, line := findExceptionCaller()

	if !handled {
		state.ExceptionPreview = err.Error()
	}
	state.IncrExceptions()

	now := float64(time.Now().UnixMicro()) / 1_000_000

	return Record{
		"v":                 3,
		"t":                 RecordTypeException,
		"timestamp":         now,
		"deploy":            state.Deploy,
		"server":            state.Server,
		"_group":            GroupHashf("%s,,%s,%d", typeName, file, line),
		"trace_id":          state.TraceID,
		"execution_source":  state.Source,
		"execution_id":      state.ExecutionID(),
		"execution_preview": state.ExecutionPreview(),
		"execution_stage":   state.CurrentStage(),
		"user":              state.UserID,
		"class":             Text(typeName),
		"file":              Text(file),
		"line":              line,
		"message":           Text(err.Error()),
		"code":              "",
		"trace":             MediumText(stack),
		"handled":           handled,
		"go_version":        runtime.Version(),
	}
}

func (nw *Nightwatch) requestGroupHash(state *ExecutionState) string {
	methods := ""
	for i, m := range state.RouteMethods {
		if i > 0 {
			methods += "|"
		}
		methods += m
	}
	return GroupHashf("%s,%s,%s", methods, state.RouteDomain, state.RoutePath)
}

func (nw *Nightwatch) shouldSampleRequest() bool {
	if nw.config.RequestSampleRate >= 1.0 {
		return true
	}
	if nw.config.RequestSampleRate <= 0.0 {
		return false
	}
	return rand.Float64() < nw.config.RequestSampleRate
}

func (nw *Nightwatch) recoverSilently() {
	if r := recover(); r != nil {
		// Monitoring must never crash the application.
		fmt.Fprintf(os.Stderr, "nightwatch: recovered from panic: %v\n", r)
	}
}

// findExceptionCaller walks the call stack to find the first frame in user code,
// skipping nightwatch, gin, net/http, and runtime internals.
func findExceptionCaller() (string, int) {
	var pcs [30]uintptr
	n := runtime.Callers(3, pcs[:])
	if n == 0 {
		return "unknown", 0
	}

	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		fn := frame.Function
		if strings.Contains(fn, "nightwatch") ||
			strings.HasPrefix(fn, "runtime.") ||
			strings.HasPrefix(fn, "github.com/gin-gonic/") ||
			strings.HasPrefix(fn, "net/http") {
			if !more {
				break
			}
			continue
		}
		return frame.File, frame.Line
	}
	return "unknown", 0
}
