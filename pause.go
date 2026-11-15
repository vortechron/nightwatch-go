package nightwatch

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// AttrPaused marks the span Pause starts. Nothing under it is recorded except
// logs and exceptions.
const AttrPaused = "nightwatch.paused"

type pauseKey struct{}

// pauseState remembers the span that was current before Pause.
type pauseState struct{ before trace.Span }

// Pause returns a context in which Nightwatch records nothing (Laravel's
// Nightwatch::pause): no queries, cache events, outgoing requests, mail,
// notifications or queued jobs, and the attempts of a job queued from it are
// not sampled. Logs and exceptions are still recorded, as in Laravel. Go has
// no global state, so the pause lives in the context: use the returned one,
// and Resume it.
//
// It works with one marker span per execution, started and ended at the first
// Pause: work started from the returned context sits under that marker, and
// the translator leaves it out as it arrives, so it never fills the trace.
// Outside an execution, or when ctx is already paused, it returns ctx.
func Pause(ctx context.Context) context.Context {
	exec := executionOf(ctx)
	if exec == nil || Paused(ctx) {
		return ctx
	}

	before := trace.SpanFromContext(ctx)

	return context.WithValue(trace.ContextWithSpan(ctx, exec.pauseMarker(ctx)), pauseKey{}, &pauseState{before: before})
}

// pauseMarker returns the execution's pause marker, starting it under the span
// in ctx the first time.
func (e *execution) pauseMarker(ctx context.Context) trace.Span {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.marker == nil {
		_, e.marker = tracer().Start(ctx, "nightwatch.pause", trace.WithAttributes(attribute.Bool(AttrPaused, true)))
		e.marker.End()
	}

	return e.marker
}

// Resume returns a context that records again (Laravel's Nightwatch::resume):
// work started from it is a child of the span that was current before Pause.
// A context that is not paused is returned as it is.
func Resume(ctx context.Context) context.Context {
	state, _ := ctx.Value(pauseKey{}).(*pauseState)
	if state == nil {
		return ctx
	}

	return context.WithValue(trace.ContextWithSpan(ctx, state.before), pauseKey{}, (*pauseState)(nil))
}

// Paused reports whether ctx was paused with Pause (Laravel's
// Nightwatch::paused).
func Paused(ctx context.Context) bool {
	state, _ := ctx.Value(pauseKey{}).(*pauseState)

	return state != nil
}

// Ignore runs fn with a paused context (Laravel's Nightwatch::ignore): the
// queries, jobs and other events fn starts from that context are not
// recorded.
func Ignore(ctx context.Context, fn func(ctx context.Context)) {
	fn(Pause(ctx))
}

// pausedSpans returns the members that sit under a Pause marker, the marker
// included. dropped holds the ids of paused spans the translator left out as
// they arrived: a member under one of them is paused too. It is nil when no
// member is a marker.
func pausedSpans(members []sdktrace.ReadOnlySpan, dropped map[trace.SpanID]bool) map[trace.SpanID]bool {
	byID := make(map[trace.SpanID]sdktrace.ReadOnlySpan, len(members))
	marked := false

	for _, span := range members {
		byID[span.SpanContext().SpanID()] = span
		marked = marked || isPauseMarker(span)
	}

	if !marked {
		return nil
	}

	paused := map[trace.SpanID]bool{}

	for id, span := range byID {
		paused[id] = underMarker(span, byID, dropped)
	}

	return paused
}

func underMarker(span sdktrace.ReadOnlySpan, byID map[trace.SpanID]sdktrace.ReadOnlySpan, dropped map[trace.SpanID]bool) bool {
	for range maxAncestorDepth {
		if isPauseMarker(span) || dropped[span.Parent().SpanID()] {
			return true
		}

		parent, known := byID[span.Parent().SpanID()]
		if !known {
			return false
		}

		span = parent
	}

	return false
}

// pausedIDsPerSpan bounds the ids of dropped paused spans a trace remembers:
// this many per span of MaxSpansPerTrace. Past it, paused spans are held (and
// counted) as before, so the result stays right.
const pausedIDsPerSpan = 8

// dropPaused leaves out a span under a Pause marker as it arrives, before it
// counts against MaxSpansPerTrace. Its id is remembered, pointing at the
// marker, so its own children and logs still find their execution. A span
// with an exception is kept: exceptions under a pause are still recorded.
func (t *Translator) dropPaused(buf *traceBuf, span sdktrace.ReadOnlySpan) bool {
	parent := span.Parent().SpanID()

	marker, under := buf.paused[parent]
	if !under {
		held, known := buf.spans[parent]
		marker, under = parent, known && isPauseMarker(held)
	}

	if !under || len(buf.paused) >= pausedIDsPerSpan*t.cfg.MaxSpansPerTrace || hasException(span) {
		return false
	}

	if buf.paused == nil {
		buf.paused = map[trace.SpanID]trace.SpanID{}
	}

	buf.paused[span.SpanContext().SpanID()] = marker

	return true
}

func hasException(span sdktrace.ReadOnlySpan) bool {
	for _, event := range span.Events() {
		if event.Name == eventException {
			return true
		}
	}

	return false
}

// takeDropped removes the dropped paused ids whose marker is one of members
// and returns them.
func takeDropped(buf *traceBuf, members []sdktrace.ReadOnlySpan) map[trace.SpanID]bool {
	if len(buf.paused) == 0 {
		return nil
	}

	isMember := make(map[trace.SpanID]bool, len(members))
	for _, span := range members {
		isMember[span.SpanContext().SpanID()] = true
	}

	dropped := map[trace.SpanID]bool{}

	for id, marker := range buf.paused {
		if isMember[marker] {
			dropped[id] = true
			delete(buf.paused, id)
		}
	}

	return dropped
}

func isPauseMarker(span sdktrace.ReadOnlySpan) bool {
	return newAttrSet(span.Attributes()).bool(AttrPaused)
}
