# Exceptions

## Introduction

An exception record is made from an `exception` event on a span. Goravel's middleware records the errors it sees, and the Nightwatch middleware records panics with their stack.

## Manually reporting exceptions

`nightwatch.ReportError` reports an unhandled error, with the Go stack of the call site, like `Nightwatch::report()`. `nightwatch.ReportHandled` reports an error the application caught on purpose, which Omni marks as handled. These come from the example app:

```go
// Boom fails with an unhandled error and a 500.
func (c *TaskController) Boom(ctx http.Context) http.Response {
	err := errors.New("task could not be loaded")
	nightwatch.ReportError(ctx.Context(), err)

	return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
}

// Handled catches an error on purpose and reports it as handled.
func (c *TaskController) Handled(ctx http.Context) http.Response {
	nightwatch.ReportHandled(ctx.Context(), errors.New("optional step skipped"))

	return ctx.Response().Json(http.StatusOK, http.Json{"ok": true})
}
```

`nightwatch.RecordExceptionOnSpan(span, err, handled)` does the same on a span you hold.

## Exception classes

The exception `class` is the Go type, such as `*errors.errorString`. A `*fmt.wrapError` is unwrapped first. A panic has the class `panic`. A panic value that is neither an error nor a string is reported by its type only, so its contents cannot leak.

Each reported error counts once. The one event skipped is Goravel's own echo of an error this client already recorded with a stack. A stack-less exception, such as one Goravel's middleware recorded, uses the span name as its file and is grouped by class, span and message, so distinct errors do not collapse. Use `ReportError` for a real stack.

## Source code capture

For the first ten application frames whose file can be read, the record holds the source lines around the frame, five on each side. An application frame is a function of your program's main module, as for [query locations](queries#file-and-line). Frames from the standard library or from dependencies get none, and neither does a `-trimpath` build, whose paths are not files on disk. To turn this off:

```ini
NIGHTWATCH_CAPTURE_EXCEPTION_SOURCE_CODE=false
```

## Sampling

An execution that was sampled out but has an unhandled exception is kept with the chance `NIGHTWATCH_EXCEPTION_SAMPLE_RATE` (default `1.0`). Set it to `0` to capture exceptions only from sampled executions.

## Redaction

`RedactExceptions` lets you change an exception's message before it is sent, like `Nightwatch::redactExceptions()`. The execution's exception preview shows the redacted text too:

```go
client.RedactExceptions(func(exception *nightwatch.Exception) {
	exception.Message = strings.ReplaceAll(exception.Message, "secret", "***")
})
```

## Unrecoverable errors

When nightwatch-go cannot record or send your data, it cannot tell you through Omni. `HandleUnrecoverableErrorsUsing` registers a handler for these errors, like `Nightwatch::handleUnrecoverableExceptionsUsing()`: a filter, redaction or `OnIngesting` callback that panicked, and every batch the sender had to drop (Omni refused it, the retries ran out, the event quota was reached, or the client shut down first). The errors of dropped batches wrap `nightwatch.ErrBatchDropped`.

```go
client.HandleUnrecoverableErrorsUsing(func(err error) {
	// Write to a log channel that does not go to Nightwatch.
	facades.Log().Channel("single").Error("Nightwatch could not record events: " + err.Error())
})
```

A handler that panics is ignored. A later call replaces the handler. The error names the type of the record a callback panicked on, never its contents.

> [!WARNING]
> This may write many lines when Nightwatch is not configured right and fails on every request.
