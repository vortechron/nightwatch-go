package nightwatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

func emitWith(h *harness, attrs ...otellog.KeyValue) Record {
	var record otellog.Record
	record.SetBody(otellog.StringValue("hello"))
	record.SetSeverity(otellog.SeverityInfo)
	record.AddAttributes(attrs...)
	h.logger.Emit(context.Background(), record)

	logs := h.sink.ofType("log")

	return logs[len(logs)-1]
}

func contextOf(t *testing.T, record Record) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(record["context"].(string)), &decoded); err != nil {
		t.Fatal(err)
	}

	return decoded
}

func TestLogContext_NeverCarriesRequestDumpsOrSecrets(t *testing.T) {
	h := newHarness(t, nil)
	record := emitWith(h,
		otellog.Map("request", otellog.String("Authorization", "Bearer topsecret"), otellog.String("body", "password=hunter2")),
		otellog.Map("response", otellog.String("set-cookie", "sid=abc")),
		otellog.String("user", "{ID:7 Email:a@b.c Password:hash}"),
		otellog.String("owner", "struct dump"),
		otellog.Map("trace", otellog.String("root", "frames")),
		otellog.Map("headers", otellog.String("cookie", "sid=abc")),
		otellog.String("password", "hunter2"),
		otellog.String("DB_PASSWORD", "hunter2"),
		otellog.String("api_token", "tok-9876"),
		otellog.String("Authorization", "Bearer x"),
		otellog.Map("form", otellog.String("name", "Ada"), otellog.String("password_confirmation", "p"),
			otellog.Map("deep", otellog.String("client_secret", "s"), otellog.String("ok", "fine"))),
		otellog.String("task", "reindex"),
		otellog.Int("count", 3),
	)

	raw := record["context"].(string)
	for _, secret := range []string{"topsecret", "hunter2", "sid=abc", "a@b.c", "struct dump", "frames", "tok-9876", `"Bearer x"`} {
		if strings.Contains(raw, secret) {
			t.Fatalf("context leaks %q: %s", secret, raw)
		}
	}

	got := contextOf(t, record)
	for _, dropped := range []string{"request", "response", "user", "owner", "trace", "headers"} {
		if _, present := got[dropped]; present {
			t.Errorf("%q must be dropped, it can hold the request context", dropped)
		}
	}
	if got["password"] != "[redacted]" || got["DB_PASSWORD"] != "[redacted]" || got["api_token"] != "[redacted]" || got["Authorization"] != "[redacted]" {
		t.Fatalf("secret looking keys must be redacted: %v", got)
	}
	form := got["form"].(map[string]any)
	deep := form["deep"].(map[string]any)
	if form["name"] != "Ada" || form["password_confirmation"] != "[redacted]" || deep["client_secret"] != "[redacted]" || deep["ok"] != "fine" {
		t.Fatalf("nested values: %v", form)
	}
	if got["task"] != "reindex" || got["count"] != float64(3) {
		t.Fatalf("ordinary values stay: %v", got)
	}
}

func TestLogContext_IsBounded(t *testing.T) {
	h := newHarness(t, nil)

	big := strings.Repeat("x", maxLogValueBytes*3)
	record := emitWith(h, otellog.String("blob", big), otellog.String("small", "ok"))
	got := contextOf(t, record)
	if len(got["blob"].(string)) > maxLogValueBytes+len("…") || got["small"] != "ok" {
		t.Fatalf("long values are cut: %d", len(got["blob"].(string)))
	}

	deep := otellog.String("leaf", "v")
	for i := 0; i < 10; i++ {
		deep = otellog.Map("n", deep)
	}
	record = emitWith(h, otellog.KeyValue{Key: "deep", Value: deep.Value})
	if strings.Contains(record["context"].(string), `"leaf"`) {
		t.Fatalf("deep nesting must be cut: %s", record["context"])
	}
}

type ctxKey struct{}

func TestRecords_NeverCopyContextValues(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)
	ctx := context.WithValue(context.Background(), ctxKey{}, "ctx-secret-value")

	h.request(ctx, 500, func(c context.Context, _ trace.Span) {
		h.log(c, otellog.SeverityError, "failed")
		ReportError(c, errTest("boom"))
	})

	for _, record := range h.sink.all() {
		raw, _ := json.Marshal(record)
		if strings.Contains(string(raw), "ctx-secret-value") {
			t.Fatalf("%s record carries a context value: %s", record.Type(), raw)
		}
	}
}
