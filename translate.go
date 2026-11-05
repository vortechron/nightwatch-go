package nightwatch

import (
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const maxAncestorDepth = 64

// Sink receives finished records. The Sender is the usual sink.
type Sink interface {
	Enqueue(records []Record)
}

// Translator turns OpenTelemetry spans and log records into Nightwatch
// records. It holds a trace's spans until the execution root span (request,
// job attempt, command or scheduled task) ends, so the execution record can
// carry correct counters and every child can name its execution.
type Translator struct {
	cfg  Config
	sink Sink
	now  func() time.Time
	rand func() float64

	mu     sync.Mutex
	traces map[trace.TraceID]*traceBuf
	owners map[trace.SpanID]ownerRef
	users  map[string]struct{}
}

type traceBuf struct {
	spans map[trace.SpanID]sdktrace.ReadOnlySpan
	logs  []sdklog.Record
	since time.Time
}

// ownerRef remembers which execution an already-emitted span belonged to, so
// late arrivals still link correctly.
type ownerRef struct {
	info    execInfo
	expires time.Time
}

// NewTranslator creates a translator that writes finished records to sink.
func NewTranslator(cfg Config, sink Sink) *Translator {
	return &Translator{
		cfg:    cfg,
		sink:   sink,
		now:    time.Now,
		rand:   rand.Float64,
		traces: map[trace.TraceID]*traceBuf{},
		owners: map[trace.SpanID]ownerRef{},
		users:  map[string]struct{}{},
	}
}

// AddSpans accepts ended spans.
func (t *Translator) AddSpans(spans []sdktrace.ReadOnlySpan) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, span := range spans {
		t.addSpan(span)
	}
}

// AddLogs accepts log records.
func (t *Translator) AddLogs(records []sdklog.Record) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, entry := range records {
		t.addLog(entry.Clone())
	}
}

func (t *Translator) buffer(id trace.TraceID) *traceBuf {
	buf, ok := t.traces[id]
	if !ok {
		buf = &traceBuf{spans: map[trace.SpanID]sdktrace.ReadOnlySpan{}, since: t.now()}
		t.traces[id] = buf
	}

	return buf
}

func (t *Translator) addSpan(span sdktrace.ReadOnlySpan) {
	sc := span.SpanContext()
	buf := t.buffer(sc.TraceID())
	buf.spans[sc.SpanID()] = span

	if kind := execKind(span); kind != "" {
		t.flushExecution(sc.TraceID(), buf, span, kind)

		return
	}

	if owner, late := t.lateOwner(buf, sc.SpanID()); late {
		t.emitLate(buf, span, owner)
	}
}

func (t *Translator) addLog(entry sdklog.Record) {
	if !entry.TraceID().IsValid() {
		t.emitStandaloneLog(entry)

		return
	}

	buf := t.buffer(entry.TraceID())
	if owner, late := t.lateOwner(buf, entry.SpanID()); late {
		t.sink.Enqueue(t.logRecords([]sdklog.Record{entry}, owner.info))

		return
	}

	buf.logs = append(buf.logs, entry)
}

// resolve walks up from a span to the execution root that owns it. found is
// false when the chain leaves what the translator knows about.
func (t *Translator) resolve(buf *traceBuf, id trace.SpanID) (ownerRef, trace.SpanID, bool) {
	for range maxAncestorDepth {
		if owner, ok := t.owners[id]; ok {
			return owner, owner.info.root, true
		}

		span, ok := buf.spans[id]
		if !ok {
			return ownerRef{}, trace.SpanID{}, false
		}

		if execKind(span) != "" {
			return ownerRef{}, id, true
		}

		id = span.Parent().SpanID()
	}

	return ownerRef{}, trace.SpanID{}, false
}

// lateOwner reports the owner of a span whose execution already ended.
func (t *Translator) lateOwner(buf *traceBuf, id trace.SpanID) (ownerRef, bool) {
	owner, _, found := t.resolve(buf, id)

	return owner, found && owner.info.root.IsValid()
}

