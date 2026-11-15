# Differences from Laravel

## Not ported yet

- **Filter and redaction callbacks.** There is no `Nightwatch::rejectQueries`, `rejectCacheKeys`, `rejectCacheEvents`, `rejectMail`, `rejectNotifications`, `rejectOutgoingRequests` or `rejectQueuedJobs`, and no `redactRequests`, `redactQueries`, `redactCommands`, `redactCacheEvents`, `redactMail`, `redactOutgoingRequests` or `redactExceptions`. Today a record type is all or nothing, through the `NIGHTWATCH_IGNORE_*` variables.
- **Inline filtering.** There is no `Nightwatch::ignore`, `pause` or `resume`.
- **Dynamic sampling.** There is no `Nightwatch::sample` or `dontSample`, no route-based rates and no rate for unmatched or package routes. The rates are global.
- **Context.** Laravel's `Context` data is not added to records.
- **Rate limiting.** There is no `IngestingEvents` event to cap ingestion.
- **Unrecoverable exceptions.** There is no `handleUnrecoverableExceptionsUsing`. Read `client.Stats()` for dropped records.
- **Vendor defaults.** There is no `defaultVendorCommands` or `defaultVendorCacheKeys` list: every command and every cache key is recorded.
- **Reporting manually failed jobs** (`$this->fail()`) has no Go form: return an error from the job.

## Skip

- The local agent (`php artisan nightwatch:agent`, `nightwatch:status`, `NIGHTWATCH_INGEST_URI`, `NIGHTWATCH_INGEST_TIMEOUT`, `NIGHTWATCH_INGEST_CONNECTION_TIMEOUT`, `NIGHTWATCH_INGEST_EVENT_BUFFER`, `NIGHTWATCH_AGENT_LOG_LEVEL`). A Go process can batch and send in the background itself, so there is no agent to run.
- `Nightwatch::guzzleMiddleware`. Go uses `nwgoravel.Transport` on an `http.Client`.
- The hosting guides (Forge, Vapor, Cloud). They are about running the PHP agent.

## Different on purpose

- **Header redaction.** `NIGHTWATCH_REDACT_HEADERS` adds names to the six that are always redacted. PHP replaces its list. Redacted values are `[redacted]`. PHP writes `[N bytes redacted]` and keeps the auth scheme and cookie names.
- **URL query redaction.** Secret query values become `[redacted]` in request and outgoing request URLs. PHP sends the full URL.
- **`NIGHTWATCH_INGEST_URL`** skips `POST /api/agent-auth`. PHP has no such setting.
- **Sampling** is decided from the trace id, so a request and the jobs it queued agree. PHP rolls the dice per execution.
- **Aborted requests.** Their `context canceled` errors are not sent as exceptions. Go cancels the request context when the client leaves. PHP keeps running, so Laravel never reports these.
- **Payload capture** reads only JSON bodies up to 64 KB, and only for 500 responses.

## Unavoidable in Go

- **The context must be passed.** Go has no request-global state, so queries, cache calls, logs, mail and dispatches join their execution only through a `context.Context`. Goravel's `Queue().Job(...).Dispatch()` and `Mail().Send()` cannot be wrapped globally.
- **Stages.** `execution_stage` is always `action` for children, and the request's `bootstrap`, `render`, `sending` and `terminating` are 0.
- **Exception classes** are Go types, such as `*errors.errorString`. A panic has the class `panic`.
- **Job attempts** are counted per job id in one worker, because Goravel does not pass the attempt number to `Handle`.
- **Mail `mailer`** is `smtp` unless you pass a name, because Goravel's mailer has only that transport.
- **Query `file` and `line`** are empty, because Goravel's database spans do not carry the caller.
- **`route_name` and `route_action`** are empty unless you set the `nightwatch.route.name` and `nightwatch.route.action` attributes.
- **Scheduler callbacks** have no context. Use `nwgoravel.TaskContext` inside them.
- **Goravel's own HTTP client** did not emit spans in the example app. Use `nwgoravel.Transport`.
- **Goravel's mailer** always sends SMTP AUTH, which a plain Mailpit refuses. The example's test uses a fake SMTP server.
