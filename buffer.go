package nightwatch

import "sync"

const defaultBufferSize = 500

// RecordsBuffer is a thread-safe, fixed-capacity buffer for telemetry records.
// When full, the oldest record is dropped (FIFO). Matches PHP RecordsBuffer behavior.
type RecordsBuffer struct {
	mu      sync.Mutex
	records []Record
	maxLen  int
	Full    bool
}

// Record is a map representing a single telemetry record to be sent to the backend.
type Record map[string]any

// NewRecordsBuffer creates a buffer with the given capacity.
func NewRecordsBuffer(maxLen int) *RecordsBuffer {
	if maxLen <= 0 {
		maxLen = defaultBufferSize
	}
	return &RecordsBuffer{
		records: make([]Record, 0, maxLen),
		maxLen:  maxLen,
	}
}

// Write adds a record to the buffer. If full, drops the oldest record first.
func (b *RecordsBuffer) Write(record Record) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.Full {
		// Drop oldest (array_shift equivalent)
		b.records = b.records[1:]
	}

	b.records = append(b.records, record)
	b.Full = len(b.records) >= b.maxLen
}

// Pull returns all buffered records as a Payload and clears the buffer.
func (b *RecordsBuffer) Pull(tokenHash string) *Payload {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.records) == 0 {
		return NewJSONPayload([]Record{}, tokenHash)
	}

	records := b.records
	b.records = make([]Record, 0, b.maxLen)
	b.Full = false

	return NewJSONPayload(records, tokenHash)
}

// Flush clears the buffer without returning any records.
func (b *RecordsBuffer) Flush() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.records = b.records[:0]
	b.Full = false
}

// Count returns the number of records currently in the buffer.
func (b *RecordsBuffer) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.records)
}
