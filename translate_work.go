package nightwatch

import (
	"slices"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type workKind int

const (
	workExec workKind = iota
	workOrphans
	workLateSpan
	workLateLog
	workStandaloneLog
)

// work is a unit of output that was decided under the lock and is rendered
// into records after the lock is released.
type work struct {
	kind    workKind
	exec    *pendingExec
	traceID trace.TraceID
	spans   []sdktrace.ReadOnlySpan
	logs    []sdklog.Record
	info    execInfo
}

// run renders the work and hands all records to the sink in one call. It must
// not be called with t.mu held: the sink may call back into the translator.
func (t *Translator) run(items []work) {
	var out []Record

	for _, item := range items {
		out = append(out, t.render(item)...)
	}

	if len(out) > 0 {
		t.sink.Enqueue(out)
	}
}

func (t *Translator) render(item work) []Record {
	switch item.kind {
	case workExec:
		if !item.exec.info.sampled {
			return nil
		}

		return t.assemble(item.exec.root, item.exec.info, item.exec.members, item.exec.logs)
	case workOrphans:
		return t.renderOrphans(item)
	case workLateSpan:
		return t.renderLateSpan(item)
	case workLateLog:
		return t.logRecords(item.logs, item.info)
	default:
		return t.logRecords(item.logs, execInfo{traceID: traceUUID(newTraceID())})
	}
}

func (t *Translator) renderLateSpan(item work) []Record {
	record, ok := t.spanRecord(item.spans[0], item.info)
	if !ok || t.ignored(record.Type()) {
		return nil
	}

	return []Record{sanitize(record)}
}

// renderOrphans sends what was held for a trace whose root never ended. Sampling
// still applies: an orphan of an unsampled trace is only kept by the exception
// re-roll, like an execution would be.
func (t *Translator) renderOrphans(item work) []Record {
	if !sampledBy(item.traceID, t.cfg.RequestSampleRate) && !t.rerollForException(collectExceptions(item.spans)) {
		return nil
	}

	x := orphanInfo(item.traceID)
	spans := item.spans

	slices.SortFunc(spans, func(a, b sdktrace.ReadOnlySpan) int { return a.StartTime().Compare(b.StartTime()) })

	var records []Record

	for _, span := range spans {
		if record, ok := t.spanRecord(span, x); ok && !t.ignored(record.Type()) {
			records = append(records, sanitize(record))
		}
	}

	for _, found := range collectExceptions(spans) {
		records = append(records, sanitize(t.exceptionRecord(found, x)))
	}

	return append(records, t.logRecords(item.logs, x)...)
}

// assemble builds the execution record and every child record.
func (t *Translator) assemble(root sdktrace.ReadOnlySpan, x execInfo, members []sdktrace.ReadOnlySpan, logs []sdklog.Record) []Record {
	children, tally := t.children(root, x, members, logs)

	preview := ""
	for _, record := range children {
		if record.Type() == RecordException && record["handled"] == false {
			preview, _ = record["message"].(string)
		}
	}

	records := make([]Record, 0, len(children)+2)
	records = append(records, sanitize(t.buildExecution(root, x, tally, preview)))
	records = append(records, children...)

	return append(records, t.userRecords(x, members)...)
}

// children builds the child records and counts them. Ignored types are
// neither sent nor counted, so counters always match what Omni receives.
func (t *Translator) children(root sdktrace.ReadOnlySpan, x execInfo, members []sdktrace.ReadOnlySpan, logs []sdklog.Record) ([]Record, counters) {
	var tally counters
	var records []Record

	rootID := root.SpanContext().SpanID()
	for _, span := range members {
		if span.SpanContext().SpanID() == rootID {
			continue
		}

		if record, ok := t.spanRecord(span, x); ok && !t.ignored(record.Type()) {
			tally.count(record.Type())
			records = append(records, sanitize(record))
		}
	}

	for _, found := range executionExceptions(root, members) {
		tally.exceptions++
		records = append(records, sanitize(t.exceptionRecord(found, x)))
	}

	logRecords := t.logRecords(logs, x)
	tally.logs += len(logRecords)

	return append(records, logRecords...), tally
}

func (c *counters) count(recordType string) {
	switch recordType {
	case RecordQuery:
		c.queries++
	case RecordOutgoingRequest:
		c.outgoing++
	case RecordQueuedJob:
		c.jobsQueued++
	case RecordCacheEvent:
		c.cacheHits++
	case RecordMail:
		c.mail++
	case RecordNotification:
		c.notifications++
	}
}

func (t *Translator) ignored(recordType string) bool {
	switch recordType {
	case RecordQuery:
		return t.cfg.IgnoreQueries
	case RecordOutgoingRequest:
		return t.cfg.IgnoreOutgoingRequests
	case RecordCacheEvent:
		return t.cfg.IgnoreCacheEvents
	case RecordMail:
		return t.cfg.IgnoreMail
	case RecordNotification:
		return t.cfg.IgnoreNotifications
	default:
		return false
	}
}

func (t *Translator) logRecords(logs []sdklog.Record, x execInfo) []Record {
	records := make([]Record, 0, len(logs))

	for _, entry := range logs {
		if t.cfg.LogAllowed(psrLevel(entry)) {
			records = append(records, sanitize(t.logRecord(entry, x)))
		}
	}

	return records
}
