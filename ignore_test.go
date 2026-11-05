package nightwatch

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

func TestPathIgnored(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		path     string
		want     bool
	}{
		{"prefix glob, file", []string{"/build/*"}, "/build/app.js", true},
		{"prefix glob, nested", []string{"/build/*"}, "/build/assets/css/app.css", true},
		{"prefix glob, directory itself with slash", []string{"/build/*"}, "/build/", true},
		{"prefix glob needs the slash", []string{"/build/*"}, "/build", false},
		{"prefix glob is anchored at the start", []string{"/build/*"}, "/api/build/x", false},
		{"prefix glob does not match a longer name", []string{"/build/*"}, "/builder/x", false},
		{"query string is ignored", []string{"/build/*"}, "/build/app.js?v=3&token=x", true},
		{"query string cannot make a path match", []string{"/build/*"}, "/api?x=/build/a", false},
		{"fragment is ignored", []string{"/favicon.ico"}, "/favicon.ico#top", true},
		{"exact", []string{"/favicon.ico"}, "/favicon.ico", true},
		{"exact with query", []string{"/favicon.ico"}, "/favicon.ico?x=1", true},
		{"exact is not a prefix", []string{"/favicon.ico"}, "/favicon.icon", false},
		{"exact is not a suffix", []string{"/favicon.ico"}, "/x/favicon.ico", false},
		{"suffix glob", []string{"*.png"}, "/img/logo.png", true},
		{"suffix glob with query", []string{"*.png"}, "/img/logo.png?w=2", true},
		{"suffix glob, longer extension", []string{"*.png"}, "/img/logo.pngx", false},
		{"star inside", []string{"/assets/*/app.js"}, "/assets/v2/app.js", true},
		{"star inside, wrong tail", []string{"/assets/*/app.js"}, "/assets/v2/app.css", false},
		{"several patterns, second matches", []string{"/build/*", "/favicon.ico", "/robots.txt"}, "/robots.txt", true},
		{"several patterns, none match", []string{"/build/*", "/favicon.ico"}, "/api/tasks/7", false},
		{"no patterns ignores nothing", nil, "/build/app.js", false},
		{"blank patterns are skipped", []string{"", "  "}, "/anything", false},
		{"case matters", []string{"/build/*"}, "/BUILD/app.js", false},
		{"root", []string{"/"}, "/", true},
		{"root is not everything", []string{"/"}, "/x", false},
		{"empty path", []string{"/build/*"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PathIgnored(tt.patterns, tt.path); got != tt.want {
				t.Fatalf("PathIgnored(%v, %q) = %v, want %v", tt.patterns, tt.path, got, tt.want)
			}
		})
	}
}

func TestConfig_IgnorePathsFromEnv(t *testing.T) {
	if got := ConfigFromEnv(env(map[string]string{"NIGHTWATCH_TOKEN": "t"}), "h").IgnorePaths; len(got) != 0 {
		t.Fatalf("the library default is empty, got %v", got)
	}
	cfg := ConfigFromEnv(env(map[string]string{"NIGHTWATCH_TOKEN": "t", "NIGHTWATCH_IGNORE_PATHS": "/build/*, /favicon.ico ,,"}), "h")
	if len(cfg.IgnorePaths) != 2 || cfg.IgnorePaths[0] != "/build/*" || cfg.IgnorePaths[1] != "/favicon.ico" {
		t.Fatalf("got %v", cfg.IgnorePaths)
	}
}

// A request the middleware marked as ignored leaves no trace in Omni at all:
// no request record, no children, no logs, nothing in the counters.
func TestTranslator_IgnoredExecutionProducesNothing(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)
	h.request(context.Background(), 200, func(ctx context.Context, span trace.Span) {
		span.SetAttributes(attribute.Bool(AttrIgnore, true))
		h.query(ctx, "select 1")
		h.log(ctx, otellog.SeverityError, "noise")
		ReportError(ctx, errTest("boom"))
	})
	if records := h.sink.all(); len(records) != 0 {
		t.Fatalf("an ignored request must send nothing, got %v", records)
	}

	h.request(context.Background(), 200, nil)
	if len(h.sink.ofType("request")) != 1 {
		t.Fatal("other requests are still recorded")
	}
}
