package nightwatch

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

// Record is one Nightwatch record exactly as it is sent to Omni: a flat JSON
// object. Builders never put nil values in it.
type Record map[string]any

// Type returns the record type ("request", "query" ...).
func (r Record) Type() string {
	value, _ := r["t"].(string)

	return value
}

// jsonString renders v the way PHP's json_encode does for Omni: no HTML
// escaping and no trailing newline.
func jsonString(v any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(v); err != nil {
		return "{}"
	}

	return string(bytes.TrimRight(buffer.Bytes(), "\n"))
}

// traceUUID writes an OpenTelemetry trace id as a UUID, which Omni needs.
func traceUUID(id trace.TraceID) string {
	return uuid.UUID(id).String()
}

// attemptUUID derives a stable job attempt id from a span: the 8 span id
// bytes followed by the last 8 trace id bytes.
func attemptUUID(traceID trace.TraceID, spanID trace.SpanID) string {
	var raw [16]byte
	copy(raw[:8], spanID[:])
	copy(raw[8:], traceID[8:])

	return uuid.UUID(raw).String()
}

// unixSeconds converts a time to float seconds since the epoch.
func unixSeconds(at time.Time) float64 {
	return float64(at.UnixNano()) / 1e9
}

// micros converts a duration to whole microseconds, never negative.
func micros(d time.Duration) int64 {
	if d < 0 {
		return 0
	}

	return d.Microseconds()
}

// sanitize replaces nil values so Omni's decoder never sees null.
func sanitize(record Record) Record {
	out := make(Record, len(record))
	for key, value := range record {
		out[key] = nonNil(key, value)
	}

	return out
}

func nonNil(key string, value any) any {
	if value == nil {
		return ""
	}

	if methods, ok := value.([]string); ok && methods == nil {
		return []string{}
	}

	switch key {
	case "context", "extra":
		if text, ok := value.(string); ok && text == "" {
			return "{}"
		}
	case "trace":
		if text, ok := value.(string); ok && text == "" {
			return "[]"
		}
	}

	return value
}
