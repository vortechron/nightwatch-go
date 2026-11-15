# Differences from Laravel

## Not ported yet

- **Notifications are not recorded automatically.** `vortechron/notifications-go` has send hooks, but nightwatch-go is a public module and cannot import that private one. Until notifications-go calls `nightwatch.StartNotification` itself, wrap your sends with it. See [Notifications](notifications).
- **`Nightwatch::sampling()`** has no Go form: a context does not know the configured rate of its execution.

## Skip

- The local agent (`php artisan nightwatch:agent`, `nightwatch:status`, `NIGHTWATCH_INGEST_URI`, `NIGHTWATCH_INGEST_TIMEOUT`, `NIGHTWATCH_INGEST_CONNECTION_TIMEOUT`, `NIGHTWATCH_INGEST_EVENT_BUFFER`, `NIGHTWATCH_AGENT_LOG_LEVEL`). A Go process can batch and send in the background itself, so there is no agent to run.
- `Nightwatch::guzzleMiddleware`. Go uses `nwgoravel.Transport` on an `http.Client`.
- The hosting guides (Forge, Vapor, Cloud). They are about running the PHP agent.
- The `Nightwatch` facade. Go calls methods on the client (`nwgoravel.ClientFrom(app)`) and package functions.

## Different on purpose

- **Header redaction.** `NIGHTWATCH_REDACT_HEADERS` adds names to the six that are always redacted. PHP replaces its list. Redacted values are `[redacted]`. PHP writes `[N bytes redacted]` and keeps the auth scheme and cookie names.
- **URL query redaction.** Secret query values become `[redacted]` in request and outgoing request URLs. PHP sends the full URL.
- **`NIGHTWATCH_INGEST_URL`** skips `POST /api/agent-auth`. PHP has no such setting.
- **Sampling** is decided from the trace id, so a request and the jobs it queued agree. PHP rolls the dice per execution. `Sample(ctx, rate)` keeps this: the rate changes, the decision still comes from the trace id.
- **Vendor commands and cache keys are recorded by default.** Laravel leaves them out. Call `CaptureDefaultVendorCommands(false)` and `CaptureDefaultVendorCacheKeys(false)` to get Laravel's behaviour. The default keeps what an upgraded app records unchanged.
- **Built-in redaction runs after the callbacks.** The built-in header, query and payload redaction runs again after a callback, so a callback cannot put a secret back in. A callback that panics drops its record instead of sending it unredacted. A request callback's redacted path is also used in the `execution_preview` of the request's children; Laravel keeps the original path there.
- **Execution context** has its secret looking keys redacted, and an oversized context keeps the values that fit (with `_nightwatch_truncated`) instead of being cut, so it is always valid JSON. Laravel sends `Context::all()` as it is and cuts the text.
- **Aborted requests.** Their `context canceled` errors are not sent as exceptions. Go cancels the request context when the client leaves. PHP keeps running, so Laravel never reports these.
- **Payload capture** reads only JSON bodies up to 64 KB, and only for 500 responses.

## Unavoidable in Go

- **The context must be passed.** Go has no request-global state, so queries, cache calls, logs, mail and dispatches join their execution only through a `context.Context`. Goravel's `Queue().Job(...).Dispatch()` and `Mail().Send()` cannot be wrapped globally. For the same reason `Pause` and `Resume` return a context instead of switching a flag.
- **Context does not travel into jobs.** The job envelope carries the trace, the user and whether the job is sampled, not the `AddContext` data. Add what a job needs in the job.
- **A job queued from a request that `Sample` forced in** follows the configured request rate in the worker. Only a decision to drop (a paused context, `DontSample`, a rate that sampled the execution out) travels with the job.
- **Decoupling job sampling.** Goravel's worker has no `Queue::before` hook, so call `nightwatch.Sample` in each job's `HandleContext`.
- **Unmatched routes.** Goravel's fallback route takes no middleware, so call `nightwatch.Sample` in the fallback handler.
- **Reporting manually failed jobs.** Goravel has no `$this->fail()`. A job fails by returning an error. Call `nightwatch.ReportError` first to report a different error.
- **Cache key patterns** run on Go's `regexp`, which has no look-arounds or back-references. Such a pattern is compared as an exact key, as PHP does for an invalid pattern. The default vendor list is matched by hand, so it behaves exactly like Laravel's.
- **Stages.** `execution_stage` is always `action` for children, and the request's `bootstrap`, `render`, `sending` and `terminating` are 0. A Go server has no bootstrap per request, no separate render step and no terminating callbacks to time.
- **Exception classes** are Go types, such as `*errors.errorString`. A panic has the class `panic`.
- **Job attempts** are counted per job id in one worker, because Goravel does not pass the attempt number to `Handle` (the worker keeps it in a local variable).
- **Mail `mailer`** is `smtp` unless you pass a name, because Goravel's mailer has only that transport.
- **Query file and line** are absolute paths, like the exception frames. Laravel makes them relative to the app's base path.
- **`route_action`** is the Go handler function, such as `goravel/app/http/controllers.(*TaskController).Show`, not `Controller@method`.
- **Scheduler callbacks** have no context. Use `nwgoravel.TaskContext` inside them.
- **Goravel's own HTTP client** ends its span only when the response body is read or closed. A call whose body you never read is not recorded. See [Outgoing Requests](outgoing-requests#goravels-http-client).
- **Goravel's mailer** always sends SMTP AUTH, which a plain Mailpit refuses. The example's test uses a fake SMTP server.
