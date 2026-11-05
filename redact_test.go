package nightwatch

import (
	"context"
	"encoding/json"
	"strings"
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
		{
			"mixed case names", `{"AUTHORIZATION":["x"],"Cookie":["a=1"],"X-Xsrf-Token":["t"],"x-csrf-token":["c"],"SET-COOKIE":["s=1","k=2"]}`,
			map[string]any{"AUTHORIZATION": []any{"[redacted]"}, "Cookie": []any{"[redacted]"}, "X-Xsrf-Token": []any{"[redacted]"}, "x-csrf-token": []any{"[redacted]"}, "SET-COOKIE": []any{"[redacted]", "[redacted]"}},
		},
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

var defaultQueryRedact = []string{
	"password", "password_confirmation", "token", "access_token", "refresh_token", "id_token", "api_key", "apikey",
	"key", "secret", "client_secret", "signature", "code", "auth",
}

func TestRedactQuery(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"nothing to redact", "a=1&b=two", "a=1&b=two"},
		{"token", "token=abc&page=2", "page=2&token=%5Bredacted%5D"},
		{"mixed case names", "Access_Token=x&API_KEY=y&Password=z&SIGNATURE=s&Code=c", "API_KEY=%5Bredacted%5D&Access_Token=%5Bredacted%5D&Code=%5Bredacted%5D&Password=%5Bredacted%5D&SIGNATURE=%5Bredacted%5D"},
		{
			"every default name", "password=1&password_confirmation=1&token=1&access_token=1&refresh_token=1&id_token=1&api_key=1&apikey=1&key=1&secret=1&client_secret=1&signature=1&code=1&auth=1",
			"access_token=%5Bredacted%5D&api_key=%5Bredacted%5D&apikey=%5Bredacted%5D&auth=%5Bredacted%5D&client_secret=%5Bredacted%5D&code=%5Bredacted%5D&id_token=%5Bredacted%5D&key=%5Bredacted%5D&password=%5Bredacted%5D&password_confirmation=%5Bredacted%5D&refresh_token=%5Bredacted%5D&secret=%5Bredacted%5D&signature=%5Bredacted%5D&token=%5Bredacted%5D",
		},
		{"repeated key", "token=a&token=b&x=1", "token=%5Bredacted%5D&token=%5Bredacted%5D&x=1"},
		{"similar names are kept", "tokenizer=1&mykey=2", "mykey=2&tokenizer=1"},
		{"key without value", "token&x", "token=%5Bredacted%5D&x="},
		{"deterministic order", "b=2&a=1", "a=1&b=2"},
		{"empty", "", ""},
		{"unparseable query is dropped", "a=%zz&token=abc", ""},
		{"bad escape only", "x=%", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactQuery(tt.in, defaultQueryRedact); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
	if got := RedactQuery("x_api=1&token=2", []string{"X_Api"}); got != "token=2&x_api=%5Bredacted%5D" {
		t.Fatalf("extra names: %q", got)
	}
}

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "https://example.com/a/b", "https://example.com/a/b"},
		{"query kept when safe", "https://example.com/a?x=1", "https://example.com/a?x=1"},
		{"token redacted", "https://example.com/reset?token=abc&email=a%40b.c", "https://example.com/reset?email=a%40b.c&token=%5Bredacted%5D"},
		{"fragment dropped with the query", "https://example.com/a?x=1#frag", "https://example.com/a?x=1"},
		{"unparseable query dropped", "https://example.com/a?token=%zz", "https://example.com/a"},
		{"relative", "/x?api_key=1", "/x?api_key=%5Bredacted%5D"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactURL(tt.in, defaultQueryRedact); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestConfig_RedactQueryAddsToTheDefaults(t *testing.T) {
	cfg := ConfigFromEnv(env(map[string]string{"NIGHTWATCH_TOKEN": "t", "NIGHTWATCH_REDACT_QUERY": "session, X_Pin"}), "h")
	for _, name := range append([]string{"session", "X_Pin"}, defaultQueryRedact...) {
		if !matches(name, cfg.RedactQuery) {
			t.Errorf("%s missing from %v", name, cfg.RedactQuery)
		}
	}
	if plain := ConfigFromEnv(env(map[string]string{"NIGHTWATCH_TOKEN": "t"}), "h"); len(plain.RedactQuery) != len(defaultQueryRedact) {
		t.Fatalf("defaults: %v", plain.RedactQuery)
	}
}

func TestTranslator_RequestUrlNeverCarriesSecretsInTheQuery(t *testing.T) {
	cases := map[string][]attribute.KeyValue{
		"built from path and query": {attribute.String(attrURLQuery, "token=abc&page=2")},
		"url.full":                  {attribute.String("url.full", "http://h/api/tasks/7?access_token=abc&page=2")},
		"unparseable query":         {attribute.String(attrURLQuery, "page=%zz&token=abc")},
	}
	want := map[string]string{
		"built from path and query": "http://127.0.0.1:2601/api/tasks/7?page=2&token=%5Bredacted%5D",
		"url.full":                  "http://h/api/tasks/7?access_token=%5Bredacted%5D&page=2",
		"unparseable query":         "http://127.0.0.1:2601/api/tasks/7",
	}
	for name, attrs := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil)
			h.request(context.Background(), 200, func(_ context.Context, span trace.Span) { span.SetAttributes(attrs...) })
			got := h.sink.ofType("request")[0]["url"].(string)
			if got != want[name] || strings.Contains(got, "abc") {
				t.Fatalf("url %q want %q", got, want[name])
			}
		})
	}
}

func TestTranslator_OutgoingUrlIsRedactedToo(t *testing.T) {
	h := newHarness(t, nil)
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		_, out := h.tracer.Start(ctx, "GET", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
			attribute.String("http.request.method", "GET"), attribute.String("url.full", "https://api.example.com/x?api_key=secret&q=1"),
			attribute.String("server.address", "api.example.com")))
		out.End()
	})
	got := h.sink.ofType("outgoing-request")[0]["url"].(string)
	if got != "https://api.example.com/x?api_key=%5Bredacted%5D&q=1" {
		t.Fatalf("got %q", got)
	}
}

func TestHandBuiltConfigStillRedactsByDefault(t *testing.T) {
	cfg := Config{}.withDefaults()
	if !matches("token", cfg.RedactQuery) || !matches("Authorization", cfg.RedactHeaders) {
		t.Fatalf("defaults missing: %v %v", cfg.RedactQuery, cfg.RedactHeaders)
	}
}
