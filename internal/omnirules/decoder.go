package omnirules

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

// DecoderLimits mirrors the omni.ingestion.* limits in the Laravel config.
type DecoderLimits struct {
	MaxBodyBytes         int
	MaxDecompressedBytes int
	MaxRecords           int
}

// Decoder turns a raw ingest request body into a validated Batch.
//
// It is a direct port of app/Services/Ingestion/IngestPayloadDecoder.php: the
// same limits, the same rejection messages and the same HTTP statuses, so an
// agent sees identical behaviour from either implementation.
type Decoder struct {
	limits DecoderLimits
}

func NewDecoder(limits DecoderLimits) *Decoder {
	return &Decoder{limits: limits}
}

// Decode validates the body and returns the records it carries.
func (d *Decoder) Decode(content []byte, contentEncoding string) (Batch, error) {
	if len(content) > d.limits.MaxBodyBytes {
		return Batch{}, newPayloadError("Payload too large", 413)
	}

	encoding, err := normalizeContentEncoding(contentEncoding)
	if err != nil {
		return Batch{}, err
	}

	decoded := content
	if encoding == encodingGzip {
		decoded, err = d.gunzip(content)
		if err != nil {
			return Batch{}, err
		}
	}

	var payload any
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	// Keep numbers exact. Durations are microsecond UInt64 values that lose
	// precision once they pass through a float64.
	decoder.UseNumber()

	if err := decoder.Decode(&payload); err != nil {
		return Batch{}, newPayloadError("Invalid JSON payload", 400)
	}

	records, err := extractRecords(payload)
	if err != nil {
		return Batch{}, err
	}

	if len(records) > d.limits.MaxRecords {
		return Batch{}, newCodedPayloadError("Too many records in a single batch", 413, "BATCH_TOO_LARGE", map[string]any{
			"max_records":      d.limits.MaxRecords,
			"records_received": len(records),
		})
	}

	events, err := validateRecords(records)
	if err != nil {
		return Batch{}, err
	}

	return Batch{Events: events, PayloadBytes: len(decoded), Encoding: encoding}, nil
}

func normalizeContentEncoding(contentEncoding string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(contentEncoding))

	if normalized == "" || normalized == "identity" {
		return "", nil
	}

	if normalized != encodingGzip {
		return "", newPayloadError("Unsupported content encoding", 415)
	}

	return encodingGzip, nil
}

func (d *Decoder) gunzip(content []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, newPayloadError("Failed to decompress payload", 400)
	}
	defer reader.Close()

	limit := d.limits.MaxDecompressedBytes
	if limit < 1 {
		limit = 1
	}

	// Read one byte past the limit so an oversized payload is detectable
	// without ever holding the whole gzip bomb in memory.
	decoded, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, newPayloadError("Failed to decompress payload", 400)
	}

	if len(decoded) > limit {
		return nil, newPayloadError("Payload too large", 413)
	}

	return decoded, nil
}

func extractRecords(payload any) ([]any, error) {
	wrapperError := newPayloadError("Payload must be a records wrapper or a flat array of records", 422)

	switch typed := payload.(type) {
	case []any:
		return typed, nil
	case map[string]any:
		records, ok := typed["records"]
		if !ok {
			records, ok = typed["events"]
		}

		if !ok {
			return nil, wrapperError
		}

		list, ok := records.([]any)
		if !ok {
			return nil, newPayloadError("Records must be a list", 422)
		}

		return list, nil
	default:
		return nil, wrapperError
	}
}

func validateRecords(records []any) ([]Event, error) {
	events := make([]Event, 0, len(records))

	for index, record := range records {
		object, ok := record.(map[string]any)
		if !ok {
			return nil, newPayloadError("Each record must be an object", 422)
		}

		eventType, ok := object["t"].(string)
		if !ok || eventType == "" {
			return nil, newCodedPayloadError("Each record must have a valid event type", 422, "", map[string]any{
				keyRecordIndex: index,
			})
		}

		if !Supports(eventType) {
			return nil, newCodedPayloadError("Unsupported event type", 422, "UNSUPPORTED_EVENT_TYPE", map[string]any{
				keyRecordIndex: index,
				"event_type":   eventType,
			})
		}

		if err := ensureSafeFieldTypes(object, index); err != nil {
			return nil, err
		}

		events = append(events, Event(object))
	}

	return events, nil
}

func ensureSafeFieldTypes(event map[string]any, index int) error {
	for _, field := range booleanFields {
		value, ok := event[field]
		if !ok {
			continue
		}

		if !isBooleanLike(value) {
			return newCodedPayloadError("Boolean telemetry fields must be booleans", 422, "", map[string]any{
				keyRecordIndex: index,
				keyField:       field,
			})
		}
	}

	for _, field := range jsonFields {
		value, ok := event[field]
		if !ok {
			continue
		}

		switch value.(type) {
		case string, []any, map[string]any:
		default:
			return newCodedPayloadError("JSON telemetry fields must be objects, arrays, or strings", 422, "", map[string]any{
				keyRecordIndex: index,
				keyField:       field,
			})
		}
	}

	return nil
}

// isBooleanLike accepts the same values as the Laravel decoder: a real
// boolean, or the integers 0 and 1.
func isBooleanLike(value any) bool {
	switch typed := value.(type) {
	case bool:
		return true
	case json.Number:
		parsed, err := strconv.ParseInt(typed.String(), 10, 64)

		return err == nil && (parsed == 0 || parsed == 1)
	default:
		return false
	}
}
