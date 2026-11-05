package goravel

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/propagation"

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
// job attempts. The provider installs it. Dispatch cannot be wrapped globally:
// Goravel's Job(...).Dispatch() carries no context, so it has no request to
// attach to. Use Queue(ctx, q) where a request is in scope.
func WrapQueue(inner queue.Queue) queue.Queue {
	return &queueFacade{Queue: inner, attempts: newAttemptCounter(), tracing: true}
}

// TolerateEnvelopes is WrapQueue for a disabled client: registered jobs only
// have the Nightwatch envelope argument removed, nothing is traced. A worker
// that does not trace must still accept jobs that a tracing dispatcher
// queued, or their Handle would receive an argument it never expected.
func TolerateEnvelopes(inner queue.Queue) queue.Queue {
	return &queueFacade{Queue: inner, attempts: newAttemptCounter()}
}

type queueFacade struct {
	queue.Queue
	attempts *attemptCounter
	tracing  bool
}

func (q *queueFacade) Register(jobs []queue.Job) {
	wrapped := make([]queue.Job, len(jobs))
	for i, job := range jobs {
		wrapped[i] = wrapJob(job, q.attempts, q.tracing)
	}

	q.Queue.Register(wrapped)
}

// Queue returns a queue whose Job(...).Dispatch() and Chain(...).Dispatch()
// record a queued-job in the execution of ctx and send the trace and the user
// with the job, so the worker continues the same trace. Goravel's dispatch
// carries no context (Go has no request-global state), so wrap per request:
//
//	nwgoravel.Queue(ctx, facades.Queue()).Job(&jobs.Send{}, args).OnQueue("mail").Dispatch()
//
// The worker must run the same provider: it strips the trailing envelope
// argument. A worker without it would pass that string to Handle.
//
// Outside a Nightwatch execution (the client is disabled, or ctx does not come
// from a request, command, job or scheduled task) it returns inner itself:
// no envelope argument, no span.
func Queue(ctx context.Context, inner queue.Queue) queue.Queue {
	if !nightwatch.Active(ctx) {
		return inner
	}

	return &dispatchQueue{Queue: inner, ctx: ctx, syncDrivers: &sync.Map{}}
}

type dispatchQueue struct {
	queue.Queue
	ctx         context.Context //nolint:containedctx // bound to one execution on purpose
	syncDrivers *sync.Map       // connection name to bool, so the driver is built once
}

func (q *dispatchQueue) Job(job queue.Job, args ...[]queue.Arg) queue.PendingJob {
	chainJob := queue.ChainJob{Job: job}
	if len(args) > 0 {
		chainJob.Args = args[0]
	}

	return &pendingJob{dispatch: q, jobs: []queue.ChainJob{chainJob}}
}

func (q *dispatchQueue) Chain(jobs []queue.ChainJob) queue.PendingJob {
	return &pendingJob{dispatch: q, jobs: jobs, chain: true}
}

// isSync tells whether a connection uses the sync driver, where the job runs
// inline and is not a job attempt of its own.
func (q *dispatchQueue) isSync(connection string) bool {
	if cached, ok := q.syncDrivers.Load(connection); ok {
		return cached.(bool) //nolint:forcetypeassert // only bools are stored
	}

	driver, err := q.Connection(connection)
	isSync := err == nil && driver != nil && driver.Driver() == queue.DriverSync
	q.syncDrivers.Store(connection, isSync)

	return isSync
}

