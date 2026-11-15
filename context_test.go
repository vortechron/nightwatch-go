package nightwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestContext_ManyKeysDoNotPushOutTheStatusCodeOrTheSampleRate(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RequestSampleRate = 0 })

	h.request(context.Background(), 500, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		for i := range 130 {
			AddContext(ctx, fmt.Sprintf("key_%03d", i), i)
		}
		Sample(ctx, 1)
	})

	requests := h.sink.ofType(RecordRequest)
	if len(requests) != 1 {
		t.Fatalf("Sample(ctx, 1) after 130 context keys must still keep the request: %v", h.sink.all())
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(requests[0]["context"].(string)), &parsed); err != nil || len(parsed) != 130 || parsed["key_129"] != float64(129) {
		t.Fatalf("context has %d keys, err %v", len(parsed), err)
	}

	if requests[0]["status_code"] != int64(500) {
		t.Fatalf("status_code = %#v, want 500", requests[0]["status_code"])
	}
}

func TestContext_AKeyReplacedOftenOrAddedFromManyGoroutinesKeepsItsLastValue(t *testing.T) {
	h := newHarness(t, nil)

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		AddContext(ctx, "steady", "kept")
		for i := range 1_000 {
			AddContext(ctx, "counter", i)
		}

		var wg sync.WaitGroup
		for worker := range 8 {
			wg.Go(func() {
				for i := range 50 {
					AddContext(ctx, fmt.Sprintf("w%d_%d", worker, i), i)
				}
			})
		}
		wg.Wait()
	})

	var parsed map[string]any
	if err := json.Unmarshal([]byte(h.sink.ofType(RecordRequest)[0]["context"].(string)), &parsed); err != nil {
		t.Fatal(err)
	}

	if len(parsed) != 402 || parsed["counter"] != float64(999) || parsed["steady"] != "kept" || parsed["w7_49"] != float64(49) {
		t.Fatalf("%d keys, counter %v, steady %v", len(parsed), parsed["counter"], parsed["steady"])
	}
}

func TestContext_TrimmingTwentyThousandSmallKeysIsFast(t *testing.T) {
	h := newHarness(t, nil)
	start := time.Now()

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		for i := range 20_000 {
			AddContext(ctx, fmt.Sprintf("key_%05d", i), i)
		}
	})

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("adding and trimming took %s; it must grow with the number of keys, not its square", elapsed)
	}

	got := h.sink.ofType(RecordRequest)[0]["context"].(string)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil || len(got) > 65_535 || len(got) < 65_535-32 || parsed["_nightwatch_truncated"] != true {
		t.Fatalf("context (%d bytes, %d keys) must be cut to just under the limit, err %v", len(got), len(parsed), err)
	}

	// The smallest values are kept: every one-digit value fits.
	if parsed["key_00000"] != float64(0) || parsed["key_00009"] != float64(9) {
		t.Fatalf("the smallest values must be kept: %v %v", parsed["key_00000"], parsed["key_00009"])
	}
}
