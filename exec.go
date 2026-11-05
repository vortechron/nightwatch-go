package nightwatch

import (
	"math"
	"strings"
	"sync"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// execInfo describes the execution (request, job attempt, command or
// scheduled task) a record belongs to.
type execInfo struct {
	source  string
	id      string
	preview string
	user    string
	traceID string
	root    trace.SpanID
	kind    string
	sampled bool
}

// counters are the per-execution tallies stored on the execution record.
type counters struct {
	exceptions, logs, queries, jobsQueued    int
	mail, notifications, outgoing, cacheHits int
}

// execKind returns the record type of an execution root span, or "" when the
// span is an ordinary child.
func execKind(span sdktrace.ReadOnlySpan) string {
	attrs := newAttrSet(span.Attributes())

	switch kind := attrs.str(AttrRecord); kind {
	case RecordJobAttempt, RecordCommand, RecordScheduledTask:
		return kind
	}

	if span.SpanKind() == trace.SpanKindServer && attrs.has(attrHTTPMethod) {
		return RecordRequest
	}

	return ""
}

func sourceOf(kind string) string {
	switch kind {
	case RecordJobAttempt:
		return sourceJob
	case RecordCommand:
		return sourceCommand
	case RecordScheduledTask:
		return sourceSchedule
	default:
		return sourceRequest
	}
}

// newExecInfo derives the linking fields of an execution from its root span.
func newExecInfo(root sdktrace.ReadOnlySpan, kind, user string) execInfo {
	attrs := newAttrSet(root.Attributes())
	traceID := root.SpanContext().TraceID()
	spanID := root.SpanContext().SpanID()

	info := execInfo{
		source:  sourceOf(kind),
		id:      traceUUID(traceID),
		traceID: traceUUID(traceID),
		user:    user,
		root:    spanID,
		kind:    kind,
	}

	switch kind {
	case RecordJobAttempt:
		info.id = attemptUUID(traceID, spanID)
		info.preview = attrs.str(AttrJobName)
	case RecordCommand:
		info.preview = attrs.str(AttrCommandName)
	case RecordScheduledTask:
		info.preview = attrs.str(AttrTaskName)
	default:
		info.preview = requestPreview(attrs)
	}

	return info
}

func requestPreview(attrs attrSet) string {
	path := attrs.str(attrURLPath, attrHTTPRoute)

	return attrs.str(attrHTTPMethod) + " " + path
}

// orphanInfo is the execution of records whose root span never arrived.
func orphanInfo(traceID trace.TraceID) execInfo {
	return execInfo{traceID: traceUUID(traceID), sampled: true}
}

// sampledBy decides, from the trace id alone, whether a trace is kept. Using
// the id means every execution of one trace (a request and the jobs it
// queued) makes the same decision.
func sampledBy(traceID trace.TraceID, rate float64) bool {
	if rate >= 1 {
		return true
	}

	if rate <= 0 {
		return false
	}

	var value uint64
	for _, b := range traceID[:8] {
		value = value<<8 | uint64(b)
	}

	return float64(value)/float64(math.MaxUint64) <= rate
}

func (t *Translator) rateFor(root sdktrace.ReadOnlySpan, kind string) float64 {
	switch kind {
	case RecordCommand:
		return t.cfg.CommandSampleRate
	case RecordScheduledTask:
		return t.cfg.ScheduledTaskSampleRate
	case RecordJobAttempt:
		if root.Parent().IsValid() {
			return t.cfg.RequestSampleRate
		}

		return t.cfg.CommandSampleRate
	default:
		return t.cfg.RequestSampleRate
	}
}

var (
	memoryMu      sync.Mutex
	memoryChecked time.Time
	memoryValue   int64
)

func firstUser(spans []sdktrace.ReadOnlySpan) string {
	for _, span := range spans {
		if user := newAttrSet(span.Attributes()).str(attrEnduserID); user != "" {
			return user
		}
	}

	return ""
}

func trimPreview(text string) string {
	return TinyText(strings.TrimSpace(text))
}
