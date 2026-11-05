package nightwatch

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	maxBackoff    = 30 * time.Second
	quotaPause    = 15 * time.Minute
	requestLimit  = 10 * time.Second
	fallbackPath  = "/api/omni/ingest"
	authPath      = "/api/agent-auth"
	fallbackRetry = time.Minute
)

// Sender batches records and posts them to Omni from a background goroutine.
// Enqueue never blocks and never does network work.
type Sender struct {
	cfg    Config
	client *http.Client
	log    *slog.Logger
	sleep  func(ctx context.Context, d time.Duration) error

	mu          sync.Mutex
	queue       []Record
	pausedUntil time.Time
	nowFn       func() time.Time
	started     bool
	stopping    bool

	dropped atomic.Int64
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	final   atomic.Pointer[context.Context]

	sendMu sync.Mutex
	target endpoint
}

// NewSender creates a sender. It does nothing until Start is called, and
// nothing at all when the config is disabled.
func NewSender(cfg Config) *Sender {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: requestLimit}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	s := &Sender{
		cfg:    cfg,
		client: client,
		log:    logger,
		nowFn:  time.Now,
		wake:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	s.sleep = s.waitBackoff

	return s
}

func (s *Sender) setClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nowFn = now
}

func (s *Sender) now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.nowFn()
}

// Start launches the background goroutine.
func (s *Sender) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started || !s.cfg.Enabled {
		return
	}

	s.started = true
	go s.loop()
}

// Enqueue adds records for sending. It drops the oldest records when more
// than MaxBuffered wait, and drops everything while the client is paused for
// a quota error.
func (s *Sender) Enqueue(records []Record) {
	if !s.cfg.Enabled || len(records) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.nowFn().Before(s.pausedUntil) {
		s.dropped.Add(int64(len(records)))

		return
	}

	s.queue = append(s.queue, records...)
	if excess := len(s.queue) - s.cfg.MaxBuffered; excess > 0 && s.cfg.MaxBuffered > 0 {
		s.queue = append([]Record(nil), s.queue[excess:]...)
		s.dropped.Add(int64(excess))
	}

	if len(s.queue) >= s.cfg.BatchSize {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// Pending is the number of records waiting to be sent.
func (s *Sender) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.queue)
}

// Dropped counts records lost to a full buffer, a quota pause or a rejected
// batch.
func (s *Sender) Dropped() int64 { return s.dropped.Load() }

func (s *Sender) loop() {
	defer close(s.done)

	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			s.flushAll(*s.final.Load())

			return
		case <-ticker.C:
			s.flushAll(context.Background())
		case <-s.wake:
			s.flushFull(context.Background())
		}
	}
}

// Flush sends everything that is queued now and waits for it.
func (s *Sender) Flush(ctx context.Context) {
	s.flushAll(ctx)
}

// Shutdown stops the goroutine after one last flush. It gives up when ctx
// ends and counts what was left as dropped.
func (s *Sender) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	started, already := s.started, s.stopping
	s.stopping = true
	s.mu.Unlock()

	if already {
		return nil
	}

	if !started {
		return nil
	}

	s.final.Store(&ctx)
	close(s.stop)

	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Sender) take(limit int) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.queue) == 0 || s.nowFn().Before(s.pausedUntil) {
		return nil
	}

	count := min(limit, len(s.queue))
	batch := append([]Record(nil), s.queue[:count]...)
	s.queue = append([]Record(nil), s.queue[count:]...)

	return batch
}

func (s *Sender) flushAll(ctx context.Context) {
	for ctx.Err() == nil {
		batch := s.take(s.cfg.BatchSize)
		if len(batch) == 0 {
			return
		}

		s.deliver(ctx, batch)
	}
}

func (s *Sender) flushFull(ctx context.Context) {
	for s.Pending() >= s.cfg.BatchSize {
		batch := s.take(s.cfg.BatchSize)
		if len(batch) == 0 {
			return
		}

		s.deliver(ctx, batch)
	}
}

// outcome says what to do after one POST.
type outcome int

const (
	outcomeSent outcome = iota
	outcomeRetry
	outcomeDrop
	outcomePause
)

// deliver posts one batch, retrying until it is sent, rejected or ctx ends.
// The compressed bytes and the Idempotency-Key are made once and reused, so a
// retry after a lost answer cannot double count.
func (s *Sender) deliver(ctx context.Context, batch []Record) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	body, err := encodeBatch(batch)
	if err != nil {
		s.log.Error("nightwatch: cannot encode batch", "error", err, "records", len(batch))
		s.dropped.Add(int64(len(batch)))

		return
	}

	key := uuid.NewString()

	for attempt := 0; ; attempt++ {
		switch s.post(ctx, body, key, len(batch)) {
		case outcomeSent:
			return
		case outcomeDrop:
			s.dropped.Add(int64(len(batch)))

			return
		case outcomePause:
			s.pause(len(batch))

			return
		}

		if err := s.sleep(ctx, backoff(attempt)); err != nil {
			s.dropped.Add(int64(len(batch)))
			s.log.Warn("nightwatch: giving up on a batch", "records", len(batch))

			return
		}
	}
}

func (s *Sender) pause(batchSize int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lost := batchSize + len(s.queue)
	s.queue = nil
	s.pausedUntil = s.nowFn().Add(quotaPause)
	s.dropped.Add(int64(lost))
	s.log.Warn("nightwatch: Omni event limit reached, pausing for 15 minutes", "dropped", lost)
}

func (s *Sender) post(ctx context.Context, body []byte, key string, records int) outcome {
	target := s.resolveEndpoint(ctx)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.url, bytes.NewReader(body))
	if err != nil {
		s.log.Error("nightwatch: bad ingest url", "error", err)

		return outcomeDrop
	}

	req.Header.Set("Authorization", "Bearer "+target.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("Nightwatch-Server", s.cfg.Server)
	req.Header.Set("User-Agent", "nightwatch-go")

	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Debug("nightwatch: send failed, will retry", "error", err)

		return outcomeRetry
	}

	defer resp.Body.Close()

	return s.classify(resp, records)
}

func (s *Sender) classify(resp *http.Response, records int) outcome {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))

	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return outcomeSent
	case code == http.StatusPaymentRequired:
		return outcomePause
	case code == http.StatusTooManyRequests || code >= 500:
		return outcomeRetry
	default:
		s.log.Warn("nightwatch: Omni rejected a batch, dropping it", "status", code, "records", records, "body", string(snippet))

		return outcomeDrop
	}
}

func encodeBatch(batch []Record) ([]byte, error) {
	var raw bytes.Buffer

	zipper := gzip.NewWriter(&raw)
	body := jsonString(map[string]any{"records": batch})

	if _, err := io.WriteString(zipper, body); err != nil {
		return nil, err //nolint:wrapcheck // in-memory writer
	}

	if err := zipper.Close(); err != nil {
		return nil, err //nolint:wrapcheck // in-memory writer
	}

	return raw.Bytes(), nil
}

// backoff is 1s, 2s, 4s ... capped at 30s, plus a little spread.
func backoff(attempt int) time.Duration {
	delay := time.Second << min(attempt, 5)
	if delay > maxBackoff {
		delay = maxBackoff
	}

	return delay + time.Duration(uuid.New().ID()%250)*time.Millisecond
}

func (s *Sender) waitBackoff(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-s.stop:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // plain context error
	}
}
