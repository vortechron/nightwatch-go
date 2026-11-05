package nightwatch

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vortechron/nightwatch-go/internal/omnirules"
)

type received struct {
	header http.Header
	body   []byte
}

type fakeOmni struct {
	mu           sync.Mutex
	got          []received
	statuses     []int // consumed one per ingest call, then 200
	authBody     string
	retryAfter   string
	alwaysStatus int
	authCode     int
	auths        atomic.Int32
	srv          *httptest.Server
}

func newFakeOmni(t *testing.T) *fakeOmni {
	t.Helper()
	f := &fakeOmni{authCode: http.StatusNotFound}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)

	return f
}

func (f *fakeOmni) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/agent-auth" {
		f.auths.Add(1)
		w.WriteHeader(f.authCode)
		_, _ = io.WriteString(w, strings.ReplaceAll(f.authBody, "{base}", f.srv.URL))

		return
	}
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.got = append(f.got, received{header: r.Header.Clone(), body: raw})
	status := http.StatusOK
	if f.alwaysStatus != 0 {
		status = f.alwaysStatus
	}
	if len(f.statuses) > 0 {
		status, f.statuses = f.statuses[0], f.statuses[1:]
	}
	retryAfter := f.retryAfter
	f.mu.Unlock()
	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":"x"}`)
}

func (f *fakeOmni) calls() []received {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]received(nil), f.got...)
}

func (f *fakeOmni) waitFor(t *testing.T, n int) []received {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if got := f.calls(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("wanted %d calls, got %d", n, len(f.calls()))

	return nil
}

func gunzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func senderConfig(f *fakeOmni) Config {
	cfg := testConfig()
	cfg.BaseURL = f.srv.URL
	cfg.IngestURL = f.srv.URL + "/api/omni/ingest"
	cfg.FlushInterval = 20 * time.Millisecond
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	return cfg
}

func oneRecord(n int) []Record {
	out := make([]Record, n)
	for i := range out {
		out[i] = Record{"v": 1, "t": "log", "trace_id": "0b9e3f6a-6d0c-4d6e-9d77-1d2a5c0f8a11", "level": "info", "message": "m", "context": "{}", "extra": "{}"}
	}

	return out
}

func startSender(t *testing.T, cfg Config) *Sender {
	t.Helper()
	s := NewSender(cfg)
	s.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	s.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})

	return s
}

func TestSender_SendsGzipBatchWithHeaders(t *testing.T) {
	f := newFakeOmni(t)
	s := startSender(t, senderConfig(f))
	s.Enqueue(oneRecord(3))

	call := f.waitFor(t, 1)[0]
	if call.header.Get("Authorization") != "Bearer t" || call.header.Get("Content-Encoding") != "gzip" ||
		call.header.Get("Content-Type") != "application/json" || call.header.Get("Idempotency-Key") == "" ||
		call.header.Get("Nightwatch-Server") != "srv" {
		t.Fatalf("headers: %v", call.header)
	}
	var payload struct{ Records []map[string]any }
	if err := json.Unmarshal(gunzip(t, call.body), &payload); err != nil || len(payload.Records) != 3 {
		t.Fatalf("body: %v %v", err, payload)
	}
	if _, perr := omnirules.Validate(call.body, "gzip"); perr != nil {
		t.Fatalf("Omni would reject: %v", perr)
	}
}

func TestSender_BatchesAtSize(t *testing.T) {
	f := newFakeOmni(t)
	cfg := senderConfig(f)
	cfg.FlushInterval = time.Hour
	cfg.BatchSize = 5
	s := startSender(t, cfg)
	s.Enqueue(oneRecord(12))
	f.waitFor(t, 2)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := f.calls()
	if len(calls) != 3 {
		t.Fatalf("12 records at batch 5 should be 3 posts, got %d", len(calls))
	}
}

func TestSender_RetriesWithSameKeyAndBody(t *testing.T) {
	f := newFakeOmni(t)
	f.statuses = []int{503, 429}
	s := startSender(t, senderConfig(f))
	s.Enqueue(oneRecord(2))

	calls := f.waitFor(t, 3)
	if calls[0].header.Get("Idempotency-Key") != calls[2].header.Get("Idempotency-Key") || !bytes.Equal(calls[0].body, calls[2].body) {
		t.Fatal("retry must reuse key and bytes")
	}
}

