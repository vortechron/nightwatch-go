package nightwatch

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// runClient sends one request with a query and a user through a real client
// and fake Omni, after register has set up the hooks.
func runClient(t *testing.T, f *fakeOmni, register func(*Client)) *Client {
	t.Helper()
	client := New(senderConfig(f))
	register(client)
	client.Start()

	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(client.SpanExporter()))
	h := &harness{tp: tp, tracer: tp.Tracer("t"), sink: &captureSink{}}
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		SetUser(ctx, User{ID: "7", Name: "Ali"})
		h.query(ctx, "select 1")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	return client
}

func TestIngesting_AListenerThatReturnsFalseCancelsTheBatch(t *testing.T) {
	f := newFakeOmni(t)
	var counts []int
	client := runClient(t, f, func(c *Client) {
		c.OnIngesting(func(e IngestingEvents) bool {
			counts = append(counts, e.EventCount())

			return false
		})
	})

	if len(f.calls()) != 0 {
		t.Fatalf("a cancelled batch must not be posted, got %d posts", len(f.calls()))
	}

	// request + query; the user record does not count, as in Laravel.
	if len(counts) != 1 || counts[0] != 2 {
		t.Fatalf("event counts = %v", counts)
	}

	if stats := client.Stats(); stats.Refused != 3 || stats.Dropped != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestIngesting_ListenersThatAllowSendTheBatchUnchanged(t *testing.T) {
	f := newFakeOmni(t)
	runClient(t, f, func(c *Client) {
		c.OnIngesting(func(e IngestingEvents) bool {
			e.Records[0]["t"] = "tampered"

			return true
		})
	})

	if body := string(gunzip(t, f.waitFor(t, 1)[0].body)); strings.Contains(body, "tampered") || !strings.Contains(body, "select 1") {
		t.Fatalf("listeners see a copy; batch = %s", body)
	}
}

func TestIngesting_AListenerCannotChangeNestedListsThatAreSent(t *testing.T) {
	f := newFakeOmni(t)
	runClient(t, f, func(c *Client) {
		c.OnIngesting(func(e IngestingEvents) bool {
			for _, record := range e.Records {
				if methods, ok := record["route_methods"].([]string); ok && len(methods) > 0 {
					methods[0] = "TAMPERED"
				}
			}

			return true
		})
	})

	if body := string(gunzip(t, f.waitFor(t, 1)[0].body)); strings.Contains(body, "TAMPERED") || !strings.Contains(body, `"route_methods":["GET"]`) {
		t.Fatalf("listeners see a deep copy; batch = %s", body)
	}
}

func TestIngesting_APanickingListenerDropsTheBatchAndIsReported(t *testing.T) {
	f := newFakeOmni(t)
	var mu sync.Mutex
	var errs []error
	runClient(t, f, func(c *Client) {
		c.HandleUnrecoverableErrorsUsing(func(err error) { mu.Lock(); errs = append(errs, err); mu.Unlock() })
		c.OnIngesting(func(IngestingEvents) bool { panic("limiter down") })
	})

	mu.Lock()
	defer mu.Unlock()
	if len(f.calls()) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "limiter down") {
		t.Fatalf("posts %d, errors %v", len(f.calls()), errs)
	}
}

func TestUnrecoverable_ADroppedBatchIsReported(t *testing.T) {
	f := newFakeOmni(t)
	f.alwaysStatus = 422
	var mu sync.Mutex
	var errs []error
	runClient(t, f, func(c *Client) {
		c.HandleUnrecoverableErrorsUsing(func(err error) { mu.Lock(); errs = append(errs, err); mu.Unlock() })
	})

	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 1 || !errors.Is(errs[0], ErrBatchDropped) || !strings.Contains(errs[0].Error(), "422") {
		t.Fatalf("errors = %v", errs)
	}
}
