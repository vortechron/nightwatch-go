// Package omnirules ports the ingest validation rules of Omni's Go receiver
// (omni-api app/telemetry) so tests and the omni-capture tool reject exactly
// what Omni rejects.
package omnirules

import "fmt"

// Event is one decoded telemetry record.
type Event map[string]any

// Batch is a decoded, validated ingest payload.
type Batch struct {
	Events       []Event
	PayloadBytes int
	Encoding     string
}

// PayloadError is a rejection with the HTTP status Omni would answer with.
type PayloadError struct {
	Message string
	Status  int
	Code    string
	Context map[string]any
}

func (e *PayloadError) Error() string {
	return fmt.Sprintf("ingest payload rejected (%d): %s", e.Status, e.Message)
}

func newPayloadError(message string, status int) *PayloadError {
	return &PayloadError{Message: message, Status: status}
}

func newCodedPayloadError(message string, status int, code string, context map[string]any) *PayloadError {
	return &PayloadError{Message: message, Status: status, Code: code, Context: context}
}
