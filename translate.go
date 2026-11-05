package nightwatch

import (
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
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

// TranslatorStats counts what the translator had to drop to stay bounded.
type TranslatorStats struct {
	// TracesEvicted are traces sent early as orphans because too many were held.
	TracesEvicted int64
	// SpansDropped are spans (or logs) refused because their trace was full.
	SpansDropped int64
	// OwnersEvicted are finished-span entries forgotten because of the cap.
	OwnersEvicted int64
	// LateChildren arrived after their execution was already sent, so the
	// counters on that execution record miss them.
	LateChildren int64
}

// Translator turns OpenTelemetry spans and log records into Nightwatch
// records. It holds a trace's spans until the execution root span (request,
// job attempt, command or scheduled task) ends, so the execution record can
// carry correct counters and every child can name its execution. A finished
// root waits RootGrace more, so logs and spans that end just after it count.
//
// All state is bounded: MaxTraces traces, MaxSpansPerTrace spans per trace and
// MaxOwners finished-span entries. Records are built and sent outside the lock.
type Translator struct {
	cfg  Config
	sink Sink
	now  func() time.Time
	rand func() float64
	mem  memSampler

	mu      sync.Mutex
	traces  map[trace.TraceID]*traceBuf
	order   []trace.TraceID
	owners  map[trace.SpanID]ownerRef
	pending map[trace.SpanID]*pendingExec

	usersMu sync.Mutex
	users   map[string]struct{}

	tracesEvicted atomic.Int64
	spansDropped  atomic.Int64
	ownersEvicted atomic.Int64
	lateChildren  atomic.Int64
}

type traceBuf struct {
	spans map[trace.SpanID]sdktrace.ReadOnlySpan
	logs  []sdklog.Record
	since time.Time
}

// ownerRef remembers which execution an already-finished span belonged to, so
// late arrivals still link correctly.
type ownerRef struct {
	info    execInfo
	expires time.Time
}

// pendingExec is a finished execution waiting out its grace period.
type pendingExec struct {
	root    sdktrace.ReadOnlySpan
	info    execInfo
	members []sdktrace.ReadOnlySpan
	logs    []sdklog.Record
	due     time.Time
}

// NewTranslator creates a translator that writes finished records to sink.
func NewTranslator(cfg Config, sink Sink) *Translator {
	return &Translator{
		cfg:     cfg.withDefaults(),
		sink:    sink,
		now:     time.Now,
		rand:    rand.Float64,
		traces:  map[trace.TraceID]*traceBuf{},
		owners:  map[trace.SpanID]ownerRef{},
		pending: map[trace.SpanID]*pendingExec{},
		users:   map[string]struct{}{},
	}
}

// Stats returns the drop counters.
func (t *Translator) Stats() TranslatorStats {
	return TranslatorStats{
		TracesEvicted: t.tracesEvicted.Load(),
		SpansDropped:  t.spansDropped.Load(),
		OwnersEvicted: t.ownersEvicted.Load(),
		LateChildren:  t.lateChildren.Load(),
	}
}

// AddSpans accepts ended spans.
func (t *Translator) AddSpans(spans []sdktrace.ReadOnlySpan) {
	var items []work

	t.mu.Lock()
	for _, span := range spans {
		items = append(items, t.addSpan(span)...)
	}
	t.mu.Unlock()

	t.run(items)
}

// AddLogs accepts log records.
func (t *Translator) AddLogs(records []sdklog.Record) {
	var items []work

	t.mu.Lock()
	for _, entry := range records {
		items = append(items, t.addLog(entry.Clone())...)
	}
	t.mu.Unlock()

	t.run(items)
}

func (t *Translator) addSpan(span sdktrace.ReadOnlySpan) []work {
	sc := span.SpanContext()
	kind := execKind(span)

	if kind == "" {
		if owner, late := t.lateOwner(t.traces[sc.TraceID()], span.Parent().SpanID()); late {
			return t.lateSpan(sc.TraceID(), span, owner)
		}
	}

	buf, evicted := t.buffer(sc.TraceID())

	if kind == "" && len(buf.spans) >= t.cfg.MaxSpansPerTrace {
		t.spansDropped.Add(1)

		return evicted
	}

	buf.spans[sc.SpanID()] = span

	if kind == "" {
		return evicted
	}

	return append(evicted, t.finishRoot(sc.TraceID(), buf, span, kind)...)
}

func (t *Translator) addLog(entry sdklog.Record) []work {
	if !entry.TraceID().IsValid() {
		return []work{{kind: workStandaloneLog, logs: []sdklog.Record{entry}}}
	}

	if owner, late := t.lateOwner(t.traces[entry.TraceID()], entry.SpanID()); late {
		return t.lateLog(entry, owner)
	}

	buf, evicted := t.buffer(entry.TraceID())
	if len(buf.logs) >= t.cfg.MaxSpansPerTrace {
		t.spansDropped.Add(1)

		return evicted
	}

	buf.logs = append(buf.logs, entry)

	return evicted
}

// buffer returns the buffer of a trace, creating it. When MaxTraces would be
// exceeded the oldest trace is taken out and returned as orphan work.
func (t *Translator) buffer(id trace.TraceID) (*traceBuf, []work) {
	if buf, ok := t.traces[id]; ok {
		return buf, nil
	}

	var evicted []work

	for len(t.traces) >= t.cfg.MaxTraces && len(t.order) > 0 {
		oldest := t.order[0]
		t.order = t.order[1:]

		if old, ok := t.traces[oldest]; ok {
			delete(t.traces, oldest)
			t.tracesEvicted.Add(1)
			evicted = append(evicted, t.orphanWork(oldest, old))
		}
	}

	buf := &traceBuf{spans: map[trace.SpanID]sdktrace.ReadOnlySpan{}, since: t.now()}
	t.traces[id] = buf
	t.order = append(t.order, id)

	return buf, evicted
}

func (t *Translator) orphanWork(id trace.TraceID, buf *traceBuf) work {
	spans := make([]sdktrace.ReadOnlySpan, 0, len(buf.spans))
	for _, span := range buf.spans {
		spans = append(spans, span)
	}

	return work{kind: workOrphans, traceID: id, spans: spans, logs: buf.logs}
}

// resolve walks up from a span to the execution root that owns it. buf may be
// nil. found is false when the chain leaves what the translator knows.
func (t *Translator) resolve(buf *traceBuf, id trace.SpanID) (ownerRef, trace.SpanID, bool) {
	for range maxAncestorDepth {
		if owner, ok := t.owners[id]; ok {
			return owner, owner.info.root, true
		}

		if buf == nil {
			return ownerRef{}, trace.SpanID{}, false
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
func (t *Translator) lateOwner(buf *traceBuf, parent trace.SpanID) (ownerRef, bool) {
	owner, _, found := t.resolve(buf, parent)

	return owner, found && owner.info.root.IsValid()
}

func (t *Translator) finishRoot(traceID trace.TraceID, buf *traceBuf, root sdktrace.ReadOnlySpan, kind string) []work {
	rootID := root.SpanContext().SpanID()
	members, logs := t.collect(buf, rootID)

	info := newExecInfo(root, kind, firstUser(members))
	info.sampled = t.keep(traceID, root, kind, members)

	expires := t.now().Add(2*t.cfg.TraceMaxWait + t.cfg.RootGrace)
	for _, member := range members {
		id := member.SpanContext().SpanID()
		t.owners[id] = ownerRef{info: info, expires: expires}
		delete(buf.spans, id)
	}

	t.trimOwners()
	t.dropIfEmpty(traceID, buf)

	exec := &pendingExec{root: root, info: info, members: members, logs: logs, due: t.now().Add(t.cfg.RootGrace)}
	if t.cfg.RootGrace <= 0 {
		return []work{{kind: workExec, exec: exec}}
	}

	t.pending[rootID] = exec

	return nil
}

func (t *Translator) dropIfEmpty(id trace.TraceID, buf *traceBuf) {
	if len(buf.spans) == 0 && len(buf.logs) == 0 {
		delete(t.traces, id)
	}
}

// trimOwners keeps the owner map under MaxOwners: expired entries go first,
// then arbitrary ones.
func (t *Translator) trimOwners() {
	if len(t.owners) <= t.cfg.MaxOwners {
		return
	}

	now := t.now()
	for id, owner := range t.owners {
		if now.After(owner.expires) {
			delete(t.owners, id)
		}
	}

	for id := range t.owners {
		if len(t.owners) <= t.cfg.MaxOwners {
			return
		}

		delete(t.owners, id)
		t.ownersEvicted.Add(1)
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

	return t.rerollForException(members)
}

func (t *Translator) rerollForException(members []sdktrace.ReadOnlySpan) bool {
	for _, found := range collectExceptions(members) {
		if !found.handled && t.rand() <= t.cfg.ExceptionSampleRate {
			return true
		}
	}

	return false
}

// lateSpan handles a span that ended after its execution finished. Inside the
// grace period it joins the waiting execution. After that it is sent alone and
// counted as late.
func (t *Translator) lateSpan(traceID trace.TraceID, span sdktrace.ReadOnlySpan, owner ownerRef) []work {
	t.owners[span.SpanContext().SpanID()] = owner

	if exec, waiting := t.pending[owner.info.root]; waiting {
		exec.members = append(exec.members, span)

		return nil
	}

	t.lateChildren.Add(1)
	t.forgetEmpty(traceID)

	if !owner.info.sampled {
		return nil
	}

	return []work{{kind: workLateSpan, spans: []sdktrace.ReadOnlySpan{span}, info: owner.info}}
}

func (t *Translator) lateLog(entry sdklog.Record, owner ownerRef) []work {
	if exec, waiting := t.pending[owner.info.root]; waiting {
		exec.logs = append(exec.logs, entry)

		return nil
	}

	t.lateChildren.Add(1)
	t.forgetEmpty(entry.TraceID())

	if !owner.info.sampled {
		return nil
	}

	return []work{{kind: workLateLog, logs: []sdklog.Record{entry}, info: owner.info}}
}

func (t *Translator) forgetEmpty(id trace.TraceID) {
	if buf, ok := t.traces[id]; ok {
		t.dropIfEmpty(id, buf)
	}
}

// Sweep sends executions whose grace period is over, sends what is left of
// traces older than TraceMaxWait (their root span never ended) and forgets old
// owners. A timer calls it.
func (t *Translator) Sweep() {
	var items []work

	t.mu.Lock()
	now := t.now()

	for id, exec := range t.pending {
		if !now.Before(exec.due) {
			delete(t.pending, id)
			items = append(items, work{kind: workExec, exec: exec})
		}
	}

	for id, buf := range t.traces {
		if now.Sub(buf.since) >= t.cfg.TraceMaxWait {
			delete(t.traces, id)
			items = append(items, t.orphanWork(id, buf))
		}
	}

	for id, owner := range t.owners {
		if now.After(owner.expires) {
			delete(t.owners, id)
		}
	}

	t.compactOrder()
	t.mu.Unlock()

	t.run(items)
}

// compactOrder drops ids that are no longer held from the eviction queue.
func (t *Translator) compactOrder() {
	if len(t.order) <= 2*len(t.traces)+16 {
		return
	}

	kept := t.order[:0]
	for _, id := range t.order {
		if _, ok := t.traces[id]; ok {
			kept = append(kept, id)
		}
	}

	t.order = kept
}

// FlushPending sends every execution that is waiting out its grace period.
func (t *Translator) FlushPending() {
	t.mu.Lock()
	items := t.takePending()
	t.mu.Unlock()

	t.run(items)
}

func (t *Translator) takePending() []work {
	items := make([]work, 0, len(t.pending))
	for id, exec := range t.pending {
		delete(t.pending, id)
		items = append(items, work{kind: workExec, exec: exec})
	}

	return items
}

// Drain sends everything that is still held. Call it on shutdown.
func (t *Translator) Drain() {
	t.mu.Lock()
	items := t.takePending()

	for id, buf := range t.traces {
		delete(t.traces, id)
		items = append(items, t.orphanWork(id, buf))
	}

	t.order = nil
	t.mu.Unlock()

	t.run(items)
}
