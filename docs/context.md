# Context

## Introduction

Laravel Nightwatch shows the data of Laravel's `Context` on requests, jobs, commands and scheduled tasks. Go has no request-global context store, so nightwatch-go has `nightwatch.AddContext`: it adds a key and a value to the execution the `context.Context` belongs to, and Omni shows them in the execution's context.

```go
nightwatch.AddContext(ctx, "locale", "ms")
```

`AddContext` works with any context that belongs to an execution: the request context in a handler (`ctx.Context()`), a command's `console.Context`, a job's `HandleContext` context, or `nwgoravel.TaskContext` in a scheduled callback. Outside an execution it does nothing.

## Examples

### Augment user details

Add details that help you see which kind of user hit a problem:

```go
nightwatch.AddContext(ctx, "user", map[string]any{
	"role":              user.Role,
	"subscription_tier": user.SubscriptionTier,
})
```

### Track feature flags

```go
nightwatch.AddContext(ctx, "feature_flags", map[string]bool{
	"new_dashboard":   flags.Active("new_dashboard"),
	"enhanced_search": flags.Active("enhanced_search"),
})
```

### Debug multi-tenant issues

```go
nightwatch.AddContext(ctx, "tenant", map[string]any{
	"id":     tenant.ID,
	"name":   tenant.Name,
	"plan":   tenant.Plan,
	"region": tenant.Region,
})
```

## How values are stored

- A value is stored as JSON. A later `AddContext` with the same key replaces the value, as `Context::add` does.
- Add as many keys as you need. The whole context is one span attribute, so it never pushes the status code or the sample rate past OpenTelemetry's limit of 128 attributes a span.
- A value that cannot be encoded (a channel, a function) turns the whole context into `{"_nightwatch_error":"Failed to serialize context"}`, Laravel's own error object.
- A key that looks secret (it contains password, secret, token, authorization, cookie, api_key, credential or session, or is in one of the redact lists) has its value replaced with `[redacted]`, at any depth.

> [!NOTE]
> **Size limits.** Up to 64 KB of context is kept per execution. A larger context keeps the smallest values that fit and adds `"_nightwatch_truncated": true`, so it is always valid JSON. Laravel cuts the text instead.

## Jobs

Laravel's context travels into the jobs a request queues. nightwatch-go's does not, because the job envelope only carries the trace and the user. Add what a job needs in its `HandleContext`.
