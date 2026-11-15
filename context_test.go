package nightwatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func TestContext_AddContextFillsTheExecutionContext(t *testing.T) {
	h := newHarness(t, nil)

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		AddContext(ctx, "tenant", map[string]any{"id": 7, "plan": "pro"})
		AddContext(ctx, "flags", []string{"new_dashboard"})
		AddContext(ctx, "locale", "ms")
		AddContext(ctx, "locale", "en") // a later add replaces the value, as in Laravel
		h.query(ctx, "select 1")
	})

	request := h.sink.ofType(RecordRequest)[0]
	if request["context"] != `{"flags":["new_dashboard"],"locale":"en","tenant":{"id":7,"plan":"pro"}}` {
		t.Fatalf("context = %v", request["context"])
	}

	mustValidate(t, h.sink.all())
}

func TestContext_CommandsJobsAndTasksCarryItToo(t *testing.T) {
	h := newHarness(t, nil)

	for _, kind := range []string{RecordCommand, RecordJobAttempt, RecordScheduledTask} {
		ctx, span := h.execution(context.Background(), kind, attribute.String(AttrCommandName, "c"))
		AddContext(MarkActive(ctx), "kind", kind)
		span.End()

		if got := h.sink.ofType(kind)[0]["context"]; got != `{"kind":"`+kind+`"}` {
			t.Fatalf("%s context = %v", kind, got)
		}
	}
}

func TestContext_SecretLookingKeysAreRedactedAtAnyDepth(t *testing.T) {
	h := newHarness(t, nil)

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		AddContext(ctx, "user", map[string]any{"role": "admin", "api_token": "abc", "nested": map[string]any{"password": "abc"}})
		AddContext(ctx, "session_id", "abc")
	})

	if got := h.sink.ofType(RecordRequest)[0]["context"].(string); strings.Contains(got, "abc") || !strings.Contains(got, "admin") {
		t.Fatalf("context = %s", got)
	}
}

func TestContext_AnOversizedContextStaysValidJSONWithinTheLimit(t *testing.T) {
	h := newHarness(t, nil)

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		AddContext(ctx, "a", "small")
		AddContext(ctx, "b", strings.Repeat("x", 70_000))
	})

	got := h.sink.ofType(RecordRequest)[0]["context"].(string)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil || len(got) > 65_535 || parsed["a"] != "small" || parsed["b"] != nil {
		t.Fatalf("context (%d bytes) = %.200s, err %v", len(got), got, err)
	}

	if parsed["_nightwatch_truncated"] != true {
		t.Fatalf("a cut context must say so: %v", parsed)
	}
}

func TestContext_AValueThatCannotBeEncodedGivesLaravelsErrorObject(t *testing.T) {
	h := newHarness(t, nil)

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		AddContext(ctx, "ok", 1)
		AddContext(ctx, "bad", make(chan int))
	})

	if got := h.sink.ofType(RecordRequest)[0]["context"]; got != `{"_nightwatch_error":"Failed to serialize context"}` {
		t.Fatalf("context = %v", got)
	}
}

func TestContext_OutsideAnExecutionAddContextDoesNothing(_ *testing.T) {
	AddContext(context.Background(), "k", "v") // must not panic
}
