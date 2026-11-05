package nightwatch

import (
	"context"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var defaultRedact = []string{"Authorization", "Cookie", "Proxy-Authorization", "Set-Cookie", "X-CSRF-TOKEN", "X-XSRF-TOKEN"}

func TestRedactHeadersJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]any
	}{
		{"exact names", `{"authorization":["Bearer abc"],"user-agent":["curl"]}`, map[string]any{"authorization": []any{"[redacted]"}, "user-agent": []any{"curl"}}},
		{"mixed case names", `{"AUTHORIZATION":["x"],"Cookie":["a=1"],"X-Xsrf-Token":["t"],"x-csrf-token":["c"],"SET-COOKIE":["s=1","k=2"]}`,
			map[string]any{"AUTHORIZATION": []any{"[redacted]"}, "Cookie": []any{"[redacted]"}, "X-Xsrf-Token": []any{"[redacted]"}, "x-csrf-token": []any{"[redacted]"}, "SET-COOKIE": []any{"[redacted]", "[redacted]"}}},
		{"string value", `{"proxy-authorization":"Basic zzz"}`, map[string]any{"proxy-authorization": "[redacted]"}},
		{"nothing to redact", `{"accept":["*/*"]}`, map[string]any{"accept": []any{"*/*"}}},
		{"bad json falls back", `{"authorization":`, map[string]any{}},
		{"not an object falls back", `["Bearer abc"]`, map[string]any{}},
		{"empty falls back", ``, map[string]any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactHeadersJSON(tt.in, defaultRedact)
			var back map[string]any
			if err := json.Unmarshal([]byte(got), &back); err != nil {
				t.Fatalf("output is not JSON: %q", got)
			}
			if len(back) != len(tt.want) {
				t.Fatalf("got %s", got)
			}
			for key, want := range tt.want {
				wantJSON, _ := json.Marshal(want)
				gotJSON, _ := json.Marshal(back[key])
				if string(wantJSON) != string(gotJSON) {
					t.Fatalf("%s: got %s want %s", key, gotJSON, wantJSON)
				}
			}
		})
	}
}

func TestRedactHeadersJSON_ExtraNames(t *testing.T) {
	got := RedactHeadersJSON(`{"x-api-key":["k"],"accept":["a"]}`, []string{"X-API-Key"})
	if got != `{"accept":["a"],"x-api-key":["[redacted]"]}` {
		t.Fatalf("got %s", got)
	}
}

func TestHeadersToJSON_RedactsAtCaptureTime(t *testing.T) {
	got := HeadersToJSON(map[string][]string{"Authorization": {"Bearer secret"}, "User-Agent": {"curl"}}, defaultRedact)
	if got != `{"authorization":["[redacted]"],"user-agent":["curl"]}` {
		t.Fatalf("got %s", got)
	}
}

func TestConfig_RedactHeadersAlwaysIncludeSecrets(t *testing.T) {
	cfg := ConfigFromEnv(env(map[string]string{"NIGHTWATCH_TOKEN": "t", "NIGHTWATCH_REDACT_HEADERS": "X-Api-Key"}), "h")
	for _, name := range append([]string{"X-Api-Key"}, defaultRedact...) {
		found := false
		for _, have := range cfg.RedactHeaders {
			if equalFold(have, name) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s missing from %v", name, cfg.RedactHeaders)
		}
	}
}

func TestTranslator_RequestHeadersNeverCarrySecrets(t *testing.T) {
	h := newHarness(t, nil)
	h.request(context.Background(), 200, func(_ context.Context, span trace.Span) {
		span.SetAttributes(attribute.String(AttrHeaders, `{"Authorization":["Bearer topsecret"],"cookie":["sid=abc"],"accept":["*/*"]}`))
	})
	got := h.sink.ofType("request")[0]["headers"].(string)
	if got != `{"Authorization":["[redacted]"],"accept":["*/*"],"cookie":["[redacted]"]}` {
		t.Fatalf("headers: %s", got)
	}
}

func TestTranslator_BadHeadersJSONNeverPassedThrough(t *testing.T) {
	h := newHarness(t, nil)
	h.request(context.Background(), 200, func(_ context.Context, span trace.Span) {
		span.SetAttributes(attribute.String(AttrHeaders, `Authorization: Bearer topsecret`))
	})
	if got := h.sink.ofType("request")[0]["headers"]; got != "{}" {
		t.Fatalf("headers: %v", got)
	}
}

func TestRedactPayloadJSON(t *testing.T) {
	fields := []string{"_token", "password", "password_confirmation"}
	got := RedactPayloadJSON(`{"name":"a","Password":"p","nested":{"_token":"x","list":[{"password":"q"}]}}`, fields)
	want := `{"Password":"[redacted]","name":"a","nested":{"_token":"[redacted]","list":[{"password":"[redacted]"}]}}`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if RedactPayloadJSON(`password=hunter2`, fields) != "" {
		t.Fatal("non JSON payload must never pass through")
	}
}

func TestTranslator_PayloadOffByDefault(t *testing.T) {
	h := newHarness(t, nil)
	h.request(context.Background(), 500, func(_ context.Context, span trace.Span) {
		span.SetAttributes(attribute.String(AttrPayload, `{"password":"p","a":1}`))
	})
	if got := h.sink.ofType("request")[0]["payload"]; got != "" {
		t.Fatalf("payload must stay empty by default: %v", got)
	}
}

func TestTranslator_PayloadOnlyOn500WhenEnabledAndRedacted(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.CaptureRequestPayload = true })
	set := func(_ context.Context, span trace.Span) {
		span.SetAttributes(attribute.String(AttrPayload, `{"password":"p","a":1}`))
	}
	h.request(context.Background(), 200, set)
	h.request(context.Background(), 500, set)
	reqs := h.sink.ofType("request")
	if reqs[0]["payload"] != "" {
		t.Fatalf("non-500 must not carry payload: %v", reqs[0]["payload"])
	}
	if reqs[1]["payload"] != `{"a":1,"password":"[redacted]"}` {
		t.Fatalf("payload: %v", reqs[1]["payload"])
	}
}
