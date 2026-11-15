# Environment Variables

The names are the same as in the PHP package. In a Goravel app the provider reads them through Goravel's config, so `.env` works, and then falls back to the process environment. `nightwatch.ConfigFromEnv` and `nightwatch.NewFromEnv` read the process environment only.

| Variable | Default | Meaning |
|---|---|---|
| `NIGHTWATCH_ENABLED` | `true` (needs a token) | The master switch. |
| `NIGHTWATCH_TOKEN` | none | The app token. Without a token the client is disabled. |
| `NIGHTWATCH_BASE_URL` | `https://nightwatch.laravel.com` | The Omni host. The ingest URL comes from `POST /api/agent-auth`, with `/api/omni/ingest` as the fallback. |
| `NIGHTWATCH_INGEST_URL` | none | Skip discovery and post here. Go only. |
| `NIGHTWATCH_DEPLOY` | empty | The deploy id. |
| `NIGHTWATCH_SERVER` | the hostname | The server label. |
| `NIGHTWATCH_REQUEST_SAMPLE_RATE` | `1.0` | The chance a request is kept, decided from the trace id. |
| `NIGHTWATCH_COMMAND_SAMPLE_RATE` | `1.0` | Commands, and jobs that no request started. |
| `NIGHTWATCH_SCHEDULED_TASK_SAMPLE_RATE` | `1.0` | Scheduled tasks. |
| `NIGHTWATCH_EXCEPTION_SAMPLE_RATE` | `1.0` | The chance a sampled-out execution with an unhandled exception is kept. |
| `NIGHTWATCH_IGNORE_QUERIES` | `false` | Skip query records. |
| `NIGHTWATCH_IGNORE_OUTGOING_REQUESTS` | `false` | Skip outgoing request records. |
| `NIGHTWATCH_IGNORE_CACHE_EVENTS` | `false` | Skip cache event records. |
| `NIGHTWATCH_IGNORE_MAIL` | `false` | Skip mail records. |
| `NIGHTWATCH_IGNORE_NOTIFICATIONS` | `false` | Skip notification records. |
| `NIGHTWATCH_IGNORE_PATHS` | empty | Comma-separated request path globs that are not recorded at all. See [Ignoring paths](filtering#ignoring-paths). |
| `NIGHTWATCH_IGNORE_SCHEDULED_TASKS` | empty | Comma-separated task names that are not recorded. See [Scheduled Tasks](scheduled-tasks#filtering). |
| `NIGHTWATCH_LOG_LEVEL` | `LOG_LEVEL` or `debug` | The lowest log level sent. |
| `NIGHTWATCH_REDACT_HEADERS` | the six always redacted | Extra header names to redact. |
| `NIGHTWATCH_REDACT_QUERY` | the defaults | Extra URL query parameter names to redact. |
| `NIGHTWATCH_CAPTURE_REQUEST_PAYLOAD` | `false` | Keep the JSON body of 500 responses, up to 64 KB, redacted. |
| `NIGHTWATCH_REDACT_PAYLOAD_FIELDS` | `_token,password,password_confirmation` | Fields blanked in a captured body, at any depth. |
| `NIGHTWATCH_CAPTURE_EXCEPTION_SOURCE_CODE` | `true` | Add source lines to exception frames. |

## Config fields without a variable

`nightwatch.Config` has more settings than the environment. Set them on the config you pass to the provider or to `nightwatch.New`.

| Field | Default | Meaning |
|---|---|---|
| `BatchSize`, `FlushInterval` | 500 records, 5 seconds | When the sender posts a batch. |
| `MaxBuffered` | 5000 | Records kept in memory. Past it the oldest are dropped and counted. |
| `MaxRetries`, `MaxRetryTime` | 5, 2 minutes | How long one batch is retried before it is dropped. |
| `TraceMaxWait` | 10 seconds | How long a trace waits for its root span to end. |
| `RootGrace` | 1 second | How long a finished execution waits for late logs and spans. |
| `MaxTraces`, `MaxSpansPerTrace`, `MaxOwners` | 2000, 2000, 50000 | Bounds on the translator's memory. |
| `HTTPClient`, `Logger` | safe defaults | Optional hooks. |
