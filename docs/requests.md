# Requests

## Introduction

A request record is made from the server span that Goravel's telemetry middleware opens. The Nightwatch middleware adds what OpenTelemetry's HTTP convention leaves out: the path and query, redacted headers, body sizes, the signed-in user, the redacted body of failed requests and panics with a stack trace. See [Installation](installation#add-the-http-middleware) for the wiring.

## Route name and action

The route path comes from `http.route`. Goravel reports no route name or action, so `route_name` and `route_action` are empty unless you set the `nightwatch.route.name` and `nightwatch.route.action` attributes (`nightwatch.AttrRouteName`, `nightwatch.AttrRouteAction`) on the request span yourself.

## Capture request headers

Headers are always captured, with secrets redacted at capture time. See [Redaction](filtering#redaction).

## Capture request payloads

`NIGHTWATCH_CAPTURE_REQUEST_PAYLOAD=true` keeps the body of requests that end with a 500. Only JSON bodies up to 64 KB are read. The fields in `NIGHTWATCH_REDACT_PAYLOAD_FIELDS` are blanked before the body leaves the middleware. Capture is off by default.

## Sampling

`NIGHTWATCH_REQUEST_SAMPLE_RATE` sets the share of requests kept. The jobs a request queues follow its decision. See [Sampling](filtering#sampling).

## Filtering

To drop static assets and health checks, list them in `NIGHTWATCH_IGNORE_PATHS`. See [Ignoring paths](filtering#ignoring-paths).

## Aborted requests

When the browser leaves, or Inertia cancels a visit, Go cancels the request context. Every query still running then fails with `context canceled`. The request is still recorded, but those cancel errors are not sent as exceptions, because PHP keeps running when the client leaves and Laravel never reports them. Other errors of the same request still count.

## Stages

`execution_stage` is always `action` for children. The request's `bootstrap`, `render`, `sending` and `terminating` durations are 0, and `action` is the whole time. PHP measures each stage.
