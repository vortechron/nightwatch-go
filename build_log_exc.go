package nightwatch

import (
	"strings"
	"time"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// exceptionInfo is an exception found in a span "exception" event.
type exceptionInfo struct {
	at      time.Time
	class   string
	message string
	code    string
	frames  []Frame
	handled bool
}

func (t *Translator) exceptionRecord(info exceptionInfo, x execInfo) Record {
	file, line := FrameLocation(info.frames)
	record := t.childHead(RecordException, info.at, ExceptionGroup(info.class, info.code, file, line), x)
	record["v"] = 3

	record["class"] = Text(info.class)
	record["file"] = Text(file)
	record["line"] = line
	record["message"] = Text(SanitizeUTF8(info.message))
	record["code"] = Text(info.code)
	record["trace"] = EncodeFrames(info.frames)
	record["handled"] = info.handled
	record["php_version"] = ""
	record["laravel_version"] = ""

	return record
}

// collectExceptions reads the "exception" events of the spans. The same error
// is often recorded twice (by a helper and by the HTTP middleware), so entries
// with the same message are merged and the one with a stack wins.
func collectExceptions(spans []sdktrace.ReadOnlySpan) []exceptionInfo {
	var found []exceptionInfo
	index := map[string]int{}

	for _, span := range spans {
		for _, event := range span.Events() {
			if event.Name != eventException {
				continue
			}

			info := exceptionFromEvent(event.Time, newAttrSet(event.Attributes))
			key := strings.TrimPrefix(info.message, "panic: ")

			if at, seen := index[key]; seen {
				if len(found[at].frames) == 0 && len(info.frames) > 0 {
					found[at] = info
				}

				continue
			}

			index[key] = len(found)
			found = append(found, info)
		}
	}

	return found
}

func exceptionFromEvent(at time.Time, attrs attrSet) exceptionInfo {
	message := attrs.str(attrExceptionMsg)
	class := attrs.str(AttrExceptionClass, attrExceptionType)

	if strings.HasPrefix(message, "panic: ") {
		class = "panic"
	}

	return exceptionInfo{
		at:      at,
		class:   class,
		message: message,
		code:    attrs.str(AttrExceptionCode),
		frames:  ParseStack(attrs.str(attrExceptionStack)),
		handled: attrs.bool(AttrExceptionHandled),
	}
}

// logRecord turns an OpenTelemetry log record into a Nightwatch log record.
func (t *Translator) logRecord(entry sdklog.Record, x execInfo) Record {
	record := t.childHead(RecordLog, entry.Timestamp(), "", x)
	delete(record, "_group")

	record["level"] = psrLevel(entry)
	record["message"] = Text(SanitizeUTF8(entry.Body().String()))
	record["context"] = jsonString(logContext(entry))
	record["extra"] = "{}"

	return record
}

func logContext(entry sdklog.Record) map[string]any {
	context := map[string]any{}
	entry.WalkAttributes(func(kv otellog.KeyValue) bool {
		context[kv.Key] = logValue(kv.Value)

		return true
	})

	return context
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
