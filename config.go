package nightwatch

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL       = "https://nightwatch.laravel.com"
	defaultBatchSize     = 500
	defaultFlushInterval = 5 * time.Second
	defaultTraceMaxWait  = 10 * time.Second
	defaultMaxBuffered   = 5000
	defaultMaxRetries    = 5
	defaultMaxRetryTime  = 2 * time.Minute
	defaultRootGrace     = time.Second
	defaultMaxTraces     = 2000
	defaultMaxSpans      = 2000
	defaultMaxOwners     = 50000
)

// logLevels lists PSR-3 levels from the least to the most severe.
var logLevels = []string{"debug", "info", "notice", "warning", "error", "critical", "alert", "emergency"}

// Config holds every setting of the client. Build it with ConfigFromEnv and
// change fields on the copy you get back.
type Config struct {
	Enabled bool
	Token   string
	// BaseURL is the Omni host. IngestURL, when set, skips discovery.
	BaseURL   string
	IngestURL string
	Deploy    string
	Server    string

	RequestSampleRate       float64
	CommandSampleRate       float64
	ExceptionSampleRate     float64
	ScheduledTaskSampleRate float64

	IgnoreCacheEvents      bool
	IgnoreMail             bool
	IgnoreNotifications    bool
	IgnoreOutgoingRequests bool
	IgnoreQueries          bool
	LogLevel               string
	// RedactHeaders always includes the mandatory secrets; env adds more.
	RedactHeaders []string
	// IgnorePaths are request path globs that are not recorded at all (static
	// assets, health checks). Empty by default. See PathIgnored for the syntax:
	// "*" stays in one path segment, "**" goes any depth ("/build/**").
	IgnorePaths []string
	// IgnoreScheduledTasks are names of scheduled tasks that are not recorded,
	// with everything that runs inside them (NIGHTWATCH_IGNORE_SCHEDULED_TASKS).
	IgnoreScheduledTasks []string
	// RedactQuery lists query parameters whose values are replaced in URLs.
	// It always includes the defaults; NIGHTWATCH_REDACT_QUERY adds names.
	RedactQuery []string
	// CaptureRequestPayload keeps the body of failed (500) requests. Off by
	// default, like the PHP package. RedactPayloadFields are blanked first.
	CaptureRequestPayload bool
	// CaptureExceptionSourceCode adds the lines around each frame (five on
	// each side, for the first ten application frames) when the file can be
	// read. On by default, like the PHP package.
	CaptureExceptionSourceCode bool
	RedactPayloadFields        []string

	// BatchSize and FlushInterval trigger a send. MaxBuffered bounds memory:
	// past it the oldest records are dropped and counted.
	BatchSize     int
	FlushInterval time.Duration
	MaxBuffered   int
	// MaxRetries is how often one batch is sent again after a retryable
	// failure before it is dropped and counted. MaxRetryTime bounds the same
	// thing by time. Without a cap one stuck batch would block all later ones.
	MaxRetries   int
	MaxRetryTime time.Duration

	// TraceMaxWait is how long the translator holds a trace's spans while it
	// waits for the root execution span to end.
	TraceMaxWait time.Duration
	// RootGrace holds a finished execution this long before it is sent, so
	// logs and spans that finish just after the root are still counted.
	RootGrace time.Duration
	// MaxTraces, MaxSpansPerTrace and MaxOwners bound the translator's memory.
	MaxTraces        int
	MaxSpansPerTrace int
	MaxOwners        int

	// Optional hooks. Nil values use safe defaults.
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// ConfigFromEnv reads the NIGHTWATCH_* variables (same names as the PHP
// package). getenv is usually os.Getenv. hostname is the default server name.
func ConfigFromEnv(getenv func(string) string, hostname string) Config {
	cfg := Config{
		Token:     getenv("NIGHTWATCH_TOKEN"),
		BaseURL:   strings.TrimRight(firstNonEmpty(getenv("NIGHTWATCH_BASE_URL"), defaultBaseURL), "/"),
		IngestURL: getenv("NIGHTWATCH_INGEST_URL"),
		Deploy:    getenv("NIGHTWATCH_DEPLOY"),
		Server:    firstNonEmpty(getenv("NIGHTWATCH_SERVER"), hostname),

		RequestSampleRate:       rate(getenv("NIGHTWATCH_REQUEST_SAMPLE_RATE")),
		CommandSampleRate:       rate(getenv("NIGHTWATCH_COMMAND_SAMPLE_RATE")),
		ExceptionSampleRate:     rate(getenv("NIGHTWATCH_EXCEPTION_SAMPLE_RATE")),
		ScheduledTaskSampleRate: rate(getenv("NIGHTWATCH_SCHEDULED_TASK_SAMPLE_RATE")),

		IgnoreCacheEvents:          truthy(getenv("NIGHTWATCH_IGNORE_CACHE_EVENTS")),
		IgnoreMail:                 truthy(getenv("NIGHTWATCH_IGNORE_MAIL")),
		IgnoreNotifications:        truthy(getenv("NIGHTWATCH_IGNORE_NOTIFICATIONS")),
		IgnoreOutgoingRequests:     truthy(getenv("NIGHTWATCH_IGNORE_OUTGOING_REQUESTS")),
		IgnoreQueries:              truthy(getenv("NIGHTWATCH_IGNORE_QUERIES")),
		LogLevel:                   strings.ToLower(firstNonEmpty(getenv("NIGHTWATCH_LOG_LEVEL"), getenv("LOG_LEVEL"), "debug")),
		RedactHeaders:              mergeRedactNames(splitList(getenv("NIGHTWATCH_REDACT_HEADERS"), nil)),
		RedactQuery:                mergeNames(defaultRedactQuery, splitList(getenv("NIGHTWATCH_REDACT_QUERY"), nil)),
		IgnorePaths:                splitList(getenv("NIGHTWATCH_IGNORE_PATHS"), nil),
		IgnoreScheduledTasks:       splitList(getenv("NIGHTWATCH_IGNORE_SCHEDULED_TASKS"), nil),
		CaptureRequestPayload:      truthy(getenv("NIGHTWATCH_CAPTURE_REQUEST_PAYLOAD")),
		CaptureExceptionSourceCode: enabledByDefault(getenv("NIGHTWATCH_CAPTURE_EXCEPTION_SOURCE_CODE")),
		RedactPayloadFields:        splitList(getenv("NIGHTWATCH_REDACT_PAYLOAD_FIELDS"), defaultRedactPayloadFields),

		BatchSize:     defaultBatchSize,
		FlushInterval: defaultFlushInterval,
		MaxBuffered:   defaultMaxBuffered,
		MaxRetries:    defaultMaxRetries,
		MaxRetryTime:  defaultMaxRetryTime,
		TraceMaxWait:  defaultTraceMaxWait,
		RootGrace:     defaultRootGrace,

		MaxTraces:        defaultMaxTraces,
		MaxSpansPerTrace: defaultMaxSpans,
		MaxOwners:        defaultMaxOwners,
	}

	enabled := getenv("NIGHTWATCH_ENABLED")
	cfg.Enabled = cfg.Token != "" && (enabled == "" || truthy(enabled))

	return cfg
}

// LogAllowed reports whether a log at level reaches Omni under LogLevel.
// Unknown levels always pass.
func (c Config) LogAllowed(level string) bool {
	return levelIndex(strings.ToLower(level)) >= levelIndex(c.LogLevel)
}

func levelIndex(level string) int {
	for i, name := range logLevels {
		if name == level {
			return i
		}
	}

	return len(logLevels)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// enabledByDefault is true unless the variable is set to a false value.
func enabledByDefault(value string) bool {
	return strings.TrimSpace(value) == "" || truthy(value)
}

func rate(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || parsed < 0 || parsed > 1 {
		return 1
	}

	return parsed
}

func splitList(value string, fallback []string) []string {
	if strings.TrimSpace(value) == "" {
		return append([]string(nil), fallback...)
	}

	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

// withDefaults returns a copy where every zero or negative size and time
// setting is replaced by its default, so a hand built Config is never
// unbounded or able to panic a ticker. RootGrace may be 0 (send at once).
func (c Config) withDefaults() Config {
	c.BatchSize = positiveOr(c.BatchSize, defaultBatchSize)
	c.MaxBuffered = positiveOr(c.MaxBuffered, defaultMaxBuffered)
	c.MaxRetries = positiveOr(c.MaxRetries, defaultMaxRetries)
	c.MaxTraces = positiveOr(c.MaxTraces, defaultMaxTraces)
	c.MaxSpansPerTrace = positiveOr(c.MaxSpansPerTrace, defaultMaxSpans)
	c.MaxOwners = positiveOr(c.MaxOwners, defaultMaxOwners)

	if c.FlushInterval <= 0 {
		c.FlushInterval = defaultFlushInterval
	}

	if c.TraceMaxWait <= 0 {
		c.TraceMaxWait = defaultTraceMaxWait
	}

	if c.MaxRetryTime <= 0 {
		c.MaxRetryTime = defaultMaxRetryTime
	}

	c.RootGrace = max(c.RootGrace, 0)

	// A hand built Config must never send secrets because a list was left empty.
	c.RedactHeaders = mergeRedactNames(c.RedactHeaders)
	c.RedactQuery = mergeNames(defaultRedactQuery, c.RedactQuery)

	return c
}

func positiveOr(value, fallback int) int {
	if value > 0 {
		return value
	}

	return fallback
}
