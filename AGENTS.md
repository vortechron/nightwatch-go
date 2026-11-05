# nightwatch-go

An OpenTelemetry based Nightwatch client for Go and Goravel. It sends the same records as `laravel/nightwatch`
v1.30.3 to Omni. Read `README.md` for the design and the span to record table.

## Commands

```sh
golangci-lint run ./...                      # must report 0 issues
go vet ./...
go test -race -covermode=atomic -coverprofile=coverage.out ./... && sh scripts/coverage-gate.sh 80 coverage.out
cd example/goravel && go test -run TestExample .     # end to end; needs Redis DB 15
go run ./cmd/omni-capture -addr 127.0.0.1:2601 -dir ./capture
go run ./cmd/nwdiff -golden <laravel dir> -go <go dir>
```

Go 1.26, toolchain go1.27.3 (`GOTOOLCHAIN=auto`). Pin OpenTelemetry to the versions Goravel uses (otel 1.44, log 0.20).

## Layout

- Root package `nightwatch`: config, hash and `_group` helpers, translator (spans and logs to records), sender,
  instrumentation helpers. It must not import Goravel.
- `goravel/`: the Goravel adapter (provider, middleware, wrappers).
- `internal/omnirules`: port of Omni's ingest decoder plus storage rules. `internal/capture`, `internal/nwdiff`: tool logic.
- `cmd/`: thin mains. `example/goravel`: separate Go module with the end to end test.

## Rules

- Test first. Test through the public seams: `Translator`, `Sender`, `Client`, the helpers, the adapter wrappers.
- Functions under 50 lines, files 200 to 400 lines (800 max), no deep nesting. Wrap errors with `%w`.
- No debug prints, no hardcoded secrets. Return new values instead of changing inputs.
- Records must never hold `null`. `_group` is 32 hex or empty. Durations are microseconds, timestamps float seconds.
- Redact headers and payloads before data leaves the process. Payload capture stays off by default.
- The example and its test must start few processes and stop all of them (`t.Cleanup`). Goravel retries a failing
  job at once and without limit unless the job has `ShouldRetry`; always give example jobs one.
- No package level mutable state: the Goravel client lives in the container (`nwgoravel.BindingClient`), the memory
  reading in the Translator.
- Goravel keys service providers by their `%T` name. The provider is `NightwatchServiceProvider` on purpose: never export a type
  called `ServiceProvider` from a package named `goravel`, it would collide with every other such package.
- Do not push or tag without the owner asking.
