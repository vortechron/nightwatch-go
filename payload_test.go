package nightwatch

import (
	"strings"
	"testing"
)

func TestPayload_Pull_Format(t *testing.T) {
	// Test that the wire format matches: {length}:v1:{tokenHash}:{body}
	// where length = len("v1") + 1 + len(tokenHash) + 1 + len(body)

	tokenHash := "abc1234" // 7 chars

	t.Run("JSON payload", func(t *testing.T) {
		records := []Record{{"v": 1, "t": "request"}}
		p := NewJSONPayload(records, tokenHash)

		wire, err := p.Pull()
		if err != nil {
			t.Fatal(err)
		}

		// Parse the wire format
		parts := strings.SplitN(wire, ":", 2)
		if len(parts) != 2 {
			t.Fatalf("expected format {length}:{rest}, got %q", wire)
		}

		// The rest should be "v1:{tokenHash}:{json}"
		rest := parts[1]
		if !strings.HasPrefix(rest, "v1:"+tokenHash+":") {
			t.Errorf("expected rest to start with 'v1:%s:', got %q", tokenHash, rest)
		}

		t.Logf("Wire format: %s", wire)
	})

	t.Run("Text payload", func(t *testing.T) {
		p := NewTextPayload("PING", tokenHash)

		wire, err := p.Pull()
		if err != nil {
			t.Fatal(err)
		}

		// Verify: length = len("v1") + 1 + 7 + 1 + len("PING") = 2+1+7+1+4 = 15
		expected := "15:v1:abc1234:PING"
		if wire != expected {
			t.Errorf("got %q, want %q", wire, expected)
		}
	})

	t.Run("Empty payload", func(t *testing.T) {
		p := NewJSONPayload([]Record{}, tokenHash)
		if !p.IsEmpty() {
			t.Error("empty records should produce an empty payload")
		}
	})

	t.Run("Double pull", func(t *testing.T) {
		p := NewTextPayload("PING", tokenHash)
		_, _ = p.Pull()
		_, err := p.Pull()
		if err == nil {
			t.Error("expected error on double pull")
		}
	})
}

func TestPayload_LengthCalculation(t *testing.T) {
	// Verify the length calculation matches PHP's:
	// $length = strlen("v1") + 1 + strlen($tokenHash) + 1 + strlen($payload)
	tokenHash := "abc1234"
	body := `[{"v":1}]`

	p := &Payload{
		body:      body,
		tokenHash: tokenHash,
	}

	wire, err := p.Pull()
	if err != nil {
		t.Fatal(err)
	}

	// Expected length: len("v1")=2 + 1 + len("abc1234")=7 + 1 + len(body)
	expectedLength := 2 + 1 + 7 + 1 + len(body)

	// Parse actual length from wire
	colonIdx := strings.Index(wire, ":")
	lengthStr := wire[:colonIdx]

	if lengthStr != "20" { // 2+1+7+1+9 = 20
		t.Errorf("length prefix = %s, want %d (body=%q, len=%d)", lengthStr, expectedLength, body, len(body))
	}
}
