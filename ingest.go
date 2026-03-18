package nightwatch

import (
	"fmt"
	"io"
	"net"
	"time"
)

// Ingest handles buffering records and transmitting them to the Nightwatch agent via TCP.
// Each Digest() opens a new TCP connection, sends the payload, reads the "2:OK" ack, and closes.
type Ingest struct {
	address           string
	connectionTimeout time.Duration
	readWriteTimeout  time.Duration
	buffer            *RecordsBuffer
	tokenHash         string
	digestOnFull      bool
}

// NewIngest creates a new Ingest instance.
func NewIngest(address string, connectionTimeout, readWriteTimeout time.Duration, buffer *RecordsBuffer, tokenHash string) *Ingest {
	return &Ingest{
		address:           address,
		connectionTimeout: connectionTimeout,
		readWriteTimeout:  readWriteTimeout,
		buffer:            buffer,
		tokenHash:         tokenHash,
		digestOnFull:      true,
	}
}

// Write adds a record to the buffer. If the buffer is full and digestOnFull is true,
// it automatically digests (transmits) the buffer contents.
func (i *Ingest) Write(record Record) error {
	i.buffer.Write(record)

	if i.digestOnFull && i.buffer.Full {
		return i.Digest()
	}
	return nil
}

// WriteNow immediately transmits a single record without buffering.
// Used for critical events like fatal errors.
func (i *Ingest) WriteNow(record Record) error {
	payload := NewJSONPayload([]Record{record}, i.tokenHash)
	return i.transmit(payload)
}

// Flush clears the buffer without transmitting.
func (i *Ingest) Flush() {
	i.buffer.Flush()
}

// Ping sends a PING message to the agent to verify connectivity.
func (i *Ingest) Ping() error {
	payload := NewTextPayload("PING", i.tokenHash)
	return i.transmit(payload)
}

// Digest pulls all records from the buffer and transmits them to the agent.
func (i *Ingest) Digest() error {
	payload := i.buffer.Pull(i.tokenHash)
	return i.transmit(payload)
}

// SetDigestOnFull controls whether the buffer auto-digests when full.
func (i *Ingest) SetDigestOnFull(enabled bool) {
	i.digestOnFull = enabled
}

func (i *Ingest) transmit(payload *Payload) error {
	if payload.IsEmpty() {
		return nil
	}

	conn, err := net.DialTimeout("tcp", i.address, i.connectionTimeout)
	if err != nil {
		return fmt.Errorf("nightwatch: failed to connect to agent at %s: %w", i.address, err)
	}
	defer conn.Close()

	// Set read/write deadline
	if err := conn.SetDeadline(time.Now().Add(i.readWriteTimeout)); err != nil {
		return fmt.Errorf("nightwatch: failed to set deadline: %w", err)
	}

	// Send payload
	data, err := payload.Pull()
	if err != nil {
		return fmt.Errorf("nightwatch: %w", err)
	}

	if _, err := io.WriteString(conn, data); err != nil {
		return fmt.Errorf("nightwatch: failed to send payload: %w", err)
	}

	// Wait for acknowledgment: "2:OK" (4 bytes)
	ack := make([]byte, 4)
	if _, err := io.ReadFull(conn, ack); err != nil {
		return fmt.Errorf("nightwatch: failed to read acknowledgment: %w", err)
	}

	if string(ack) != "2:OK" {
		return fmt.Errorf("nightwatch: unexpected response from agent [%s]", string(ack))
	}

	return nil
}
