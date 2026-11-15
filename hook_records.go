package nightwatch

import "time"

// Query is what a query filter or redaction callback sees (Laravel's
// Laravel\Nightwatch\Records\Query). A redaction callback may change SQL;
// changes to the other fields are ignored, as they are read-only in Laravel.
type Query struct {
	SQL            string
	File           string
	Line           int
	Duration       time.Duration
	Connection     string
	ConnectionType string // "read", "write" or ""
}

// CacheEvent is what a cache filter or redaction callback sees (Laravel's
// Records\CacheEvent). Type is one of the Cache* constants. A redaction
// callback may change Key.
type CacheEvent struct {
	Store    string
	Key      string
	Type     string
	Duration time.Duration
	TTL      time.Duration
}

// Mail is what a mail filter or redaction callback sees (Laravel's
// Records\Mail). To, Cc, Bcc and Attachments are counts. A redaction callback
// may change Subject.
type Mail struct {
	Mailer      string
	Class       string
	Subject     string
	To          int
	Cc          int
	Bcc         int
	Attachments int
	Duration    time.Duration
	Failed      bool
}

// Notification is what a notification filter sees (Laravel's
// Records\Notification).
type Notification struct {
	Channel  string
	Class    string
	Duration time.Duration
}

// QueuedJob is what a queued job filter sees (Laravel's Records\QueuedJob).
type QueuedJob struct {
	JobID      string
	Name       string
	Connection string
	Queue      string
	Duration   time.Duration
}

// OutgoingRequest is what an outgoing request filter or redaction callback
// sees (Laravel's Records\OutgoingRequest). URL already has the query secrets
// redacted (NIGHTWATCH_REDACT_QUERY). A redaction callback may change URL; the
// result is redacted again, and the host stays the real one.
type OutgoingRequest struct {
	Method       string
	URL          string
	Duration     time.Duration
	RequestSize  int64
	ResponseSize int64
	StatusCode   int
}

// Exception is what an exception redaction callback sees (Laravel's
// Records\Exception). A redaction callback may change Message; the
// execution's exception preview shows the redacted text.
type Exception struct {
	Class   string
	Message string
	Code    string
	File    string
	Line    int
	Handled bool
}

// Command is what a command redaction callback sees (Laravel's
// Records\Command). A redaction callback may change Command, the command
// line.
type Command struct {
	Class    string
	Name     string
	Command  string
	ExitCode int
	Duration time.Duration
}

// Request is what a request redaction callback sees (Laravel's
// Records\Request). A callback may change URL, IP, Headers and Payload; the
// other fields are read-only, as in Laravel. Headers has lowercase names and
// values that are already redacted. Payload is the captured JSON body (only
// with CaptureRequestPayload, on a 500), or nil; setting it to nil drops it.
// The query, header and payload redaction run again on the result, so a
// callback cannot put a secret back in.
type Request struct {
	Method       string
	URL          string
	RouteName    string
	RouteMethods []string
	RouteDomain  string
	RoutePath    string
	RouteAction  string
	IP           string
	Duration     time.Duration
	StatusCode   int
	RequestSize  int64
	ResponseSize int64
	Headers      map[string][]string
	Payload      map[string]any
}