func TestSender_DropsClientErrorsWithoutRetry(t *testing.T) {
	f := newFakeOmni(t)
	f.statuses = []int{422}
	s := startSender(t, senderConfig(f))
	s.Enqueue(oneRecord(2))
	f.waitFor(t, 1)
	s.Enqueue(oneRecord(1))
	calls := f.waitFor(t, 2)
	if calls[0].header.Get("Idempotency-Key") == calls[1].header.Get("Idempotency-Key") {
		t.Fatal("dropped batch must not be retried")
	}
	if s.Dropped() != 2 {
		t.Fatalf("dropped %d", s.Dropped())
	}
}

func TestSender_PausesOn402(t *testing.T) {
	f := newFakeOmni(t)
	f.statuses = []int{402}
	cfg := senderConfig(f)
	s := startSender(t, cfg)
	now := time.Now()
	s.setClock(func() time.Time { return now })
	s.Enqueue(oneRecord(2))
	f.waitFor(t, 1)
	waitUntil(t, "the quota pause to start", func() bool { return s.Dropped() == 2 })

	s.Enqueue(oneRecord(4)) // inside the pause: dropped
	if len(f.calls()) != 1 {
		t.Fatalf("must not send while paused, calls=%d", len(f.calls()))
	}
	if s.Dropped() != 6 {
		t.Fatalf("dropped %d", s.Dropped())
	}

	s.setClock(func() time.Time { return now.Add(16 * time.Minute) })
	s.Enqueue(oneRecord(1))
	f.waitFor(t, 2)
}

func TestSender_BoundedMemoryDropsOldest(t *testing.T) {
	f := newFakeOmni(t)
	cfg := senderConfig(f)
	cfg.MaxBuffered = 10
	s := NewSender(cfg) // not started: nothing drains
	first := oneRecord(8)
	first[0]["message"] = "oldest"
	s.Enqueue(first)
	s.Enqueue(oneRecord(8))
	if s.Dropped() != 6 || s.Pending() != 10 {
		t.Fatalf("dropped %d pending %d", s.Dropped(), s.Pending())
	}
	if s.ring.at(0)["message"] == "oldest" {
		t.Fatal("oldest record should be gone")
	}
}

func TestSender_EnqueueNeverBlocksOnSlowServer(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-block
	}))
	defer srv.Close()
	defer close(block)
	cfg := testConfig()
	cfg.IngestURL = srv.URL
	cfg.FlushInterval = 10 * time.Millisecond
	s := startSender(t, cfg)
	s.Enqueue(oneRecord(1))
	<-entered // the sender is now stuck inside a request
	done := make(chan struct{})
	go func() { s.Enqueue(oneRecord(100)); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Enqueue blocked")
	}
}

func TestSender_ShutdownFlushes(t *testing.T) {
	f := newFakeOmni(t)
	cfg := senderConfig(f)
	cfg.FlushInterval = time.Hour
	s := startSender(t, cfg)
	s.Enqueue(oneRecord(4))
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls()) != 1 {
		t.Fatalf("shutdown should flush, calls=%d", len(f.calls()))
	}
}

func TestSender_DiscoversIngestURL(t *testing.T) {
	f := newFakeOmni(t)
	f.authCode = 200
	f.authBody = `{"token":"fresh","expires_in":3600,"refresh_in":3000,"ingest_url":"{base}/custom/ingest"}`
	cfg := senderConfig(f)
	cfg.IngestURL = ""
	var path atomic.Value
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent-auth" {
			path.Store(r.URL.Path + "|" + r.Header.Get("Authorization"))
		}
		f.handle(w, r)
	})
	s := startSender(t, cfg)
	s.Enqueue(oneRecord(1))
	f.waitFor(t, 1)
	if path.Load() != "/custom/ingest|Bearer fresh" {
		t.Fatalf("got %v", path.Load())
	}
}

func TestSender_FallsBackWhenAuthFails(t *testing.T) {
	f := newFakeOmni(t) // agent-auth answers 404
	cfg := senderConfig(f)
	cfg.IngestURL = ""
	var path atomic.Value
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent-auth" {
			path.Store(r.URL.Path + "|" + r.Header.Get("Authorization"))
		}
		f.handle(w, r)
	})
	s := startSender(t, cfg)
	s.Enqueue(oneRecord(1))
	f.waitFor(t, 1)
	if path.Load() != "/api/omni/ingest|Bearer t" {
		t.Fatalf("got %v", path.Load())
	}
}

func TestSender_DisabledDoesNothing(t *testing.T) {
	cfg := testConfig()
	cfg.Enabled = false
	s := NewSender(cfg)
	s.Start()
	s.Enqueue(oneRecord(3))
	if s.Pending() != 0 {
		t.Fatal("disabled sender must not queue")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// waitUntil polls a condition. It never decides a result by elapsed time; the
// long limit only turns a hang into a failure.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

func blockingHandler(block chan struct{}) http.Handler {
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block })
}
