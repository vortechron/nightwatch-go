package nightwatch

import (
	"errors"
	"fmt"
	"slices"
)

// ErrBatchDropped is wrapped by the errors the HandleUnrecoverableErrorsUsing
// handler gets when records could not be delivered: Omni refused a batch, the
// retries ran out, the event quota was reached, or the client shut down first.
var ErrBatchDropped = errors.New("nightwatch: a batch of records was dropped")

// IngestingEvents is what an OnIngesting listener gets before a batch is sent
// (Laravel's Laravel\Nightwatch\Events\IngestingEvents). Records is a deep
// copy, nested lists included: changing it changes nothing that is sent.
type IngestingEvents struct {
	Records []Record
}

// EventCount is the number of events in the batch. User records do not count,
// as in Laravel.
func (e IngestingEvents) EventCount() int {
	count := 0

	for _, record := range e.Records {
		if record.Type() != RecordUser {
			count++
		}
	}

	return count
}

// OnIngesting registers a listener that runs before each batch is sent. When
// a listener returns false the batch is dropped and counted in
// Stats().Refused, and the later listeners do not run. Combined with a rate
// limiter this caps how many events reach Omni. A listener that panics drops
// the batch too, and the panic goes to the unrecoverable handler.
func (h *Hooks) OnIngesting(fn func(IngestingEvents) bool) {
	h.update(func(s *hookSet) { s.ingesting = add(s.ingesting, fn) })
}

// ingestAllowed runs the OnIngesting listeners on a copy of the batch.
func (s *hookSet) ingestAllowed(batch []Record) (allowed bool) {
	if len(s.ingesting) == 0 {
		return true
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			allowed = false
			s.unrecoverableError(fmt.Errorf("nightwatch: an OnIngesting listener panicked: %w", errorFromPanic(recovered)))
		}
	}()

	event := IngestingEvents{Records: make([]Record, len(batch))}
	for i, record := range batch {
		event.Records[i] = cloneRecord(record)
	}

	for _, listener := range s.ingesting {
		if !listener(event) {
			return false
		}
	}

	return true
}

// cloneRecord copies a record and the lists in it. Records hold plain values,
// []string (route_methods) and JSON text for everything nested.
func cloneRecord(record Record) Record {
	out := make(Record, len(record))
	for key, value := range record {
		if list, ok := value.([]string); ok {
			value = slices.Clone(list)
		}

		out[key] = value
	}

	return out
}
