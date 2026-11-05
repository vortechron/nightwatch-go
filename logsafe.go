package nightwatch

import (
	"strings"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// A log record sends the attributes the application put on the log line, and
// nothing else. The client never reads context.Context values, and it never
// prints a Go value with %v or %+v. Goravel's own debug log attaches the whole
// request context to a line (request, response, user, owner, trace), which can
// hold headers, bodies and user structs, so those keys are dropped, secret
// looking keys are redacted at any depth, and everything is bounded.
const (
	maxLogValueBytes   = 2048
	maxLogDepth        = 4
	maxLogContextBytes = 32 * 1024
	truncatedMark      = "[truncated]"
)

// dropLogKeys are attributes that hold request or user dumps.
var dropLogKeys = []string{"request", "response", "user", "owner", "trace", "headers", "cookies", "ctx", "context", "root"}

// secretKeyParts mark a key as secret when its lower-case name contains one.
var secretKeyParts = []string{keyPassword, "passwd", "secret", "token", "authorization", "cookie", "apikey", "api_key", "credential", "session"}

// logContext builds the JSON object of the "context" column.
func (t *Translator) logContext(entry sdklog.Record) map[string]any {
	context := map[string]any{}

	entry.WalkAttributes(func(kv otellog.KeyValue) bool {
		if !matches(kv.Key, dropLogKeys) {
			context[kv.Key] = t.safeLogValue(kv.Key, kv.Value, 0)
		}

		return true
	})

	if len(jsonString(context)) > maxLogContextBytes {
		return map[string]any{"_truncated": true}
	}

	return context
}

func (t *Translator) secretKey(key string) bool {
	lower := strings.ToLower(key)

	for _, part := range secretKeyParts {
		if strings.Contains(lower, part) {
			return true
		}
	}

	return matches(key, t.cfg.RedactPayloadFields) || matches(key, t.cfg.RedactHeaders) || matches(key, t.cfg.RedactQuery)
}

func (t *Translator) safeLogValue(key string, value otellog.Value, depth int) any {
	if t.secretKey(key) {
		return redactedValue
	}

	if depth > maxLogDepth {
		return truncatedMark
	}

	switch value.Kind() {
	case otellog.KindMap:
		out := map[string]any{}

		for _, kv := range value.AsMap() {
			if !matches(kv.Key, dropLogKeys) {
				out[kv.Key] = t.safeLogValue(kv.Key, kv.Value, depth+1)
			}
		}

		return out
	case otellog.KindSlice:
		items := value.AsSlice()
		out := make([]any, 0, len(items))

		for _, item := range items {
			out = append(out, t.safeLogValue("", item, depth+1))
		}

		return out
	default:
		return cutLogValue(logValue(value))
	}
}

// cutLogValue shortens long strings so one log line cannot carry a document.
func cutLogValue(value any) any {
	if text, ok := value.(string); ok && len(text) > maxLogValueBytes {
		return text[:maxLogValueBytes] + "…"
	}

	return value
}
