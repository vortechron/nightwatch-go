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

### Rejecting keys

`RejectCacheKeys` drops the cache events of matching keys, like `Nightwatch::rejectCacheKeys()`. A key written as a PHP-style regular expression with delimiters is a pattern, and anything else is an exact key:

```go
client.RejectCacheKeys(
	"/^session:/",    // a pattern
	`#^lock-\d+$#i`, // other delimiters and the i, m, s, U and u flags work too
	"rate:global",    // an exact key
)
```

Patterns run on Go's `regexp`. A pattern it cannot compile (look-arounds, back-references, the `x` flag) is compared as an exact key, the way PHP treats an invalid pattern.

### Rejecting events

`RejectCacheEvents` drops the cache events for which the callback returns `true`, like `Nightwatch::rejectCacheEvents()`:

```go
client.RejectCacheEvents(func(event nightwatch.CacheEvent) bool {
	return event.Type == nightwatch.CacheMiss && strings.HasPrefix(event.Key, "optional:")
})
```

A rejected event is not sent and not counted. `NIGHTWATCH_IGNORE_CACHE_EVENTS=true` drops every cache event record.

### Vendor cache keys

Laravel leaves out the cache keys of framework and package internals, such as `illuminate:` and `telescope:`. `nightwatch.DefaultVendorCacheKeys()` returns that list in Laravel's notation. nightwatch-go records these keys by default, so upgrading changes nothing. To get Laravel's behaviour:

```go
client.CaptureDefaultVendorCacheKeys(false)
```

## Redaction

`RedactCacheEvents` lets you change the key before it is sent, like `Nightwatch::redactCacheEvents()`. The event's group is hashed from the redacted key, as in Laravel:

```go
client.RedactCacheEvents(func(event *nightwatch.CacheEvent) {
	event.Key = strings.ReplaceAll(event.Key, "secret", "***")
})
```
