package nightwatch

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// failingSender is a started sender whose server always answers 503, with
// retries that wait for a signal instead of for time.
func failingSender(t *testing.T, mutate func(*Sender)) (*Sender, *fakeOmni) {
	t.Helper()
	f := newFakeOmni(t)
	f.alwaysStatus = 503
	cfg := senderConfig(f)
	cfg.FlushInterval = 10 * time.Millisecond
	s := NewSender(cfg)
	if mutate != nil {
		mutate(s)
	}

	return s, f
}

// The rule: a batch whose last attempt failed and that is still pending gets
// exactly one more attempt during shutdown, then it is dropped and counted.
// It must hold whenever Shutdown arrives. These three tests pin the three
// moments, with no sleeping.

func TestShutdown_WhileTheBatchWaitsInBackoff(t *testing.T) {
	entered := make(chan struct{})
	s, f := failingSender(t, func(s *Sender) {
		s.sleep = func(_ context.Context, _ time.Duration) error {
			close(entered)
			<-s.stop // the real wait also ends when Shutdown closes stop

			return errStopping
		}
	})
	s.Enqueue(oneRecord(2))
	s.Start()
	<-entered // the first attempt failed and the sender is waiting to retry

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(f.calls()); got != 2 {
		t.Fatalf("want the first attempt plus exactly one final attempt, got %d posts", got)
	}
	if s.Dropped() != 2 || s.Pending() != 0 {
		t.Fatalf("dropped %d pending %d", s.Dropped(), s.Pending())
	}
}

func TestShutdown_RightAfterTheFailedAttemptBeforeTheBackoffStarts(t *testing.T) {
	var s *Sender
	var once sync.Once
	s, f := failingSender(t, func(sender *Sender) {
		s = sender
		s.sleep = func(context.Context, time.Duration) error {
			t.Error("shutdown already began: there must be no backoff wait")

			return nil
		}
		// Shutdown starts exactly between "the attempt failed" and "decide what
		// next", the moment the old code lost its final attempt.
		s.afterFailedAttempt = func() {
			once.Do(func() {
				go func() { _ = s.Shutdown(context.Background()) }()
				for !s.isStopping() {
					runtime.Gosched()
				}
			})
		}
	})
	s.Enqueue(oneRecord(3))
	s.Start()
	<-s.done

	if got := len(f.calls()); got != 2 {
		t.Fatalf("want the first attempt plus exactly one final attempt, got %d posts", got)
	}
	if s.Dropped() != 3 || s.Pending() != 0 {
		t.Fatalf("dropped %d pending %d", s.Dropped(), s.Pending())
	}
}

func TestShutdown_BeforeTheFirstAttemptTheFirstAttemptIsTheFinalOne(t *testing.T) {
	s, f := failingSender(t, func(s *Sender) {
		s.cfg.FlushInterval = time.Hour
		s.sleep = func(context.Context, time.Duration) error {
			t.Error("no backoff wait during shutdown")

			return nil
		}
	})
	s.Enqueue(oneRecord(4)) // no timer and no full batch: only the shutdown flush sends it
	s.Start()

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(f.calls()); got != 1 {
		t.Fatalf("want exactly one attempt, got %d posts", got)
	}
	if s.Dropped() != 4 || s.Pending() != 0 {
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
	waitUntil(t, "the in-flight batch to be counted", func() bool { return s.Dropped() == 3 })
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
	waitUntil(t, "the stuck batch to be dropped", func() bool { return s.Dropped() == 1 })
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
