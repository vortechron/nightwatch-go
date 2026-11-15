package nightwatch

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestSample_DontSampleDropsTheRequestAndSampleKeepsIt(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RequestSampleRate = 0; c.ExceptionSampleRate = 0 })

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		Sample(ctx, 1)
		h.query(ctx, "select 'kept'")
	})

	if len(h.sink.ofType(RecordRequest)) != 1 || len(h.sink.ofType(RecordQuery)) != 1 {
		t.Fatalf("Sample(ctx, 1) must keep the request whatever the global rate: %v", h.sink.all())
	}

	h2 := newHarness(t, nil)
	h2.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		DontSample(ctx)
		h2.query(ctx, "select 'dropped'")
	})

	if records := h2.sink.all(); len(records) != 0 {
		t.Fatalf("DontSample must drop the execution: %v", records)
	}
}

func TestSample_AnUnhandledExceptionStillRerollsWithTheExceptionRate(t *testing.T) {
	h := newHarness(t, nil)

	h.request(context.Background(), 500, func(ctx context.Context, span trace.Span) {
		DontSample(MarkActive(ctx))
		addException(span, "boom")
	})

	if len(h.sink.ofType(RecordRequest)) != 1 || len(h.sink.ofType(RecordException)) != 1 {
		t.Fatalf("like Laravel, an unhandled exception samples again with NIGHTWATCH_EXCEPTION_SAMPLE_RATE: %v", h.sink.all())
	}
}

func TestSample_RatesOutsideZeroToOneMeanZeroAndAPlainContextIsANoOp(t *testing.T) {
	h := newHarness(t, nil)

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		Sample(ctx, 1) // not marked active: no execution to change
		Sample(MarkActive(ctx), 1.5)
	})

	if len(h.sink.all()) != 0 {
		t.Fatalf("a rate above 1 is 0, as in Laravel: %v", h.sink.all())
	}
}
