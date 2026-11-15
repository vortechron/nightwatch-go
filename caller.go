package nightwatch

import (
	"context"
	"runtime"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const maxCallerFrames = 48

// queryInternalPrefixes are functions that run a query for the application
// but are never its location: the database layers and this client.
var queryInternalPrefixes = append(slices.Clone(internalFramePrefixes),
	"gorm.io/", "github.com/goravel/framework/", "github.com/jmoiron/sqlx", "database/sql.", "runtime.",
)

// QueryCallerProcessor returns a span processor that writes the file and line
// that ran a query onto each database span (nightwatch.query.file and .line),
// like Laravel's query location. A database span is a client span started by
// a tracer of one of scopes; the Goravel adapter passes Goravel's database
// instrumentation. It reads the call stack when the span starts, so it sees
// the code that called the database. It skips queries started from a paused
// context; register it only when query records are sent.
//
// The location is the first application frame (not the Go installation, not
// the module cache or vendor folder). When there is none, it is the first
// frame of a dependency that is not a database layer, as Laravel falls back to
// the first vendor file outside the framework. Paths are absolute, like the
// exception frames.
func QueryCallerProcessor(scopes ...string) sdktrace.SpanProcessor {
	return &callerProcessor{scopes: slices.Clone(scopes)}
}

type callerProcessor struct{ scopes []string }

// OnStart reads the location. A query started from a paused context is never
// recorded, so its stack is not read.
func (p *callerProcessor) OnStart(parent context.Context, span sdktrace.ReadWriteSpan) {
	if span.SpanKind() != trace.SpanKindClient || !slices.Contains(p.scopes, span.InstrumentationScope().Name) || Paused(parent) {
		return
	}

	if file, line := queryCaller(); file != "" {
		span.SetAttributes(attribute.String(AttrQueryFile, file), attribute.Int(AttrQueryLine, line))
	}
}

func (p *callerProcessor) OnEnd(sdktrace.ReadOnlySpan) {}

func (p *callerProcessor) Shutdown(context.Context) error { return nil }

func (p *callerProcessor) ForceFlush(context.Context) error { return nil }

// queryCaller finds the location of the code that runs the current query.
func queryCaller() (string, int) {
	pcs := make([]uintptr, maxCallerFrames)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])

	var fallback runtime.Frame

	for {
		frame, more := frames.Next()

		switch {
		case hasAnyPrefix(frame.Function, queryInternalPrefixes):
		case isApplicationFile(frame.File):
			return frame.File, frame.Line
		case fallback.File == "" && isDependencyFile(frame.File):
			fallback = frame
		}

		if !more {
			return fallback.File, fallback.Line
		}
	}
}

func hasAnyPrefix(value string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(prefix string) bool { return strings.HasPrefix(value, prefix) })
}

// isDependencyFile is a file of a module, not of the Go installation (a
// downloaded toolchain also sits in the module cache, as toolchain@...).
func isDependencyFile(file string) bool {
	return (strings.Contains(file, "/pkg/mod/") || strings.Contains(file, "/vendor/")) && !strings.Contains(file, "/toolchain@")
}
