package nightwatch

import (
	"encoding/json"
	"fmt"
)

const payloadVersion = "v1"

// Payload represents a wire-format payload ready to be sent to the Nightwatch agent.
// Format: {length}:v1:{tokenHash}:{body}
// Where length = len("v1") + 1 + len(tokenHash) + 1 + len(body)
type Payload struct {
	body      string
	tokenHash string
	pulled    bool
	empty     bool
}

// NewJSONPayload creates a payload from a slice of records, JSON-encoded.
func NewJSONPayload(records []Record, tokenHash string) *Payload {
	data, err := json.Marshal(records)
	if err != nil {
		// Graceful degradation: if we can't serialize, return an empty payload
		return &Payload{tokenHash: tokenHash, empty: true}
	}

	body := string(data)
	empty := body == "[]" || body == "{}" || body == `""` || body == "null"

	return &Payload{
		body:      body,
		tokenHash: tokenHash,
		empty:     empty,
	}
}

// NewTextPayload creates a payload from a raw text string (e.g., "PING").
func NewTextPayload(text string, tokenHash string) *Payload {
	return &Payload{
		body:      text,
		tokenHash: tokenHash,
		empty:     text == "",
	}
}

// Pull returns the wire-format string and marks the payload as consumed.
// Format: {length}:v1:{tokenHash}:{body}
func (p *Payload) Pull() (string, error) {
	if p.pulled {
		return "", fmt.Errorf("payload has already been read")
	}
	p.pulled = true

	body := p.body
	p.body = ""

	// length counts everything after the first colon: "v1" + ":" + tokenHash + ":" + body
	length := len(payloadVersion) + 1 + len(p.tokenHash) + 1 + len(body)

	return fmt.Sprintf("%d:%s:%s:%s", length, payloadVersion, p.tokenHash, body), nil
}

// IsEmpty returns true if the payload has no meaningful content.
func (p *Payload) IsEmpty() bool {
	return p.empty
}
