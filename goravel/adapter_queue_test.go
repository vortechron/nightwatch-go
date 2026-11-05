package goravel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/goravel/framework/contracts/queue"

	nightwatch "github.com/vortechron/nightwatch-go"
)

type fakeJob struct {
	queue.Job
	got []any
	err error
}

func (j *fakeJob) Signature() string        { return "jobs.SendEmail" }
func (j *fakeJob) Handle(args ...any) error { j.got = args; return j.err }

type ctxJob struct {
	fakeJob
	ctx context.Context
}

func (j *ctxJob) HandleContext(ctx context.Context, args ...any) error {
	j.ctx = ctx
	j.got = args

	return nil
}

type fakePending struct {
	queue.PendingJob
	owner *fakeQueue
}

func (p *fakePending) OnConnection(c string) queue.PendingJob {
	p.owner.opts = append(p.owner.opts, "conn:"+c)

	return p
}

func (p *fakePending) OnQueue(q string) queue.PendingJob {
	p.owner.opts = append(p.owner.opts, "queue:"+q)

	return p
}

func (p *fakePending) Delay(time.Time) queue.PendingJob {
	p.owner.opts = append(p.owner.opts, "delay")

	return p
}
func (p *fakePending) Dispatch() error     { p.owner.dispatched++; return nil }
func (p *fakePending) DispatchSync() error { p.owner.synced++; return nil }

type fakeQueueDriver struct{ name string }

func (d fakeQueueDriver) Driver() string                        { return d.name }
func (d fakeQueueDriver) Pop(string) (queue.ReservedJob, error) { return nil, nil }
func (d fakeQueueDriver) Push(queue.Task, string) error         { return nil }

type fakeQueue struct {
	queue.Queue
	args       []queue.Arg
	chain      []queue.ChainJob
	opts       []string
	dispatched int
	synced     int
	registered []queue.Job
	drivers    map[string]string
	built      int
}

func (f *fakeQueue) Job(_ queue.Job, args ...[]queue.Arg) queue.PendingJob {
	if len(args) > 0 {
		f.args = args[0]
	}

	return &fakePending{owner: f}
}

func (f *fakeQueue) Chain(jobs []queue.ChainJob) queue.PendingJob {
	f.chain = jobs

	return &fakePending{owner: f}
}

func (f *fakeQueue) Connection(name string) (queue.Driver, error) {
	f.built++

	return fakeQueueDriver{name: f.drivers[name]}, nil
}

func (f *fakeQueue) Register(jobs []queue.Job) { f.registered = jobs }

func dispatchCtx(t *testing.T) (context.Context, func()) {
	t.Helper()
	ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "dispatcher"})
	ctx = nightwatch.SetUser(ctx, nightwatch.User{ID: "1"})

	return ctx, func() { end(0, nil) }
}

func TestQueue_DispatchCarriesATraceAndTheUserAndWorkerContinuesIt(t *testing.T) {
	out := setup(t)
	ctx, done := dispatchCtx(t)
	inner := &fakeQueue{}

	err := Queue(ctx, inner).Job(&fakeJob{}, []queue.Arg{{Type: "string", Value: "hello"}}).
		OnConnection("redis").OnQueue("mail").Delay(time.Now()).Dispatch()
	if err != nil || inner.dispatched != 1 || len(inner.opts) != 3 {
		t.Fatalf("dispatch: %v %v", err, inner.opts)
	}
	if len(inner.args) != 2 || inner.args[0].Value != "hello" {
		t.Fatalf("args: %v", inner.args)
	}
	envelope := inner.args[1].Value.(string)
	done()

	reg := &fakeQueue{}
	WrapQueue(reg).Register([]queue.Job{&fakeJob{err: errors.New("boom")}, &ctxJob{}})
	failing, withCtx := reg.registered[0], reg.registered[1]

	if err = failing.Handle("hello", envelope); err == nil {
		t.Fatal("job error must pass through")
	}
	_ = failing.Handle("hello", envelope)
	if err = withCtx.Handle("hello", envelope); err != nil {
		t.Fatal(err)
	}
	job := withCtx.(*tracedJob).Job.(*ctxJob)
	if len(job.got) != 1 || job.got[0] != "hello" || job.ctx == nil {
		t.Fatalf("the envelope must be stripped and HandleContext must get a context: %v", job.got)
	}

	attempts := out.ofType("job-attempt")
	if len(attempts) != 3 {
		t.Fatalf("attempts: %d", len(attempts))
	}
	if attempts[0]["status"] != "failed" || attempts[0]["queue"] != "mail" || attempts[0]["connection"] != "redis" ||
		attempts[1]["attempt"] != 2 || attempts[0]["user"] != "1" {
		t.Fatalf("attempt records: %v / %v", attempts[0], attempts[1])
	}
	if out.ofType("queued-job")[0]["job_id"] != attempts[0]["job_id"] {
		t.Fatal("the attempt must carry the job id of the dispatch")
	}
	if _, offers := failing.(queue.JobWithShouldRetry); offers {
		t.Fatal("a job without ShouldRetry must not gain one")
	}
}

