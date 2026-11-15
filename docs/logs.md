# Logs

## Introduction

Log records come from Goravel's `otel` log channel. Add the channel to your log stack, as shown in [Installation](installation#send-logs). A log line joins its execution when it is written with the execution's context:

```go
facades.Log().WithContext(reqCtx).Info("task loaded")
```

## Log level filtering

`NIGHTWATCH_LOG_LEVEL` sets the lowest level that is sent. It defaults to `LOG_LEVEL`, and to `debug` when neither is set.

```ini
NIGHTWATCH_LOG_LEVEL=warning
```

On a `nightwatch.Config`, `LogAllowed(level)` reports whether a level passes. Unknown levels always pass.

## Redaction

The log context is cleaned before it leaves the process:

- The attributes `request`, `response`, `user`, `owner`, `trace`, `headers`, `cookies`, `ctx`, `context` and `root` are dropped. Goravel's debug log attaches the request context under these names.
- A key that looks secret has its value replaced with `[redacted]`, at any depth. A key looks secret when it contains password, secret, token, authorization, cookie, api_key, credential or session, or is in one of the redact lists.
- Strings are cut at 2 KB, nesting at 4 levels and the whole context at 32 KB.
