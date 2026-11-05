package omnirules

import (
	"encoding/json"
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
	pe, ok := err.(*PayloadError)
	if !ok || pe.Status != 422 || pe.Code != "UNSUPPORTED_EVENT_TYPE" {
		t.Fatalf("got %#v", err)
	}
	_, err = d.Decode([]byte(`{"records":[{"t":"exception","handled":null}]}`), "")
	if pe, ok = err.(*PayloadError); !ok || pe.Status != 422 {
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
