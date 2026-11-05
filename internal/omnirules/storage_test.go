package omnirules

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const goodUUID = "0b9e3f6a-6d0c-4d6e-9d77-1d2a5c0f8a11"

func decode(t *testing.T, raw string) Event {
	t.Helper()
	var e Event
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&e); err != nil {
		t.Fatal(err)
	}

	return e
}

func TestCheckStorage(t *testing.T) {
	tests := []struct {
		name    string
		record  string
		wantErr string
	}{
		{"valid request", `{"t":"request","trace_id":"` + goodUUID + `","_group":"","duration":5,"route_methods":["GET"],"timestamp":1.5}`, ""},
		{"bad trace id", `{"t":"request","trace_id":"nope"}`, "trace_id"},
		{"empty trace id on request", `{"t":"request","trace_id":""}`, "trace_id"},
		{"user needs no trace", `{"t":"user","id":"1"}`, ""},
		{"group too short", `{"t":"query","trace_id":"` + goodUUID + `","_group":"abc"}`, "_group"},
		{"null value", `{"t":"query","trace_id":"` + goodUUID + `","sql":null}`, "sql"},
		{"null route methods", `{"t":"request","trace_id":"` + goodUUID + `","route_methods":null}`, "route_methods"},
		{"bad cache type", `{"t":"cache-event","trace_id":"` + goodUUID + `","type":"oops"}`, "type"},
		{"good cache type", `{"t":"cache-event","trace_id":"` + goodUUID + `","type":"write-failure"}`, ""},
		{"bad job status", `{"t":"job-attempt","trace_id":"` + goodUUID + `","attempt_id":"` + goodUUID + `","status":"x"}`, "status"},
		{"bad attempt id", `{"t":"job-attempt","trace_id":"` + goodUUID + `","attempt_id":"1"}`, "attempt_id"},
		{"bad task status", `{"t":"scheduled-task","trace_id":"` + goodUUID + `","status":"done"}`, "status"},
		{"float duration", `{"t":"query","trace_id":"` + goodUUID + `","duration":1.5}`, "duration"},
		{"negative line", `{"t":"query","trace_id":"` + goodUUID + `","line":-1}`, "line"},
		{"string duration", `{"t":"query","trace_id":"` + goodUUID + `","duration":"5"}`, "duration"},
		{"number sql", `{"t":"query","trace_id":"` + goodUUID + `","sql":5}`, "sql"},
		{"bad execution id", `{"t":"query","trace_id":"` + goodUUID + `","execution_id":"zzz"}`, "execution_id"},
		{"string timestamp", `{"t":"query","trace_id":"` + goodUUID + `","timestamp":"now"}`, "timestamp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckStorage(decode(t, tt.record), 0)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}

				return
			}
			if err == nil || err.Context["field"] != tt.wantErr || err.Status != 503 {
				t.Fatalf("want field %q rejected with 503, got %+v", tt.wantErr, err)
			}
		})
	}
}

func TestDecoderRejectsWholeBatch(t *testing.T) {
	d := NewDecoder(DecoderLimits{MaxBodyBytes: 1 << 20, MaxDecompressedBytes: 1 << 20, MaxRecords: 10})
	_, err := d.Decode([]byte(`{"records":[{"t":"log"},{"t":"nope"}]}`), "")
	var pe *PayloadError
	ok := errors.As(err, &pe)
	if !ok || pe.Status != 422 || pe.Code != "UNSUPPORTED_EVENT_TYPE" {
		t.Fatalf("got %#v", err)
	}
	_, err = d.Decode([]byte(`{"records":[{"t":"exception","handled":null}]}`), "")
	if ok = errors.As(err, &pe); !ok || pe.Status != 422 {
		t.Fatalf("null boolean should be 422, got %#v", err)
	}
	if _, err = d.Decode([]byte(`{"records":[{"t":"log","context":"{}"}]}`), ""); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBody(t *testing.T) {
	if _, err := Validate([]byte(`{"records":[{"t":"log","trace_id":"`+goodUUID+`","context":"{}"}]}`), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate([]byte(`{"records":[{"t":"log","trace_id":"bad"}]}`), ""); err == nil || err.Status != 503 {
		t.Fatalf("got %v", err)
	}
	if _, err := Validate([]byte(`not json`), ""); err == nil || err.Status != 400 {
		t.Fatalf("got %v", err)
	}
	if _, err := Validate([]byte(`x`), "br"); err == nil || err.Status != 415 {
		t.Fatalf("got %v", err)
	}
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()

	return b.Bytes()
}

func TestDecoder_Limits(t *testing.T) {
	d := NewDecoder(DecoderLimits{MaxBodyBytes: 200, MaxDecompressedBytes: 120, MaxRecords: 2})
	tests := []struct {
		name     string
		body     []byte
		encoding string
		status   int
		code     string
	}{
		{"gzip ok", gz(t, `{"records":[{"t":"log"}]}`), "gzip", 0, ""},
		{"bad gzip", []byte("not gzip"), "gzip", 400, ""},
		{"bomb", gz(t, strings.Repeat(" ", 500)+`{"records":[]}`), "gzip", 413, ""},
		{"too large", []byte(strings.Repeat("x", 300)), "", 413, ""},
		{"unsupported encoding", []byte(`{}`), "br", 415, ""},
		{"too many records", []byte(`{"records":[{"t":"log"},{"t":"log"},{"t":"log"}]}`), "", 413, "BATCH_TOO_LARGE"},
		{"flat list", []byte(`[{"t":"log"}]`), "", 0, ""},
		{"events wrapper", []byte(`{"events":[{"t":"log"}]}`), "", 0, ""},
		{"no wrapper", []byte(`{"x":1}`), "", 422, ""},
		{"records not a list", []byte(`{"records":1}`), "", 422, ""},
		{"scalar", []byte(`5`), "", 422, ""},
		{"record not object", []byte(`{"records":[1]}`), "", 422, ""},
		{"missing type", []byte(`{"records":[{"x":1}]}`), "", 422, ""},
		{"bad json field", []byte(`{"records":[{"t":"log","context":5}]}`), "", 422, ""},
		{"bad boolean", []byte(`{"records":[{"t":"exception","handled":2}]}`), "", 422, ""},
		{"numeric boolean", []byte(`{"records":[{"t":"exception","handled":1}]}`), "", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := d.Decode(tt.body, tt.encoding)
			var pe *PayloadError
			if tt.status == 0 {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}

				return
			}
			if !errors.As(err, &pe) || pe.Status != tt.status || pe.Code != tt.code {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestSchemaHelpers(t *testing.T) {
	if table, ok := TableFor("request"); !ok || table != "requests" {
		t.Fatal("table")
	}
	if !Supports("mail") || Supports("nope") || len(BooleanFields()) == 0 || len(JSONFields()) != 4 || len(Tables()) != 13 {
		t.Fatal("schema helpers")
	}
	if (&PayloadError{Message: "m", Status: 422}).Error() == "" {
		t.Fatal("error text")
	}
}
