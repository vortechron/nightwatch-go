package nightwatch

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSender_ShutdownDuringBackoffMakesOneFinalAttemptThenDrops(t *testing.T) {
	f := newFakeOmni(t)
	f.alwaysStatus = 503
	cfg := senderConfig(f)
	s := NewSender(cfg) // real backoff sleep (1s and up)
	s.Start()
	s.Enqueue(oneRecord(2))
	f.waitFor(t, 1)

	started := time.Now()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 1500*time.Millisecond {
		t.Fatalf("shutdown spun or waited out the backoff: %v", took)
	}
	if got := len(f.calls()); got != 2 {
		t.Fatalf("want first attempt plus one final attempt, got %d posts", got)
	}
	if s.Dropped() != 2 || s.Pending() != 0 {
		t.Fatalf("dropped %d pending %d", s.Dropped(), s.Pending())
	}
}

func TestSender_ShutdownGivesUpWhenContextEndsAndCountsTheRest(t *testing.T) {
	block := make(chan struct{})
	f := newFakeOmni(t)
	f.srv.Config.Handler = blockingHandler(block)
	defer close(block)
	cfg := senderConfig(f)
	cfg.FlushInterval = time.Hour
	s := NewSender(cfg)
	s.Start()
	s.Enqueue(oneRecord(3))

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := s.Shutdown(ctx); err == nil {
		t.Fatal("want the context error")
	}
	deadline := time.Now().Add(2 * time.Second)
	for s.Dropped() != 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Pending() != 0 || s.Dropped() != 3 {
		t.Fatalf("leftovers must be dropped and counted: pending %d dropped %d", s.Pending(), s.Dropped())
	}
}

func TestSender_RejectsEnqueueAfterShutdown(t *testing.T) {
	f := newFakeOmni(t)
	s := startSender(t, senderConfig(f))
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(oneRecord(4))
	if s.Pending() != 0 || s.Dropped() != 4 {
		t.Fatalf("pending %d dropped %d", s.Pending(), s.Dropped())
	}
}

func TestSender_ZeroMaxBufferedMeansDefaultBound(t *testing.T) {
	f := newFakeOmni(t)
	cfg := senderConfig(f)
	cfg.MaxBuffered = 0
	cfg.BatchSize = 0
	cfg.FlushInterval = 0
	s := NewSender(cfg) // not started
	s.Enqueue(oneRecord(defaultMaxBuffered + 100))
	if s.Pending() != defaultMaxBuffered || s.Dropped() != 100 {
		t.Fatalf("pending %d dropped %d", s.Pending(), s.Dropped())
	}
}

func TestSender_StuckBatchIsDroppedAfterMaxRetries(t *testing.T) {
	f := newFakeOmni(t)
	f.statuses = []int{503, 503, 503}
	cfg := senderConfig(f)
	cfg.BatchSize = 1
	cfg.MaxRetries = 2
	s := startSender(t, cfg)
	s.Enqueue(oneRecord(1))
	f.waitFor(t, 3)
	deadline := time.Now().Add(2 * time.Second)
	for s.Dropped() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Dropped() != 1 {
		t.Fatalf("stuck batch must be dropped and counted, dropped=%d", s.Dropped())
	}
	s.Enqueue(oneRecord(1))
	f.waitFor(t, 4) // the next batch goes out
}

func TestSender_HonoursRetryAfterOn429(t *testing.T) {
	f := newFakeOmni(t)
	f.statuses = []int{429}
	f.retryAfter = "7"
	s := NewSender(senderConfig(f))
	var mu sync.Mutex
	var waits []time.Duration
	s.sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()

		return nil
	}
	s.Start()
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	s.Enqueue(oneRecord(1))
	f.waitFor(t, 2)
	mu.Lock()
	defer mu.Unlock()
	if len(waits) == 0 || waits[0] < 7*time.Second {
		t.Fatalf("waits %v", waits)
	}
}

func TestRing_KeepsNewestAndOrder(t *testing.T) {
	r := newRing(3)
	for i := 0; i < 5; i++ {
		r.push(Record{"i": i})
	}
	if r.len() != 3 || r.at(0)["i"] != 2 {
		t.Fatalf("len %d first %v", r.len(), r.at(0))
	}
	got := r.pop(2)
	if len(got) != 2 || got[0]["i"] != 2 || got[1]["i"] != 3 || r.len() != 1 {
		t.Fatalf("pop: %v", got)
	}
	r.push(Record{"i": 5})
	r.push(Record{"i": 6})
	if got = r.pop(10); len(got) != 3 || got[2]["i"] != 6 {
		t.Fatalf("wraparound: %v", got)
	}
	if r.pop(1) != nil {
		t.Fatal("empty ring")
	}
	r.push(Record{"i": 7})
	r.clear()
	if r.len() != 0 {
		t.Fatal("clear")
	}
}
