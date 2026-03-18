# Nightwatch Go

A Go APM client for [Nightwatch](https://nightwatch.laravel.com) (and compatible backends like Omni). Captures HTTP requests, database queries, outgoing HTTP calls, exceptions, and logs — then sends them to your Nightwatch dashboard.

This is a Go port of the [Laravel Nightwatch](https://github.com/laravel/nightwatch) PHP package, targeting Go web frameworks (Gin, Echo, Chi) instead of Laravel.

## Quick Start

```go
package main

import (
    "github.com/gin-gonic/gin"
    nightwatch "github.com/laravel/nightwatch-go"
    "github.com/laravel/nightwatch-go/gormhook"
    "github.com/laravel/nightwatch-go/middleware/ginmw"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
)

func main() {
    // 1. Initialize Nightwatch
    nw, _ := nightwatch.New(
        nightwatch.WithToken("your-nightwatch-token"),
        nightwatch.WithBaseURL("https://your-nightwatch-dashboard.com"),
        nightwatch.WithDeployment("my-go-app"),
    )

    // 2. Initialize GORM
    db, _ := gorm.Open(sqlite.Open("app.db"), &gorm.Config{})
    gormhook.Register(db, nw, gormhook.WithConnectionName("sqlite"))

    // 3. Set up Gin with Nightwatch middleware
    r := gin.Default()
    r.Use(ginmw.Middleware(nw))

    r.GET("/users", func(c *gin.Context) {
        var users []User
        // Pass request context to GORM — this links queries to the HTTP request
        db.WithContext(c.Request.Context()).Find(&users)
        c.JSON(200, users)
    })

    r.Run(":8080")
}
```

That's it. Every HTTP request, database query, and error is now captured and sent to your Nightwatch dashboard with full timeline stitching.

## Installation

```bash
go get github.com/laravel/nightwatch-go
```

For GORM support:

```bash
go get github.com/laravel/nightwatch-go/gormhook
```

## Configuration

### Functional Options

```go
nw, err := nightwatch.New(
    nightwatch.WithToken("your-token"),             // Required
    nightwatch.WithBaseURL("https://nightwatch.example.com"), // HTTP direct mode
    nightwatch.WithDeployment("my-app-v1.2.3"),     // Deployment identifier
    nightwatch.WithServer("web-01"),                 // Server name (default: hostname)
    nightwatch.WithBufferSize(500),                  // Record buffer size (default: 500)
    nightwatch.WithRequestSampleRate(1.0),           // 0.0-1.0 (default: 1.0 = 100%)
    nightwatch.WithExceptionSampleRate(1.0),         // 0.0-1.0
    nightwatch.WithLogLevel("info"),                 // Minimum log level to capture
    nightwatch.WithIgnoreQueries(false),             // Skip query capture
    nightwatch.WithIgnoreOutgoingRequests(false),     // Skip outgoing HTTP capture
    nightwatch.WithRedactHeaders("authorization", "cookie"), // Headers to redact
    nightwatch.WithDebug(true),                      // Log payloads to stderr
)
```

### Environment Variables

All options can be set via environment variables (options take precedence):

| Variable | Default | Description |
|----------|---------|-------------|
| `NIGHTWATCH_TOKEN` | — | API token (required) |
| `NIGHTWATCH_BASE_URL` | — | Dashboard URL for HTTP direct mode |
| `NIGHTWATCH_DEPLOY` | — | Deployment identifier |
| `NIGHTWATCH_SERVER` | `hostname` | Server name |
| `NIGHTWATCH_INGEST_URI` | `127.0.0.1:2407` | TCP agent address (when not using HTTP direct) |
| `NIGHTWATCH_REQUEST_SAMPLE_RATE` | `1.0` | Request sampling rate |
| `NIGHTWATCH_EXCEPTION_SAMPLE_RATE` | `1.0` | Exception sampling rate |

## Ingestion Modes

### HTTP Direct Mode (recommended for Go)

Sends telemetry directly to the Nightwatch backend over HTTPS. No local agent needed. Set `WithBaseURL()` or `NIGHTWATCH_BASE_URL` to enable.

```go
nw, _ := nightwatch.New(
    nightwatch.WithToken("your-token"),
    nightwatch.WithBaseURL("https://nightwatch.example.com"),
)
```

The client authenticates via `POST /api/agent-auth`, receives a temporary Bearer token and ingest URL, then sends gzip-compressed JSON payloads to the ingest endpoint.

### TCP Agent Mode

Sends telemetry to a local Nightwatch agent via TCP (the same protocol the PHP package uses). This is the default when `BaseURL` is not set.

```go
nw, _ := nightwatch.New(
    nightwatch.WithToken("your-token"),
    nightwatch.WithAddress("127.0.0.1:2407"),
)
```

Requires the Nightwatch agent running locally (`php artisan nightwatch:agent`).

## Framework Middleware

### Gin

```go
import "github.com/laravel/nightwatch-go/middleware/ginmw"

r := gin.Default()
r.Use(ginmw.Middleware(nw))
```

### Chi

```go
import "github.com/laravel/nightwatch-go/middleware/chimw"

r := chi.NewRouter()
r.Use(chimw.Middleware(nw))
```

The middleware captures:
- HTTP method, URL, route pattern, status code
- Request/response sizes
- Execution stage durations (before_middleware, action, after_middleware)
- Panic recovery with exception reporting

## Database Query Tracking (GORM)

```go
import "github.com/laravel/nightwatch-go/gormhook"

db, _ := gorm.Open(sqlite.Open("app.db"), &gorm.Config{})

gormhook.Register(db, nw,
    gormhook.WithConnectionName("sqlite"),     // Connection name in dashboard
    gormhook.WithConnectionType("write"),       // "read", "write", or "" (optional)
    gormhook.WithBasePath("/app"),              // Strip from file paths (optional)
)
```

**Important:** Always pass the request context to GORM so queries are linked to HTTP requests:

```go
func listUsers(c *gin.Context) {
    var users []User
    db.WithContext(c.Request.Context()).Find(&users)  // ← context links query to request
    c.JSON(200, users)
}
```

Each query record includes:
- SQL statement
- Duration in microseconds
- Source file and line number (auto-detected, skips GORM/framework internals)
- Connection name and type
- Execution stage (which phase of the request lifecycle the query ran in)

## Outgoing HTTP Tracking

Wrap your `http.Client` transport:

```go
import "github.com/laravel/nightwatch-go/transport"

client := &http.Client{
    Transport: transport.New(nw),
}

// Use the client normally — outgoing requests are tracked
resp, err := client.Get("https://api.example.com/data")
```

Each outgoing request record includes method, URL (with credentials stripped), status code, duration, and request/response sizes.

## Log Capture

Wrap your `slog` handler:

```go
import "github.com/laravel/nightwatch-go/loghandler"

handler := loghandler.New(nw, slog.NewJSONHandler(os.Stdout, nil))
logger := slog.New(handler)

// Logs are captured as telemetry AND passed through to the underlying handler
logger.InfoContext(ctx, "user created", "user_id", 123)
```

Logs are mapped to PSR-3 levels: `slog.Debug` → `debug`, `slog.Info` → `info`, `slog.Warn` → `warning`, `slog.Error` → `error`.

**Important:** Use `InfoContext`/`ErrorContext` (not `Info`/`Error`) and pass the request context so logs are linked to the HTTP request.

## Error Reporting

### Explicit Error Reporting

```go
func handler(c *gin.Context) {
    err := doSomething()
    if err != nil {
        nw.ReportError(c.Request.Context(), err)
        c.JSON(500, gin.H{"error": "something went wrong"})
        return
    }
}
```

### Panic Recovery

The middleware automatically captures panics as unhandled exceptions and returns a 500 response:

```go
r.GET("/dangerous", func(c *gin.Context) {
    panic("something broke")  // Captured automatically, returns 500
})
```

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│  Your Go Application                                     │
│                                                          │
│  ┌──────────┐  ┌──────────┐  ┌────────────┐            │
│  │   Gin    │  │  GORM    │  │ http.Client│            │
│  │Middleware│  │  Hook    │  │ Transport  │            │
│  └────┬─────┘  └────┬─────┘  └─────┬──────┘            │
│       │              │              │                    │
│       ▼              ▼              ▼                    │
│  ┌──────────────────────────────────────────┐           │
│  │         Nightwatch Core                   │           │
│  │  ┌────────────┐  ┌───────────────────┐   │           │
│  │  │   Buffer   │  │  ExecutionState   │   │           │
│  │  │ (500 recs) │  │  (per-request in  │   │           │
│  │  │            │  │   context.Context) │   │           │
│  │  └─────┬──────┘  └───────────────────┘   │           │
│  │        │                                  │           │
│  │        ▼                                  │           │
│  │  ┌────────────┐    ┌────────────────┐    │           │
│  │  │ HTTPIngest │ OR │  TCP Ingest    │    │           │
│  │  │ (direct)   │    │  (via agent)   │    │           │
│  │  └─────┬──────┘    └──────┬─────────┘    │           │
│  └────────┼──────────────────┼──────────────┘           │
└───────────┼──────────────────┼──────────────────────────┘
            │                  │
            ▼                  ▼
   Nightwatch Backend     Local Agent → Backend
```

### How Records Are Linked

Every HTTP request gets a unique `trace_id` (UUID) stored in `context.Context`. All child records (queries, logs, exceptions, outgoing requests) inherit this `trace_id` via the context, enabling the dashboard to stitch them into a timeline:

```
Request (trace_id: abc-123)
├── Query: INSERT INTO tasks ... (execution_id: abc-123, stage: action)
├── Query: SELECT * FROM users ... (execution_id: abc-123, stage: action)
├── OutgoingRequest: GET https://api.example.com (execution_id: abc-123, stage: action)
└── Log: "task created" (execution_id: abc-123, stage: action)
```

## Wire Protocol Compatibility

The Go client produces the exact same wire format as the PHP package:

- **xxh128 hashing** — verified byte-for-byte against PHP's `hash('xxh128', ...)` for token hashing and record grouping
- **TCP protocol** — `{length}:v1:{tokenHash}:{json}` with `2:OK` acknowledgment
- **HTTP payload** — `{"records": [{...}, {...}]}` with gzip compression, matching the PHP agent's `StreamBuffer.pull()` format
- **Record schemas** — all field names, types, and values match the PHP sensors

## Package Structure

```
nightwatch-go/
├── nightwatch.go          # Public API: New(), BeginRequest(), EndRequest(), ReportError()
├── config.go              # Config + functional options + env var fallbacks
├── context.go             # context.Context keys for trace propagation
├── http_ingest.go         # HTTP direct ingestion (auth + gzip + send)
├── ingest.go              # TCP ingestion (wire protocol)
├── payload.go             # Wire format: {length}:v1:{tokenHash}:{json}
├── buffer.go              # Thread-safe ring buffer (FIFO overflow)
├── state.go               # Per-request ExecutionState
├── hash.go                # xxh128 hashing (PHP-compatible)
├── stage.go               # ExecutionStage constants
├── record.go              # Record type constants
├── str.go                 # String truncation (TinyText/Text/MediumText)
├── responsewriter.go      # http.ResponseWriter wrapper
│
├── sensor/                # Record builders
│   ├── query.go           # Query records + SQL normalization
│   ├── outgoing.go        # Outgoing HTTP request records
│   ├── exception.go       # Exception records + stack traces
│   └── log.go             # Log records
│
├── gormhook/              # GORM integration
│   └── gorm.go            # Callback-based query instrumentation
│
├── middleware/
│   ├── ginmw/             # Gin middleware
│   ├── chimw/             # Chi middleware
│   └── echomw/            # Echo middleware (placeholder)
│
├── transport/
│   └── roundtripper.go    # http.RoundTripper wrapper
│
├── loghandler/
│   └── slog.go            # slog.Handler wrapper
│
├── example/               # Full CRUD example app
├── cmd/testserver/         # Test server for E2E testing
└── test.sh                # E2E test script
```

## Running the Example

```bash
cd nightwatch-go

# Set your credentials
export NIGHTWATCH_TOKEN="your-token"
export NIGHTWATCH_BASE_URL="https://your-dashboard.com"

# Run the CRUD example
go run ./example/

# Or run the E2E test
bash ./test.sh
```

## Testing

```bash
go test ./... -v
```

The test suite includes:
- **xxh128 hash compatibility** — verified against PHP reference values
- **Wire protocol format** — payload encoding and length calculation
- **Buffer behavior** — overflow, pull, flush
- **TCP integration** — mock agent round-trip with acknowledgment
- **Graceful degradation** — connection failures don't crash the app

## License

MIT
