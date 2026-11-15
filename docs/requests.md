# Requests

## Introduction

A request record is made from the server span that Goravel's telemetry middleware opens. The Nightwatch middleware adds what OpenTelemetry's HTTP convention leaves out: the path and query, redacted headers, body sizes, the signed-in user, the redacted body of failed requests and panics with a stack trace. See [Installation](installation#add-the-http-middleware) for the wiring.

## Route name and action

The route path comes from `http.route`. The route name and the handler come from Goravel's route info (`ctx.Request().Info()`): `route_name` is the name given with `Name("tasks.show")`, and `route_action` is the handler function, such as `goravel/app/http/controllers.(*TaskController).Show`. Go's `-fm` suffix for a method value is cut. An unmatched route has neither.

They are set before the handlers run, so a handler that sets the `nightwatch.route.name` or `nightwatch.route.action` attribute (`nightwatch.AttrRouteName`, `nightwatch.AttrRouteAction`) on the request span still wins.

## Capture request headers

Headers are always captured, with secrets redacted at capture time. See [Redaction](filtering#redaction).

## Capture request payloads

`NIGHTWATCH_CAPTURE_REQUEST_PAYLOAD=true` keeps the body of requests that end with a 500. Only JSON bodies up to 64 KB are read. The fields in `NIGHTWATCH_REDACT_PAYLOAD_FIELDS` are blanked before the body leaves the middleware. Capture is off by default.

## Sampling

### Global sampling

`NIGHTWATCH_REQUEST_SAMPLE_RATE` sets the share of requests kept. When a request is sampled in, its queries, queued jobs, exceptions and outgoing requests are kept with it. The jobs a request queues follow its decision.

```ini
NIGHTWATCH_REQUEST_SAMPLE_RATE=0.1
```

### Route-based sampling

`nwgoravel.Sample(rate)` sets the rate of one route or route group, like Laravel's `Sample::rate()` middleware. `nwgoravel.SampleAlways()` keeps every request of the route and `nwgoravel.SampleNever()` keeps none.

```go
facades.Route().Middleware(nwgoravel.Sample(0.5)).Get("/users", users.Index)

facades.Route().Middleware(nwgoravel.SampleNever()).Group(func(router route.Router) {
	router.Get("/up", health.Show)
})
```

### Unmatched routes sampling

Goravel's fallback route takes no middleware, so set the rate for unmatched (404) requests in the fallback handler. A reduced rate is better than ignoring them all, because it still shows you the bot traffic you may want to block:

```go
facades.Route().Fallback(func(ctx http.Context) http.Response {
	nightwatch.Sample(ctx.Context(), 0.1)

	return ctx.Response().String(http.StatusNotFound, "not found")
})
```

### Dynamic sampling

`nightwatch.Sample(ctx, rate)` and `nightwatch.DontSample(ctx)` decide from anything the request knows, such as the user's role. See [Dynamic sampling](filtering#dynamic-sampling).

## Filtering

There are no filtering callbacks for requests, as in Laravel. To leave requests out, use sampling, or list static assets and health checks in `NIGHTWATCH_IGNORE_PATHS`. See [Ignoring paths](filtering#ignoring-paths).

## Redaction

`RedactRequests` lets you change a request's URL, IP address, headers and payload before it is sent, like `Nightwatch::redactRequests()`:

```go
client.RedactRequests(func(request *nightwatch.Request) {
	request.URL = strings.ReplaceAll(request.URL, "secret", "***")
	request.IP = strings.ReplaceAll(request.IP, "secret", "***")
	delete(request.Headers, "x-tenant")
})
```

`Headers` has lowercase names and values that are already redacted. `Payload` is the captured JSON body (only with `NIGHTWATCH_CAPTURE_REQUEST_PAYLOAD`, on a 500), or `nil`; setting it to `nil` drops it. The query, header and payload redaction run again on the result.

The children of the request (its queries, logs and other records) show the redacted path in their `execution_preview`, because the callback runs once, as soon as the request ends, before any child is sent. Laravel keeps the original path there.

## Aborted requests

When the browser leaves, or Inertia cancels a visit, Go cancels the request context. Every query still running then fails with `context canceled`. The request is still recorded, but those cancel errors are not sent as exceptions, because PHP keeps running when the client leaves and Laravel never reports them. Other errors of the same request still count.

## Stages

`execution_stage` is always `action` for children. The request's `bootstrap`, `render`, `sending` and `terminating` durations are 0, and `action` is the whole time. PHP measures each stage.
