# Cache

## Introduction

The provider wraps Goravel's cache. A call through `WithContext(ctx)` becomes a `cache-event` record of the execution in `ctx`:

```go
cache := facades.Cache().WithContext(reqCtx)
if !cache.Has("task:" + id) {
	_ = cache.Put("task:"+id, "seen", time.Minute)
}
```

Cache calls made without `WithContext(ctx)` are not recorded: with no request in scope there is no execution to attach them to.

## What is recorded

Only what Laravel's cache repository fires events for:

| Call | Event |
|---|---|
| `Get`, `GetString` and the other reads, `Has`, the read inside `Pull` and `Remember` | `hit` or `miss` |
| `Put`, `Forever`, the write inside `Remember` | `write` or `write-failure` |
| `Forget`, the delete inside `Pull` | `delete` or `delete-failure` |

`Add`, `Increment`, `Decrement` and `Lock` record nothing, as in Laravel. So a set-if-missing on an existing key never shows as a failed write.

`nwgoravel.Cache(ctx, driver, store)` wraps a single driver by hand, with `store` as the label shown in Omni.

## Filtering

`NIGHTWATCH_IGNORE_CACHE_EVENTS=true` drops every cache event record.
