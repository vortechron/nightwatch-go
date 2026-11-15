package nightwatch

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
)

// Hooks holds the callbacks that filter and redact records before they leave
// the process: Laravel's Nightwatch::rejectQueries, redactQueries and the
// rest. Register them once at boot, for example in a service provider:
//
//	client.RejectQueries(func(q nightwatch.Query) bool {
//		return strings.Contains(q.SQL, "health_checks")
//	})
//
// The Client and its Translator share one Hooks, so the methods can be called
// on either. Registration is safe while records are being built; a callback
// may be called from any goroutine and must not block.
type Hooks struct {
	mu  sync.Mutex
	set atomic.Pointer[hookSet]
}

// hookSet is replaced as a whole on every registration, so readers never lock.
type hookSet struct {
	rejectQueries []func(Query) bool

	redactQueries []func(*Query)

	rejectCacheKeys   []keyMatcher
	rejectVendorKeys  bool
	rejectCacheEvents []func(CacheEvent) bool
	redactCacheEvents []func(*CacheEvent)

	rejectMail []func(Mail) bool
	redactMail []func(*Mail)

	rejectNotifications []func(Notification) bool
	rejectQueuedJobs    []func(QueuedJob) bool

	rejectOutgoingRequests []func(OutgoingRequest) bool
	redactOutgoingRequests []func(*OutgoingRequest)

	redactExceptions []func(*Exception)

	redactCommands []func(*Command)

	redactRequests []func(*Request)

	ingesting []func(IngestingEvents) bool

	skipVendorCommands bool
	taskRates          map[string]float64

	unrecoverable func(error)
}

func newHooks() *Hooks {
	h := &Hooks{}
	h.set.Store(&hookSet{})

	return h
}

func (h *Hooks) load() *hookSet { return h.set.Load() }

// update copies the current set, lets change edit the copy and publishes it.
func (h *Hooks) update(change func(*hookSet)) {
	h.mu.Lock()
	defer h.mu.Unlock()

	next := *h.set.Load()
	change(&next)
	h.set.Store(&next)
}

// add appends fn to a copy of list, so a published set never changes.
func add[T any](list []T, fn T) []T {
	return append(slices.Clip(list), fn)
}

// RejectQueries drops every query for which the callback returns true. A
// rejected query is not sent and not counted on its execution, like Laravel.
func (h *Hooks) RejectQueries(fn func(Query) bool) {
	h.update(func(s *hookSet) { s.rejectQueries = add(s.rejectQueries, fn) })
}

// RedactQueries lets the callback change a query before it is sent. Only SQL
// is taken from the result; the group is hashed from the redacted SQL.
func (h *Hooks) RedactQueries(fn func(*Query)) {
	h.update(func(s *hookSet) { s.redactQueries = add(s.redactQueries, fn) })
}

// RejectCacheEvents drops every cache event for which the callback returns
// true. It is not sent and not counted.
func (h *Hooks) RejectCacheEvents(fn func(CacheEvent) bool) {
	h.update(func(s *hookSet) { s.rejectCacheEvents = add(s.rejectCacheEvents, fn) })
}

// RedactCacheEvents lets the callback change a cache event before it is sent.
// Only Key is taken from the result; the group is hashed from it.
func (h *Hooks) RedactCacheEvents(fn func(*CacheEvent)) {
	h.update(func(s *hookSet) { s.redactCacheEvents = add(s.redactCacheEvents, fn) })
}

// RejectMail drops every mail for which the callback returns true. It is not
// sent and not counted.
func (h *Hooks) RejectMail(fn func(Mail) bool) {
	h.update(func(s *hookSet) { s.rejectMail = add(s.rejectMail, fn) })
}

// RedactMail lets the callback change a mail before it is sent. Only Subject
// is taken from the result.
func (h *Hooks) RedactMail(fn func(*Mail)) {
	h.update(func(s *hookSet) { s.redactMail = add(s.redactMail, fn) })
}

