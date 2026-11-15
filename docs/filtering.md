# Filtering

## Introduction

As in `laravel/nightwatch`, data passes three gates before it leaves the process: sampling decides whether an execution is kept, filtering drops whole record types or paths, and redaction removes secrets.

## Sampling

Sampling applies to the entry points: requests, commands and scheduled tasks. When an entry point is sampled in, its whole trace is kept. When it is sampled out, nothing of it is sent. Every rate defaults to `1.0`, which keeps everything.

```ini
NIGHTWATCH_EXCEPTION_SAMPLE_RATE=1.0
NIGHTWATCH_COMMAND_SAMPLE_RATE=1.0
NIGHTWATCH_REQUEST_SAMPLE_RATE=0.1
```

| Variable | Applies to |
|---|---|
| `NIGHTWATCH_REQUEST_SAMPLE_RATE` | Requests |
| `NIGHTWATCH_COMMAND_SAMPLE_RATE` | Commands, and jobs that no request started |
| `NIGHTWATCH_SCHEDULED_TASK_SAMPLE_RATE` | Scheduled tasks |
| `NIGHTWATCH_EXCEPTION_SAMPLE_RATE` | The chance that a sampled-out execution with an unhandled exception is kept anyway |

The decision comes from the trace id, so a request and the jobs it queued always agree. PHP rolls the dice per execution.

## Filtering record types

These variables skip one record type. The execution's counters still count the skipped records.

```ini
NIGHTWATCH_IGNORE_QUERIES=true
NIGHTWATCH_IGNORE_OUTGOING_REQUESTS=true
NIGHTWATCH_IGNORE_CACHE_EVENTS=true
NIGHTWATCH_IGNORE_MAIL=true
NIGHTWATCH_IGNORE_NOTIFICATIONS=true
```

`NIGHTWATCH_LOG_LEVEL` sets the lowest log level that is sent. See [Logs](logs).

## Ignoring paths

`NIGHTWATCH_IGNORE_PATHS` lists request path globs that are not recorded at all: no request record, none of its queries, logs or exceptions, and nothing in the counters. Add more in code with `nwgoravel.WithIgnorePaths` on the middleware:

```ini
NIGHTWATCH_IGNORE_PATHS=/build/**,/favicon.ico
```

```go
nwgoravel.WithIgnorePaths("/build/**", "/up")
```

The rules:

- A pattern is matched against the whole path, case sensitive. The path is the decoded `URL.Path` the router routes on, which never holds the query string.
- `*` matches any characters except `/`, so it stays inside one segment. `**` matches any characters including `/`, at any depth.
- `/build/*` covers `/build/app.js` but not `/build/assets/app.js`. `/build/**` covers both. Neither covers `/build` itself.
- `/*.png` covers `/logo.png` only. Use `**.png` for every PNG at any depth.
- Nothing is cut or cleaned. `/favicon.ico%3Fanything` decodes to `/favicon.ico?anything`, which is not the favicon, so it is recorded. A path with a `.` or `..` segment, or an empty segment (`//`), is never ignored.

`nightwatch.PathIgnored(patterns, path)` runs the same match.

## Ignoring scheduled tasks

`NIGHTWATCH_IGNORE_SCHEDULED_TASKS` lists task names that are not recorded, with everything that runs inside them. See [Scheduled Tasks](scheduled-tasks).

## Redaction

- **Headers.** Authorization, Cookie, Proxy-Authorization, Set-Cookie, X-CSRF-TOKEN and X-XSRF-TOKEN are always redacted. `NIGHTWATCH_REDACT_HEADERS` adds more names. Values become `[redacted]`, at capture time and again before sending.
- **URL query values.** The values of password, password_confirmation, token, access_token, refresh_token, id_token, api_key, apikey, key, secret, client_secret, signature, code and auth become `[redacted]`, in any case. `NIGHTWATCH_REDACT_QUERY` adds names. The other parameters are kept, the keys are sorted, and a query that cannot be parsed is dropped.
- **Request payloads.** When payload capture is on, `NIGHTWATCH_REDACT_PAYLOAD_FIELDS` (default `_token,password,password_confirmation`) blanks those fields at any depth.
- **Log context.** The attributes `request`, `response`, `user`, `owner`, `trace`, `headers`, `cookies`, `ctx`, `context` and `root` are dropped. A key that looks secret has its value replaced with `[redacted]`. See [Logs](logs).

The helpers `nightwatch.RedactURL`, `nightwatch.RedactQuery`, `nightwatch.RedactHeadersJSON` and `nightwatch.RedactPayloadJSON` apply the same rules.

## What never reaches Omni

Records are built from the attributes the application sets on spans and log lines, and from nothing else.

- `context.Context` values are never read or copied.
- Nothing is printed with `%v` or `%+v`. A panic value that is not an error or a string is reported by its type only, so a panic with a request or a user inside cannot leak it.
- An exception carries the error message, the Go type, the stack and source lines. The message is whatever the application wrote, so do not put secrets in error text.
