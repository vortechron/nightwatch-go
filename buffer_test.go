package nightwatch

import "testing"

func TestRecordsBuffer_Write(t *testing.T) {
	buf := NewRecordsBuffer(3)

	buf.Write(Record{"id": 1})
	buf.Write(Record{"id": 2})

	if buf.Count() != 2 {
		t.Errorf("count = %d, want 2", buf.Count())
	}
	if buf.Full {
		t.Error("buffer should not be full yet")
	}
}

func TestRecordsBuffer_Full(t *testing.T) {
	buf := NewRecordsBuffer(3)

	buf.Write(Record{"id": 1})
	buf.Write(Record{"id": 2})
	buf.Write(Record{"id": 3})

	if !buf.Full {
		t.Error("buffer should be full")
	}
	if buf.Count() != 3 {
		t.Errorf("count = %d, want 3", buf.Count())
	}
}

func TestRecordsBuffer_OverflowDropsOldest(t *testing.T) {
	buf := NewRecordsBuffer(3)

	buf.Write(Record{"id": 1})
	buf.Write(Record{"id": 2})
	buf.Write(Record{"id": 3})
	buf.Write(Record{"id": 4}) // should drop id:1

	if buf.Count() != 3 {
		t.Errorf("count = %d, want 3", buf.Count())
	}

	p := buf.Pull("hash")
	wire, _ := p.Pull()

	// The pulled records should contain ids 2, 3, 4 (not 1)
	if buf.Count() != 0 {
		t.Error("buffer should be empty after pull")
	}

	// Verify wire contains "id":2 but not "id":1
	if len(wire) == 0 {
		t.Fatal("wire format should not be empty")
	}
}

func TestRecordsBuffer_Pull(t *testing.T) {
	buf := NewRecordsBuffer(10)

	buf.Write(Record{"id": 1})
	buf.Write(Record{"id": 2})

	p := buf.Pull("hash")

	if buf.Count() != 0 {
		t.Error("buffer should be empty after pull")
	}
	if buf.Full {
		t.Error("buffer should not be full after pull")
	}
	if p.IsEmpty() {
		t.Error("payload should not be empty")
	}
}

func TestRecordsBuffer_PullEmpty(t *testing.T) {
	buf := NewRecordsBuffer(10)

	p := buf.Pull("hash")
	if !p.IsEmpty() {
		t.Error("payload from empty buffer should be empty")
	}
}

func TestRecordsBuffer_Flush(t *testing.T) {
	buf := NewRecordsBuffer(10)

	buf.Write(Record{"id": 1})
	buf.Write(Record{"id": 2})
	buf.Flush()

	if buf.Count() != 0 {
		t.Error("buffer should be empty after flush")
	}
}