func (t *Translator) flushExecution(traceID trace.TraceID, buf *traceBuf, root sdktrace.ReadOnlySpan, kind string) {
	rootID := root.SpanContext().SpanID()
	members, logs := t.collect(buf, rootID)

	user := firstUser(members)
	info := newExecInfo(root, kind, user)
	info.sampled = t.keep(traceID, root, kind, members)

	expires := t.now().Add(2 * t.cfg.TraceMaxWait)
	for _, member := range members {
		t.owners[member.SpanContext().SpanID()] = ownerRef{info: info, expires: expires}
	}

	for _, member := range members {
		delete(buf.spans, member.SpanContext().SpanID())
	}

	if len(buf.spans) == 0 && len(buf.logs) == 0 {
		delete(t.traces, traceID)
	}

	if info.sampled {
		t.sink.Enqueue(t.assemble(root, info, members, logs))
	}
}

// collect removes and returns the spans and logs owned by the root.
func (t *Translator) collect(buf *traceBuf, rootID trace.SpanID) ([]sdktrace.ReadOnlySpan, []sdklog.Record) {
	var members []sdktrace.ReadOnlySpan

	for id, span := range buf.spans {
		if _, owner, found := t.resolve(buf, id); found && owner == rootID {
			members = append(members, span)
		}
	}

	slices.SortFunc(members, func(a, b sdktrace.ReadOnlySpan) int { return a.StartTime().Compare(b.StartTime()) })

	var logs, rest []sdklog.Record

	for _, entry := range buf.logs {
		if _, owner, found := t.resolve(buf, entry.SpanID()); found && owner == rootID {
			logs = append(logs, entry)
		} else {
			rest = append(rest, entry)
		}
	}

	buf.logs = rest

	return members, logs
}

// keep applies sampling. A trace that sampling dropped is still kept when it
// holds an unhandled exception and the exception sample rate allows it.
func (t *Translator) keep(traceID trace.TraceID, root sdktrace.ReadOnlySpan, kind string, members []sdktrace.ReadOnlySpan) bool {
	if sampledBy(traceID, t.rateFor(root, kind)) {
		return true
	}

	for _, found := range collectExceptions(members) {
		if !found.handled && t.rand() <= t.cfg.ExceptionSampleRate {
			return true
		}
	}

	return false
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

	for _, found := range collectExceptions(members) {
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

func (t *Translator) emitStandaloneLog(entry sdklog.Record) {
	if !sampledBy(entry.TraceID(), 1) {
		return
	}

	x := execInfo{traceID: traceUUID(entry.TraceID())}
	if !entry.TraceID().IsValid() {
		x.traceID = traceUUID(newTraceID())
	}

	t.sink.Enqueue(t.logRecords([]sdklog.Record{entry}, x))
}

// emitLate sends a child span that ended after its execution was already sent.
func (t *Translator) emitLate(buf *traceBuf, span sdktrace.ReadOnlySpan, owner ownerRef) {
	delete(buf.spans, span.SpanContext().SpanID())
	t.owners[span.SpanContext().SpanID()] = owner

	if !owner.info.sampled {
		return
	}

	if record, ok := t.spanRecord(span, owner.info); ok && !t.ignored(record.Type()) {
		t.sink.Enqueue([]Record{sanitize(record)})
	}
}

// Sweep emits what is left of traces older than TraceMaxWait (their root span
// never ended) and forgets old owners. It is called by a timer.
func (t *Translator) Sweep() {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	for id, buf := range t.traces {
		if now.Sub(buf.since) >= t.cfg.TraceMaxWait {
			t.emitOrphans(id, buf)
		}
	}

	for id, owner := range t.owners {
		if now.After(owner.expires) {
			delete(t.owners, id)
		}
	}
}

// Drain emits everything that is still held. Call it on shutdown.
func (t *Translator) Drain() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for id, buf := range t.traces {
		t.emitOrphans(id, buf)
	}
}

func (t *Translator) emitOrphans(id trace.TraceID, buf *traceBuf) {
	delete(t.traces, id)

	x := orphanInfo(id)
	spans := make([]sdktrace.ReadOnlySpan, 0, len(buf.spans))
	for _, span := range buf.spans {
		spans = append(spans, span)
	}

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

	records = append(records, t.logRecords(buf.logs, x)...)
	if len(records) > 0 {
		t.sink.Enqueue(records)
	}
}