// pendingJob collects the options and builds the real pending job at Dispatch,
// when the connection and queue names are final and the envelopes can be made.
type pendingJob struct {
	dispatch   *dispatchQueue
	jobs       []queue.ChainJob
	chain      bool
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

func (p *pendingJob) Dispatch() error {
	return p.send(false, p.dispatch.isSync(p.connection))
}

func (p *pendingJob) DispatchSync() error { return p.send(true, true) }

// send records one queued-job per job and builds the real pending job with an
// envelope added to each job's arguments.
func (p *pendingJob) send(forceSync, inline bool) error {
	jobs := make([]queue.ChainJob, len(p.jobs))
	ends := make([]nightwatch.EndFunc, len(p.jobs))

	for i, job := range p.jobs {
		info := nightwatch.JobInfo{Name: job.Job.Signature(), Connection: p.connection, Queue: p.queueName}
		ctx, end := nightwatch.StartJobDispatch(p.dispatch.ctx, info)
		ends[i] = end

		job.Args = append(append([]queue.Arg(nil), job.Args...), queue.Arg{
			Type:  "string",
			Value: nightwatch.EncodeJobEnvelope(ctx, p.connection, p.queueName, inline),
		})
		jobs[i] = job
	}

	err := p.realPending(jobs).dispatch(forceSync)

	for _, end := range ends {
		end(err)
	}

	return err
}

type dispatcher struct {
	pending queue.PendingJob
}

func (d dispatcher) dispatch(sync bool) error {
	if sync {
		return d.pending.DispatchSync()
	}

	return d.pending.Dispatch()
}

func (p *pendingJob) realPending(jobs []queue.ChainJob) dispatcher {
	var pending queue.PendingJob
	if p.chain {
		pending = p.dispatch.Queue.Chain(jobs)
	} else {
		pending = p.dispatch.Queue.Job(jobs[0].Job, jobs[0].Args)
	}

	if p.connection != "" {
		pending = pending.OnConnection(p.connection)
	}

	if p.queueName != "" {
		pending = pending.OnQueue(p.queueName)
	}

	if !p.delay.IsZero() {
		pending = pending.Delay(p.delay)
	}

	return dispatcher{pending: pending}
}

// tracedJob records each run of a job as a job attempt.
type tracedJob struct {
	queue.Job
	attempts *attemptCounter
	tracing  bool
}

func (j *tracedJob) Handle(args ...any) (err error) {
	env, rest := splitEnvelope(args)

	if !j.tracing {
		return j.Job.Handle(rest...) //nolint:wrapcheck // passthrough of the job error
	}

	if env.Sync {
		return j.runInline(env, rest)
	}

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

	return j.call(ctx, rest)
}

// runInline runs a job dispatched with the sync driver. It is not a job
// attempt (Nightwatch records none for the sync connection); its work joins
// the trace of the dispatching code.
func (j *tracedJob) runInline(env nightwatch.JobEnvelope, args []any) error {
	ctx := propagation.TraceContext{}.Extract(nightwatch.MarkActive(context.Background()), propagation.MapCarrier{"traceparent": env.TraceParent})

	return j.call(ctx, args)
}

func (j *tracedJob) call(ctx context.Context, args []any) error {
	if withContext, ok := j.Job.(ContextJob); ok {
		return withContext.HandleContext(ctx, args...) //nolint:wrapcheck // passthrough of the job error
	}

	return j.Job.Handle(args...) //nolint:wrapcheck // passthrough of the job error
}

// wrapJob traces a job. A job with its own retry rule keeps it. A job without
// one must not gain one: Goravel treats "has ShouldRetry" as "retry as often
// as it says", so the wrapper only offers ShouldRetry when the job has it.
func wrapJob(job queue.Job, attempts *attemptCounter, tracing bool) queue.Job {
	traced := &tracedJob{Job: job, attempts: attempts, tracing: tracing}
	if retry, ok := job.(queue.JobWithShouldRetry); ok {
		return &tracedRetryJob{tracedJob: traced, retry: retry}
	}

	return traced
}

type tracedRetryJob struct {
	*tracedJob
	retry queue.JobWithShouldRetry
}

// ShouldRetry delegates to the job's own rule.
func (j *tracedRetryJob) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	return j.retry.ShouldRetry(err, attempt)
}

// splitEnvelope removes the trailing envelope argument added by Queue(). Only
// the last argument is looked at, and only a string that passes the strict
// envelope check (tag, W3C traceparent, UUID job id, checksum) is removed.
func splitEnvelope(args []any) (nightwatch.JobEnvelope, []any) {
	if len(args) == 0 {
		return nightwatch.JobEnvelope{}, args
	}

	last, ok := args[len(args)-1].(string)
	if !ok {
		return nightwatch.JobEnvelope{}, args
	}

	env, valid := nightwatch.DecodeJobEnvelope(last)
	if !valid {
		return nightwatch.JobEnvelope{}, args
	}

	return env, args[:len(args)-1]
}

// attemptCounter numbers the runs of one job id inside this process.
// Goravel does not pass the attempt number to Handle (the worker keeps it in a
// local variable and only gives it to ShouldRetry, after the run), so retries
// on this worker are counted here. The map is bounded.
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
