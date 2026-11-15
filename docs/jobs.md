# Jobs

## Introduction

nightwatch-go records two things for a queued job: a `queued-job` record in the execution that dispatched it, and a `job-attempt` execution for each run in the worker. The worker's attempt continues the trace of the code that queued it, and reports the same user.

## Dispatching jobs

Goravel's `Queue().Job(...).Dispatch()` takes no context, so it cannot be wrapped globally. Wrap the queue per request with `nwgoravel.Queue`. This comes from the example app:

```go
queue := nwgoravel.Queue(ctx.Context(), facades.Queue())
err := queue.Job(&jobs.SendWelcome{}, []contractsqueue.Arg{{Type: "string", Value: "new.user@example.test"}}).
	OnQueue(facades.Config().GetString("queue.connections.redis.queue")).Dispatch()
```

`Chain(...).Dispatch()` is wrapped too. Outside an execution, `nwgoravel.Queue` returns the queue you gave it: no envelope, no span.

## Running jobs

The provider wraps every registered job, so each run is a job attempt. To keep the job's database and HTTP work on its trace, implement `nwgoravel.ContextJob`. Goravel's `Handle` has no context, so call `HandleContext` from it:

```go
// Handle is required by Goravel. HandleContext below is what runs: it has the
// context of the job attempt, so the work joins the trace of the request.
func (j *SendWelcome) Handle(args ...any) error {
	return j.HandleContext(context.Background(), args...)
}

func (j *SendWelcome) HandleContext(ctx context.Context, args ...any) error {
	if len(args) == 0 {
		return fmt.Errorf("send_welcome needs an address")
	}

	address, _ := args[0].(string)

	var rows []map[string]any
	if err := facades.Orm().WithContext(ctx).Query().Raw("select * from tasks where id = ?", 7).Scan(&rows); err != nil {
		return err
	}

	_ = facades.Cache().WithContext(ctx).Put("welcome:"+address, "sent", 0)
	facades.Log().WithContext(ctx).Info("sending welcome mail")

	return nwgoravel.Mail(ctx, facades.Mail()).To([]string{address}).Subject("Welcome").
		Content(mail.Content{Html: "<p>Welcome</p>"}).Send()
}
```

> [!WARNING]
> Goravel retries a failing job at once and without limit unless the job implements `ShouldRetry`. Give every job one.

## The job envelope

The trace travels as a trailing job argument, a string of the form `nwtp2|<traceparent>|<job id>|<connection>|<queue>|<user>|<sync>|<crc32>`. The wrapped job removes it before your `Handle` runs. Only a last argument that passes the strict check (the tag, a W3C traceparent, a UUID and the checksum) is removed, so a real job argument is never taken by mistake.

A trailing string is used because Goravel's `ConvertArgs` drops an argument with an unknown `Type`, and `Handle(args ...any)` is the only place the framework gives a job anything.

Every worker that runs these jobs must register the provider. A worker without it would pass the envelope string to `Handle`. When the client is disabled, the provider still installs `nwgoravel.TolerateEnvelopes`, so a worker that does not trace still removes the envelope that a tracing dispatcher added.

## Attempts

The attempt number is counted per job id in one worker, because Goravel passes it neither to `Handle` nor anywhere a wrapper can see. A job queued with the sync driver is not a job attempt: its work stays part of the dispatching execution, as in Nightwatch.

## Sampling

There is no rate of its own for jobs: a job is captured when the execution that queued it is captured. A job queued by a request follows the request's sampling decision. A job that no request started follows `NIGHTWATCH_COMMAND_SAMPLE_RATE`.

A job queued from a paused context (see [Inline filtering](filtering#inline-filtering)), or from an execution that `nightwatch.Sample` or `nightwatch.DontSample` dropped, carries an unsampled `traceparent`. As in Laravel, its attempts then have a sample rate of 0 in the worker: an attempt that succeeds is dropped, and one that fails is sampled again with `NIGHTWATCH_EXCEPTION_SAMPLE_RATE` (default `1.0`), so the failure and its exception reach Omni. This works under Goravel's default parent-based sampler too: the attempt is recorded, and the rate decides at the end. A `nightwatch.Sample` call inside the job still wins, and jobs it queues stay sampled out.

### Sampling specific jobs

While not recommended, you may give one job its own rate by calling `nightwatch.Sample` at the start of its `HandleContext`:

```go
func (j *Report) HandleContext(ctx context.Context, args ...any) error {
	nightwatch.Sample(ctx, 0.5) // keep half of the runs

	return nil
}
```

### Decoupling job sampling

Laravel decouples every job with `Queue::before(fn () => Nightwatch::sample(...))`. Goravel's worker has no before hook, so call `nightwatch.Sample` in the `HandleContext` of each job you want to decouple. This may lead to a captured job whose request was not captured, or the other way round, which gives a less complete view.

## Filtering

`RejectQueuedJobs` drops the `queued-job` record of the jobs for which the callback returns `true`, like `Nightwatch::rejectQueuedJobs()`. The job's attempts are separate executions and are still recorded, as in Laravel:

```go
client.RejectQueuedJobs(func(job nightwatch.QueuedJob) bool {
	return job.Name == "jobs.LowPriority"
})
```

## Redaction

There are no redaction callbacks for jobs, as in Laravel.

## Reporting manually failed jobs

Goravel has no `$this->fail()`: a job fails by returning an error, and that error is recorded as an exception and makes the attempt `failed`. To report a different error than the one you return, report it first:

```go
func (j *Import) HandleContext(ctx context.Context, args ...any) error {
	if err := importRows(ctx); err != nil {
		nightwatch.ReportError(ctx, err)

		return errors.New("import failed")
	}

	return nil
}
```
