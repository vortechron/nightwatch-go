package nightwatch

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func addException(span trace.Span, message string) {
	span.AddEvent("exception", trace.WithAttributes(
		attribute.String("exception.type", "*errors.errorString"),
		attribute.String("exception.message", message),
	))
}

func (h *harness) failedQuery(ctx context.Context, message string) {
	_, span := h.tracer.Start(ctx, "SELECT teams", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("db.system.name", "mysql"),
		attribute.String("db.query.text", "select * from teams where id = ?"),
		attribute.String("db.client.connection.pool.name", "mysql"),
	))
	addException(span, message)
	span.End()
}

func TestTranslator_AbortedRequestDropsOnlyTheCancelErrorsTheAbortCaused(t *testing.T) {
	h := newHarness(t, nil)

	h.request(context.Background(), 200, func(ctx context.Context, span trace.Span) {
		h.failedQuery(ctx, "context canceled")
		ReportHandled(ctx, fmt.Errorf("read team: select: %w", context.Canceled))
		ReportError(ctx, errors.New("task 7 not found"))
		addException(span, "context canceled")
		span.SetAttributes(attribute.Bool(AttrAborted, true))
	})

	mustValidate(t, h.sink.all())
	exceptions := h.sink.ofType("exception")
	if len(exceptions) != 1 || exceptions[0]["message"] != "task 7 not found" {
		t.Fatalf("want only the real error, got %v", exceptions)
	}
	requests := h.sink.ofType("request")
	if len(requests) != 1 || requests[0]["exceptions"] != 1 || len(h.sink.ofType("query")) != 1 {
		t.Fatalf("the request and its query are still recorded, counting one exception: %v", requests)
	}
}

func TestTranslator_CancelErrorInARequestTheClientDidNotAbortIsKept(t *testing.T) {
	h := newHarness(t, nil)

	// Code that cancels its own context has a bug, so the error still counts.
	h.request(context.Background(), 500, func(ctx context.Context, _ trace.Span) {
		ReportError(ctx, fmt.Errorf("load task: %w", context.Canceled))
	})

	if exceptions := h.sink.ofType("exception"); len(exceptions) != 1 {
		t.Fatalf("want the cancel error, got %v", exceptions)
	}
}
