package nightwatch

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockAgent simulates the Nightwatch agent listening on a TCP port.
// It accepts connections, reads the wire-format payload, sends "2:OK", and stores received data.
type mockAgent struct {
	listener net.Listener
	mu       sync.Mutex
	payloads []string // raw wire-format payloads received
	records  [][]Record
	done     chan struct{}
}

func startMockAgent(t *testing.T) *mockAgent {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	agent := &mockAgent{
		listener: ln,
		done:     make(chan struct{}),
	}

	go agent.serve()
	return agent
}

func (a *mockAgent) serve() {
	for {
		conn, err := a.listener.Accept()
		if err != nil {
			select {
			case <-a.done:
				return
			default:
				continue
			}
		}
		go a.handleConn(conn)
	}
}

func (a *mockAgent) handleConn(conn net.Conn) {
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Read the wire format: {length}:{content}
	// First, read byte-by-byte until we find the first colon to get the length prefix.
	var lengthBuf []byte
	oneByte := make([]byte, 1)
	for {
		_, err := conn.Read(oneByte)
		if err != nil {
			return
		}
		if oneByte[0] == ':' {
			break
		}
		lengthBuf = append(lengthBuf, oneByte[0])
	}

	length := 0
	for _, b := range lengthBuf {
		length = length*10 + int(b-'0')
	}

	// Read exactly `length` bytes of content
	content := make([]byte, length)
	_, err := io.ReadFull(conn, content)
	if err != nil {
		return
	}

	payload := string(lengthBuf) + ":" + string(content)

	// Extract JSON part: v1:{tokenHash}:{json}
	parts := strings.SplitN(string(content), ":", 3)

	a.mu.Lock()
	a.payloads = append(a.payloads, payload)

	if len(parts) >= 3 {
		var records []Record
		if err := json.Unmarshal([]byte(parts[2]), &records); err == nil {
			a.records = append(a.records, records)
		}
	}
	a.mu.Unlock()

	// Send acknowledgment
	conn.Write([]byte("2:OK"))
}

func (a *mockAgent) close() {
	close(a.done)
	a.listener.Close()
}

func (a *mockAgent) addr() string {
	return a.listener.Addr().String()
}

func (a *mockAgent) getPayloads() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]string, len(a.payloads))
	copy(result, a.payloads)
	return result
}

func (a *mockAgent) getRecords() [][]Record {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([][]Record, len(a.records))
	copy(result, a.records)
	return result
}

func TestIngest_EndToEnd(t *testing.T) {
	agent := startMockAgent(t)
	defer agent.close()

	nw, err := New(
		WithToken("test-token-123"),
		WithAddress(agent.addr()),
		WithDeployment("test-deploy"),
		WithServer("test-server"),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Test 1: Ping
	if err := nw.Ingester().Ping(); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let mock agent process

	payloads := agent.getPayloads()
	if len(payloads) < 1 {
		t.Fatal("expected at least 1 payload after ping")
	}
	if !strings.Contains(payloads[0], "PING") {
		t.Errorf("expected PING in payload, got %q", payloads[0])
	}

	// Test 2: Write records and digest
	record := Record{
		"v":         1,
		"t":         "request",
		"timestamp": 1234567890.123456,
		"deploy":    "test-deploy",
		"server":    "test-server",
		"method":    "GET",
		"url":       "/users",
	}

	if err := nw.Ingester().Write(record); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := nw.Ingester().Digest(); err != nil {
		t.Fatalf("Digest failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	records := agent.getRecords()
	if len(records) < 1 {
		t.Fatal("expected at least 1 batch of records")
	}

	batch := records[0]
	if len(batch) != 1 {
		t.Fatalf("expected 1 record in batch, got %d", len(batch))
	}

	if batch[0]["t"] != "request" {
		t.Errorf("record type = %v, want 'request'", batch[0]["t"])
	}
	if batch[0]["method"] != "GET" {
		t.Errorf("method = %v, want 'GET'", batch[0]["method"])
	}
}

func TestIngest_WireFormat(t *testing.T) {
	agent := startMockAgent(t)
	defer agent.close()

	nw, err := New(
		WithToken("test-token-123"),
		WithAddress(agent.addr()),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Send a PING and verify wire format
	if err := nw.Ingester().Ping(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	payloads := agent.getPayloads()
	if len(payloads) == 0 {
		t.Fatal("no payloads received")
	}

	wire := payloads[0]
	// Expected: {length}:v1:e9c611a:PING
	// length = len("v1") + 1 + 7 + 1 + len("PING") = 15
	expected := "15:v1:e9c611a:PING"
	if wire != expected {
		t.Errorf("wire = %q, want %q", wire, expected)
	}
}

func TestIngest_BufferAutoDigest(t *testing.T) {
	agent := startMockAgent(t)
	defer agent.close()

	nw, err := New(
		WithToken("test-token-123"),
		WithAddress(agent.addr()),
		WithBufferSize(3),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Write 3 records — buffer should auto-digest at capacity
	for i := 0; i < 3; i++ {
		_ = nw.Ingester().Write(Record{"id": i})
	}

	time.Sleep(100 * time.Millisecond)

	records := agent.getRecords()
	if len(records) < 1 {
		t.Fatal("expected auto-digest when buffer is full")
	}

	// The batch should contain 3 records
	total := 0
	for _, batch := range records {
		total += len(batch)
	}
	if total != 3 {
		t.Errorf("total records = %d, want 3", total)
	}
}

func TestIngest_ConnectionFailure_GracefulDegradation(t *testing.T) {
	// Try to connect to a port that's not listening
	nw, err := New(
		WithToken("test-token-123"),
		WithAddress("127.0.0.1:1"), // port 1 — should fail
		WithConnectionTimeout(100 * time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	// These should return errors but not panic
	err = nw.Ingester().Write(Record{"v": 1})
	// Write itself doesn't transmit unless buffer is full, so no error here
	if err != nil {
		t.Logf("Write error (unexpected but acceptable): %v", err)
	}

	err = nw.Ingester().Digest()
	if err == nil {
		t.Error("expected error when digesting to unreachable address")
	}
	fmt.Printf("Expected error: %v\n", err)
}
