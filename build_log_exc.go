package nightwatch

import (
	"slices"
	"strings"
	"time"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// exceptionInfo is an exception found in a span "exception" event.
type exceptionInfo struct {
	at       time.Time
	class    string
	message  string
	code     string
	frames   []Frame
	handled  bool
	reported bool
	spanName string
}

func (t *Translator) exceptionRecord(info exceptionInfo, x execInfo) Record {
	file, line := FrameLocation(info.frames)
	groupFile := file

	// Without a stack there is no real location. Use the span as the best one
	// we have, and the message in the group so distinct errors do not all
	// collapse into one issue.
	if file == "" {
		file = info.spanName
		groupFile = info.spanName + "|" + info.message
	}

	record := t.childHead(RecordException, info.at, ExceptionGroup(info.class, info.code, groupFile, line), x)
	record["v"] = 3

	record["class"] = Text(info.class)
	record["file"] = Text(file)
	record["line"] = line
	record["message"] = Text(SanitizeUTF8(info.message))
	record["code"] = Text(info.code)
	record["trace"] = EncodeFrames(t.withSource(info.frames))
	record["handled"] = info.handled
	record["php_version"] = ""
	record["laravel_version"] = ""

	return record
}

// collectExceptions reads the "exception" events of the spans. Every reported
// error counts, like in PHP. The one exception: when Goravel's HTTP middleware
// echoes an error that this client already recorded with a stack (same span,
// same message, no stack of its own), the echo is skipped once per original.
func collectExceptions(spans []sdktrace.ReadOnlySpan) []exceptionInfo {
	var found []exceptionInfo

	for _, span := range spans {
		found = append(found, spanExceptions(span)...)
	}

	return found
}

func spanExceptions(span sdktrace.ReadOnlySpan) []exceptionInfo {
	var all []exceptionInfo

	for _, event := range span.Events() {
		if event.Name == eventException {
			info := exceptionFromEvent(event.Time, newAttrSet(event.Attributes))
			info.spanName = span.Name()
			all = append(all, info)
		}
	}

	return dropEchoes(all)
}

func dropEchoes(all []exceptionInfo) []exceptionInfo {
	consumed := make([]bool, len(all))
	kept := make([]exceptionInfo, 0, len(all))

	for _, info := range all {
		if info.reported || len(info.frames) > 0 {
			kept = append(kept, info)
		}
	}

	for _, info := range all {
		if info.reported || len(info.frames) > 0 {
			continue
		}

		if original := matchingOriginal(all, consumed, info); original >= 0 {
			consumed[original] = true

			continue
		}

		kept = append(kept, info)
	}

	slices.SortStableFunc(kept, func(a, b exceptionInfo) int { return a.at.Compare(b.at) })

	return kept
}

func matchingOriginal(all []exceptionInfo, consumed []bool, echo exceptionInfo) int {
	for i, candidate := range all {
		if consumed[i] || !candidate.reported {
			continue
		}

		if strings.TrimPrefix(candidate.message, "panic: ") == strings.TrimPrefix(echo.message, "panic: ") {
			return i
		}
	}

	return -1
}

func exceptionFromEvent(at time.Time, attrs attrSet) exceptionInfo {
	message := attrs.str(attrExceptionMsg)
	class := attrs.str(AttrExceptionClass, attrExceptionType)

	if strings.HasPrefix(message, "panic: ") {
		class = "panic"
	}

	return exceptionInfo{
		at:       at,
		class:    class,
		message:  message,
		code:     attrs.str(AttrExceptionCode),
		frames:   ParseStack(attrs.str(attrExceptionStack)),
		handled:  attrs.bool(AttrExceptionHandled),
		reported: attrs.bool(AttrExceptionReported),
	}
}

// logRecord turns an OpenTelemetry log record into a Nightwatch log record.
func (t *Translator) logRecord(entry sdklog.Record, x execInfo) Record {
	record := t.childHead(RecordLog, t.logTime(entry), "", x)
	delete(record, "_group")

	record["level"] = psrLevel(entry)
	record["message"] = Text(SanitizeUTF8(entry.Body().String()))
	record["context"] = jsonString(t.logContext(entry))
	record["extra"] = "{}"

	return record
}

// logTime is the time of the log line. Some emitters only set the observed
// time; if both are missing the time of arrival is used, never a zero time.
func (t *Translator) logTime(entry sdklog.Record) time.Time {
	if at := entry.Timestamp(); !at.IsZero() {
		return at
	}

	if at := entry.ObservedTimestamp(); !at.IsZero() {
		return at
	}

	return t.now()
}

func logValue(value otellog.Value) any {
	switch value.Kind() {
	case otellog.KindBool:
		return value.AsBool()
	case otellog.KindFloat64:
		return value.AsFloat64()
	case otellog.KindInt64:
		return value.AsInt64()
	case otellog.KindSlice:
		items := value.AsSlice()
		out := make([]any, 0, len(items))
		for _, item := range items {
			out = append(out, logValue(item))
		}

		return out
	case otellog.KindMap:
		out := map[string]any{}
		for _, kv := range value.AsMap() {
			out[kv.Key] = logValue(kv.Value)
		}

		return out
	case otellog.KindEmpty:
		return ""
	default:
		return value.String()
	}
}

// psrLevel returns the PSR-3 level of a log record.
func psrLevel(entry sdklog.Record) string {
	if text := strings.ToLower(entry.SeverityText()); text != "" {
		for _, level := range logLevels {
			if text == level {
				return text
			}
		}
	}

	severity := entry.Severity()

	switch {
	case severity >= otellog.SeverityFatal4:
		return "emergency"
	case severity >= otellog.SeverityFatal:
		return "critical"
	case severity >= otellog.SeverityError:
		return "error"
	case severity >= otellog.SeverityWarn:
		return "warning"
	case severity >= otellog.SeverityInfo:
		return "info"
	default:
		return "debug"
	}
}
