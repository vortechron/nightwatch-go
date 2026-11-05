package goravel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/goravel/framework/contracts/cache"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/mail"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/contracts/schedule"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// sink collects records sent by a translator.
type sink struct {
	mu      sync.Mutex
	records []nightwatch.Record
}

func (s *sink) Enqueue(records []nightwatch.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, records...)
}

func (s *sink) ofType(kind string) []nightwatch.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []nightwatch.Record
	for _, r := range s.records {
		if r.Type() == kind {
			out = append(out, r)
		}
	}

	return out
}

func setup(t *testing.T) *sink { return setupWith(t, nil) }

func setupWith(t *testing.T, mutate func(*nightwatch.Config)) *sink {
	t.Helper()
	cfg := nightwatch.ConfigFromEnv(func(k string) string {
		return map[string]string{"NIGHTWATCH_TOKEN": "t"}[k]
	}, "host")
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.RootGrace = 0
	if mutate != nil {
		mutate(&cfg)
	}
	out := &sink{}
	tr := nightwatch.NewTranslator(cfg, out)
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tr.SpanExporter()))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = tp.Shutdown(context.Background()) })

	return out
}

// ---- cache ----

// fakeDriver is a map backed cache. Like the real drivers it returns the
// default argument of Get when the key is missing.
type fakeDriver struct {
	cache.Driver
	data       map[string]any
	calls      []string
	failWrites bool // Put and Forever report a failed write
}

func (f *fakeDriver) Has(key string) bool {
	f.calls = append(f.calls, "has")
	_, ok := f.data[key]
	return ok
}

func (f *fakeDriver) Get(key string, def ...any) any {
	f.calls = append(f.calls, "get")
	if v, ok := f.data[key]; ok {
		return v
	}
	if len(def) > 0 {
		return def[0]
	}

	return nil
}

func (f *fakeDriver) Put(key string, v any, _ time.Duration) error {
	if f.failWrites {
		return errors.New("write failed")
	}
	f.data[key] = v

	return nil
}

// Add is set-if-missing, like Redis SETNX: false when the key exists.
func (f *fakeDriver) Add(key string, v any, _ time.Duration) bool {
	if _, exists := f.data[key]; exists {
		return false
	}
	f.data[key] = v

	return true
}

func (f *fakeDriver) Forever(key string, v any) bool {
	if f.failWrites {
		return false
	}
	f.data[key] = v

	return true
}
func (f *fakeDriver) Lock(string, ...time.Duration) cache.Lock { return nil }
func (f *fakeDriver) Forget(key string) bool {
	_, ok := f.data[key]
	delete(f.data, key)

	return ok
}
func (f *fakeDriver) Increment(string, ...int64) (int64, error) { return 1, nil }
func (f *fakeDriver) Decrement(string, ...int64) (int64, error) { return 0, errors.New("nope") }

func cacheTypes(out *sink) map[string]int {
	types := map[string]int{}
	for _, r := range out.ofType("cache-event") {
		types[r["type"].(string)]++
	}

	return types
}

func TestCache_HitAndMissComeFromTheGetResultNotFromHas(t *testing.T) {
	out := setup(t)
	ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "c"})
	inner := &fakeDriver{data: map[string]any{"a": "1"}}
	d := Cache(ctx, inner, "redis")

	if d.Get("zzz") != nil || d.Get("zzz", "fallback") != "fallback" || d.Get("a") != "1" {
		t.Fatal("Get must keep the driver semantics")
	}
	if d.GetString("a") != "1" || d.GetString("zzz", "x") != "x" || d.GetInt("zzz", 4) != 4 || d.GetInt64("zzz") != 0 || d.GetBool("zzz", true) != true {
		t.Fatal("typed getters")
	}
	if d.GetInt("a") != 1 || d.GetInt64("a") != 1 || d.GetBool("a") != true {
		t.Fatal("typed getters on a hit")
	}
	end(0, nil)

	for _, call := range inner.calls {
		if call == "has" {
			t.Fatal("Get must not call Has")
		}
	}
	types := cacheTypes(out)
	if types["miss"] != 6 || types["hit"] != 5 {
		t.Fatalf("types: %v", types)
	}
	if out.ofType("command")[0]["cache_events"] != 11 {
		t.Fatalf("one event per call: %v", out.ofType("command")[0]["cache_events"])
	}
}

type fakeCache struct {
	cache.Cache
	driver cache.Driver
}

func (f *fakeCache) WithContext(context.Context) cache.Driver { return f.driver }
func (f *fakeCache) Store(string) cache.Driver                { return &fakeStore{Driver: f.driver} }

type fakeStore struct{ cache.Driver }

func (f *fakeStore) WithContext(context.Context) cache.Driver { return f.Driver }

func TestWrapCache_WithContextIsTraced(t *testing.T) {
	out := setup(t)
	wrapped := WrapCache(&fakeCache{driver: &fakeDriver{data: map[string]any{}}})
	ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "c"})
	wrapped.WithContext(ctx).Has("x")
	wrapped.Store("file").WithContext(ctx).Has("y")
	end(0, nil)
	events := out.ofType("cache-event")
	if len(events) != 2 || events[1]["store"] != "file" && events[0]["store"] != "file" {
		t.Fatalf("events: %v", events)
	}
}

func TestTransport_RecordsOutgoingRequest(t *testing.T) {
	out := setup(t)
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()

	ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "c"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.URL+"/x", nil)
	resp, err := (&http.Client{Transport: Transport(nil)}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	end(0, nil)

	got := out.ofType("outgoing-request")
	if len(got) != 1 || got[0]["status_code"] != int64(200) || got[0]["execution_source"] != "command" {
		t.Fatalf("outgoing: %v", got)
	}
}

var (
	_ = mail.Content{}
	_ queue.Job
	_ schedule.Event
	_ console.Command
)