func TestQueue_ChainGetsAnEnvelopePerJob(t *testing.T) {
	out := setup(t)
	ctx, done := dispatchCtx(t)
	inner := &fakeQueue{}
	jobs := []queue.ChainJob{
		{Job: &fakeJob{}, Args: []queue.Arg{{Type: "string", Value: "a"}}},
		{Job: &fakeJob{}},
	}

	if err := Queue(ctx, inner).Chain(jobs).OnQueue("q").Dispatch(); err != nil {
		t.Fatal(err)
	}
	done()

	if len(inner.chain) != 2 || len(inner.chain[0].Args) != 2 || len(inner.chain[1].Args) != 1 || len(jobs[0].Args) != 1 {
		t.Fatalf("every chained job needs its own envelope and the input must stay unchanged: %v", inner.chain)
	}
	first, _ := nightwatch.DecodeJobEnvelope(inner.chain[0].Args[1].Value.(string))
	second, _ := nightwatch.DecodeJobEnvelope(inner.chain[1].Args[0].Value.(string))
	if first.JobID == "" || first.JobID == second.JobID || first.TraceParent == "" {
		t.Fatalf("envelopes: %+v %+v", first, second)
	}
	if len(out.ofType("queued-job")) != 2 {
		t.Fatalf("one queued-job per chained job: %d", len(out.ofType("queued-job")))
	}
}

func TestQueue_SyncDispatchRunsInlineWithoutAJobAttempt(t *testing.T) {
	out := setup(t)
	ctx, done := dispatchCtx(t)
	inner := &fakeQueue{drivers: map[string]string{"inline": queue.DriverSync, "redis": "custom"}}
	q := Queue(ctx, inner)

	if err := q.Job(&fakeJob{}).OnConnection("inline").Dispatch(); err != nil || inner.dispatched != 1 {
		t.Fatalf("dispatch on a sync connection: %v", err)
	}
	syncEnvelope, _ := nightwatch.DecodeJobEnvelope(inner.args[len(inner.args)-1].Value.(string))
	if !syncEnvelope.Sync {
		t.Fatal("a sync connection must be flagged")
	}
	if err := q.Job(&fakeJob{}).OnConnection("redis").Dispatch(); err != nil {
		t.Fatal(err)
	}
	if env, _ := nightwatch.DecodeJobEnvelope(inner.args[len(inner.args)-1].Value.(string)); env.Sync {
		t.Fatal("a real queue is not sync")
	}
	if err := q.Job(&fakeJob{}).DispatchSync(); err != nil || inner.synced != 1 {
		t.Fatalf("DispatchSync: %v", err)
	}
	_ = q.Job(&fakeJob{}).OnConnection("inline").Dispatch()
	if inner.built != 2 {
		t.Fatalf("the driver of a connection must be built once, built %d times", inner.built)
	}

	reg := &fakeQueue{}
	WrapQueue(reg).Register([]queue.Job{&ctxJob{}})
	if err := reg.registered[0].Handle("x", encodeFor(ctx, t, true)); err != nil {
		t.Fatal(err)
	}
	done()

	if len(out.ofType("job-attempt")) != 0 {
		t.Fatalf("a sync job is not a job attempt: %v", out.ofType("job-attempt"))
	}
	if reg.registered[0].(*tracedJob).Job.(*ctxJob).ctx == nil {
		t.Fatal("the inline job still gets a context on the dispatching trace")
	}
}

func encodeFor(ctx context.Context, t *testing.T, sync bool) string {
	t.Helper()
	dctx, end := nightwatch.StartJobDispatch(ctx, nightwatch.JobInfo{Name: "j"})
	defer end(nil)

	return nightwatch.EncodeJobEnvelope(dctx, "c", "q", sync)
}

type retryJob struct{ fakeJob }

func (retryJob) ShouldRetry(error, int) (bool, time.Duration) { return false, time.Second }

func TestWrapJob_KeepsOwnRetryRule(t *testing.T) {
	wrapped := wrapJob(&retryJob{}, newAttemptCounter())
	retry, ok := wrapped.(queue.JobWithShouldRetry)
	if !ok {
		t.Fatal("must offer ShouldRetry")
	}
	if again, delay := retry.ShouldRetry(nil, 1); again || delay != time.Second {
		t.Fatal("must delegate")
	}
}

func TestSplitEnvelope_OnlyAStrictEnvelopeAtTheEndIsRemoved(t *testing.T) {
	setup(t)
	ctx, done := dispatchCtx(t)
	good := encodeFor(ctx, t, false)
	done()

	if env, rest := splitEnvelope([]any{"a", good}); env.JobID == "" || len(rest) != 1 {
		t.Fatal("a real envelope must be removed")
	}
	for name, args := range map[string][]any{
		"empty":                 nil,
		"plain args":            {"a", 2},
		"envelope not last":     {good, "a"},
		"lookalike":             {"a", "nwtp2|garbage|x|c|q|u|0|deadbeef"},
		"user string with tag":  {"nwtp2|just text"},
		"non string at the end": {"a", 5},
	} {
		if env, rest := splitEnvelope(args); env.JobID != "" || len(rest) != len(args) {
			t.Errorf("%s: arguments must stay as they are", name)
		}
	}
}

func TestAttemptCounter_Bounded(t *testing.T) {
	c := newAttemptCounter()
	if c.next("") != 1 || c.next("a") != 1 || c.next("a") != 2 {
		t.Fatal("count")
	}
	c.done("a")
	if c.next("a") != 1 {
		t.Fatal("done must reset")
	}
	for i := 0; i <= maxTrackedJobs; i++ {
		c.next(string(rune(i+1000)) + "x")
	}
	if len(c.counts) > maxTrackedJobs {
		t.Fatal("must stay bounded")
	}
}
