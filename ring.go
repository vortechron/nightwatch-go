package nightwatch

// ring is a fixed size queue of records. Pushing into a full ring drops the
// oldest record. Push is O(1) and pop is O(n) in the records taken.
type ring struct {
	buf  []Record
	head int
	size int
}

func newRing(capacity int) ring {
	return ring{buf: make([]Record, capacity)}
}

func (r *ring) len() int { return r.size }

// push adds a record and reports whether it had to drop the oldest one.
func (r *ring) push(record Record) (dropped bool) {
	if r.size == len(r.buf) {
		r.buf[r.head] = record
		r.head = (r.head + 1) % len(r.buf)

		return true
	}

	r.buf[(r.head+r.size)%len(r.buf)] = record
	r.size++

	return false
}

// at returns the i-th oldest record.
func (r *ring) at(i int) Record {
	return r.buf[(r.head+i)%len(r.buf)]
}

// pop removes and returns up to n of the oldest records.
func (r *ring) pop(n int) []Record {
	n = min(n, r.size)
	if n == 0 {
		return nil
	}

	out := make([]Record, n)
	for i := range out {
		slot := (r.head + i) % len(r.buf)
		out[i] = r.buf[slot]
		r.buf[slot] = nil
	}

	r.head = (r.head + n) % len(r.buf)
	r.size -= n

	return out
}

func (r *ring) clear() {
	r.pop(r.size)
}
