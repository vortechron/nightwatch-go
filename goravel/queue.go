package goravel

import (
	"context"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/queue"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// ContextJob can be implemented by a job that wants the context of its
// attempt. Goravel's Handle(args ...any) carries none, so a job that does
// database or HTTP work implements HandleContext to keep that work on the
// job's trace.
type ContextJob interface {
	HandleContext(ctx context.Context, args ...any) error
}

// WrapQueue wraps every job passed to Register so its runs are recorded as
// job attempts. It is installed globally by the provider.
func WrapQueue(inner queue.Queue) queue.Queue {
	return &queueFacade{Queue: inner, attempts: newAttemptCounter()}
}

type queueFacade struct {
	queue.Queue
	attempts *attemptCounter
}

func (q *queueFacade) Register(jobs []queue.Job) {
	wrapped := make([]queue.Job, len(jobs))
	for i, job := range jobs {
		wrapped[i] = &tracedJob{Job: job, attempts: q.attempts}
	}

	q.Queue.Register(wrapped)
}

// Queue returns a queue whose Job(...).Dispatch() records a queued-job in the
// execution of ctx and sends the trace with the job, so the worker continues
// the same trace. Goravel's dispatch carries no context, so wrap per request:
//
//	nwgoravel.Queue(ctx, facades.Queue()).Job(&jobs.Send{}, args).OnQueue("mail").Dispatch()
func Queue(ctx context.Context, inner queue.Queue) queue.Queue {
	return &dispatchQueue{Queue: inner, ctx: ctx}
}

type dispatchQueue struct {
	queue.Queue
	ctx context.Context //nolint:containedctx // bound to one execution on purpose
}

func (q *dispatchQueue) Job(job queue.Job, args ...[]queue.Arg) queue.PendingJob {
	pending := &pendingJob{queue: q.Queue, ctx: q.ctx, job: job}
	if len(args) > 0 {
		pending.args = append([]queue.Arg(nil), args[0]...)
	}

	return pending
}

// pendingJob collects the options and builds the real pending job at Dispatch,
// when the connection and queue names are final and the envelope can be made.
type pendingJob struct {
	queue      queue.Queue
	ctx        context.Context //nolint:containedctx // bound to one execution on purpose
	job        queue.Job
	args       []queue.Arg
	delay      time.Time
	connection string
	queueName  string
}

func (p *pendingJob) Delay(at time.Time) queue.PendingJob {
	next := *p
	next.delay = at

	return &next
}

func (p *pendingJob) OnConnection(name string) queue.PendingJob {
	next := *p
	next.connection = name

	return &next
}

func (p *pendingJob) OnQueue(name string) queue.PendingJob {
	next := *p
	next.queueName = name

	return &next
}

func (p *pendingJob) Dispatch() error { return p.send(false) }

func (p *pendingJob) DispatchSync() error { return p.send(true) }

func (p *pendingJob) send(sync bool) error {
	info := nightwatch.JobInfo{Name: p.job.Signature(), Connection: p.connection, Queue: p.queueName}
	ctx, end := nightwatch.StartJobDispatch(p.ctx, info)

	args := append(append([]queue.Arg(nil), p.args...), queue.Arg{
		Type:  "string",
		Value: nightwatch.EncodeJobEnvelope(ctx, p.connection, p.queueName),
	})

	real := p.queue.Job(p.job, args)
	if p.connection != "" {
		real = real.OnConnection(p.connection)
	}

	if p.queueName != "" {
		real = real.OnQueue(p.queueName)
	}

	if !p.delay.IsZero() {
		real = real.Delay(p.delay)
	}

	var err error
	if sync {
		err = real.DispatchSync()
	} else {
		err = real.Dispatch()
	}

	end(err)

	return err //nolint:wrapcheck // passthrough of the queue error
}

// tracedJob records each run of a job as a job attempt.
type tracedJob struct {
	queue.Job
	attempts *attemptCounter
}

func (j *tracedJob) Handle(args ...any) (err error) {
	env, rest := splitEnvelope(args)
	attempt := j.attempts.next(env.JobID)

	ctx, end := nightwatch.StartJobAttempt(context.Background(), env,
		nightwatch.JobInfo{Name: j.Signature(), Connection: env.Connection, Queue: env.Queue}, attempt)

	defer func() {
		if recovered := recover(); recovered != nil {
			end(panicError(recovered))
			panic(recovered)
		}

		if err == nil {
			j.attempts.done(env.JobID)
		}

		end(err)
	}()

	if withContext, ok := j.Job.(ContextJob); ok {
		return withContext.HandleContext(ctx, rest...) //nolint:wrapcheck // passthrough of the job error
	}

	return j.Job.Handle(rest...) //nolint:wrapcheck // passthrough of the job error
}

// ShouldRetry keeps the job's own retry rule when it has one.
func (j *tracedJob) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	if retry, ok := j.Job.(queue.JobWithShouldRetry); ok {
		return retry.ShouldRetry(err, attempt)
	}

	return true, 0
}

// splitEnvelope removes the trailing envelope argument added by Queue().
func splitEnvelope(args []any) (nightwatch.JobEnvelope, []any) {
	if len(args) == 0 {
		return nightwatch.JobEnvelope{}, args
	}

	last, ok := args[len(args)-1].(string)
	if !ok || !nightwatch.IsJobEnvelope(last) {
		return nightwatch.JobEnvelope{}, args
	}

	env, _ := nightwatch.DecodeJobEnvelope(last)

	return env, args[:len(args)-1]
}

// attemptCounter numbers the runs of one job id inside this process. Goravel
// does not pass the attempt number to Handle, so retries on this worker are
// counted here. The map is bounded.
type attemptCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

const maxTrackedJobs = 10000

func newAttemptCounter() *attemptCounter {
	return &attemptCounter{counts: map[string]int{}}
}

func (a *attemptCounter) next(jobID string) int {
	if jobID == "" {
		return 1
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.counts) >= maxTrackedJobs {
		a.counts = map[string]int{}
	}

	a.counts[jobID]++

	return a.counts[jobID]
}

func (a *attemptCounter) done(jobID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.counts, jobID)
}