// RejectNotifications drops every notification for which the callback
// returns true. It is not sent and not counted.
func (h *Hooks) RejectNotifications(fn func(Notification) bool) {
	h.update(func(s *hookSet) { s.rejectNotifications = add(s.rejectNotifications, fn) })
}

// RejectQueuedJobs drops the queued-job record of every job for which the
// callback returns true. It is not sent and not counted. The job's attempts
// are separate executions and are still recorded, as in Laravel.
func (h *Hooks) RejectQueuedJobs(fn func(QueuedJob) bool) {
	h.update(func(s *hookSet) { s.rejectQueuedJobs = add(s.rejectQueuedJobs, fn) })
}

// RejectOutgoingRequests drops every outgoing request for which the callback
// returns true. It is not sent and not counted.
func (h *Hooks) RejectOutgoingRequests(fn func(OutgoingRequest) bool) {
	h.update(func(s *hookSet) { s.rejectOutgoingRequests = add(s.rejectOutgoingRequests, fn) })
}

// RedactOutgoingRequests lets the callback change an outgoing request before
// it is sent. Only URL is taken from the result, and its query is redacted
// again afterwards.
func (h *Hooks) RedactOutgoingRequests(fn func(*OutgoingRequest)) {
	h.update(func(s *hookSet) { s.redactOutgoingRequests = add(s.redactOutgoingRequests, fn) })
}

// RedactExceptions lets the callback change an exception before it is sent.
// Only Message is taken from the result.
func (h *Hooks) RedactExceptions(fn func(*Exception)) {
	h.update(func(s *hookSet) { s.redactExceptions = add(s.redactExceptions, fn) })
}

// RedactCommands lets the callback change a command before it is sent. Only
// Command (the command line) is taken from the result.
func (h *Hooks) RedactCommands(fn func(*Command)) {
	h.update(func(s *hookSet) { s.redactCommands = add(s.redactCommands, fn) })
}

// RedactRequests lets the callback change a request before it is sent. URL,
// IP, Headers and Payload are taken from the result and redacted again.
func (h *Hooks) RedactRequests(fn func(*Request)) {
	h.update(func(s *hookSet) { s.redactRequests = add(s.redactRequests, fn) })
}

// HandleUnrecoverableErrorsUsing registers the handler for errors the client
// cannot report to Omni itself (Laravel's
// Nightwatch::handleUnrecoverableExceptionsUsing): a filter or redaction
// callback that panicked, a batch that was dropped. Log them to a place other
// than Nightwatch. A handler that panics is ignored. A later call replaces
// the handler.
func (h *Hooks) HandleUnrecoverableErrorsUsing(fn func(error)) {
	h.update(func(s *hookSet) { s.unrecoverable = fn })
}

// unrecoverableError hands err to the registered handler, if any.
func (s *hookSet) unrecoverableError(err error) {
	if s.unrecoverable == nil {
		return
	}

	defer func() { _ = recover() }()

	s.unrecoverable(err)
}

// errorFromPanic keeps an error or a string, and only the type of anything
// else, so a record or a request inside a panic value is never printed.
func errorFromPanic(recovered any) error {
	switch value := recovered.(type) {
	case error:
		return value
	case string:
		return errors.New(value) //nolint:err113 // the panic text itself
	default:
		return fmt.Errorf("value of type %T", value) //nolint:err113 // type only, never the value
	}
}

// filtered runs the reject callbacks and then the redaction callbacks on a
// copy of value. keep is false when a reject callback returned true, or when
// any callback panicked: a filter or a redaction that failed must not let the
// record leave the process, and must not take the application down. The panic
// goes to the unrecoverable handler.
func filtered[T any](hooks *hookSet, rejects []func(T) bool, redacts []func(*T), value T) (out T, keep bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			keep = false
			hooks.unrecoverableError(fmt.Errorf("nightwatch: a %T callback panicked: %w", value, errorFromPanic(recovered)))
		}
	}()

	for _, reject := range rejects {
		if reject(value) {
			return value, false
		}
	}

	for _, redact := range redacts {
		redact(&value)
	}

	return value, true
}
