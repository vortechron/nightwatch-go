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
// It works by starting and ending a marker span, so work started from the
// returned context sits under that marker and the translator leaves it out.
// Outside an execution, or when ctx is already paused, it returns ctx.
func Pause(ctx context.Context) context.Context {
	if !Active(ctx) || Paused(ctx) {
		return ctx
	}

	before := trace.SpanFromContext(ctx)
	paused, marker := tracer().Start(ctx, "nightwatch.pause", trace.WithAttributes(attribute.Bool(AttrPaused, true)))
	marker.End()

	return context.WithValue(paused, pauseKey{}, &pauseState{before: before})
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
// included. It is nil when no member is a marker.
func pausedSpans(members []sdktrace.ReadOnlySpan) map[trace.SpanID]bool {
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
		paused[id] = underMarker(span, byID)
	}

	return paused
}

func underMarker(span sdktrace.ReadOnlySpan, byID map[trace.SpanID]sdktrace.ReadOnlySpan) bool {
	for range maxAncestorDepth {
		if isPauseMarker(span) {
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

func isPauseMarker(span sdktrace.ReadOnlySpan) bool {
	return newAttrSet(span.Attributes()).bool(AttrPaused)
}
