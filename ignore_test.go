package nightwatch

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// PathIgnored gets the path the router routes on (the request's decoded
// URL.Path; the query string is never part of it). It cuts nothing, cleans
// nothing and matches the string it is given.
func TestPathIgnored(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		path     string
		want     bool
	}{
		// exact
		{"exact", []string{"/favicon.ico"}, "/favicon.ico", true},
		{"exact is not a prefix", []string{"/favicon.ico"}, "/favicon.icon", false},
		{"exact is not a suffix", []string{"/favicon.ico"}, "/x/favicon.ico", false},
		{"exact does not take a trailing slash", []string{"/favicon.ico"}, "/favicon.ico/", false},
		{"case matters", []string{"/favicon.ico"}, "/Favicon.ico", false},
		{"root", []string{"/"}, "/", true},
		{"root is not everything", []string{"/"}, "/x", false},

		// * stays inside one segment
		{"star matches a file in the directory", []string{"/build/*"}, "/build/app.js", true},
		{"star does not cross a slash", []string{"/build/*"}, "/build/assets/app.js", false},
		{"star matches the empty last segment", []string{"/build/*"}, "/build/", true},
		{"star needs the slash", []string{"/build/*"}, "/build", false},
		{"star inside a segment", []string{"/*.png"}, "/logo.png", true},
		{"star inside a segment, deeper path", []string{"/*.png"}, "/img/logo.png", false},
		{"star in the middle", []string{"/assets/*/app.js"}, "/assets/v2/app.js", true},
		{"star in the middle does not cross a slash", []string{"/assets/*/app.js"}, "/assets/v2/x/app.js", false},
		{"star can be empty", []string{"/a*b"}, "/ab", true},
		{"star prefix of a name", []string{"/build/app.*"}, "/build/app.js", true},

		// ** goes any depth
		{"double star, file", []string{"/build/**"}, "/build/app.js", true},
		{"double star, nested", []string{"/build/**"}, "/build/assets/css/app.css", true},
		{"double star, empty tail", []string{"/build/**"}, "/build/", true},
		{"double star needs the slash", []string{"/build/**"}, "/build", false},
		{"double star is anchored", []string{"/build/**"}, "/api/build/x", false},
		{"double star is not a longer name", []string{"/build/**"}, "/builder/x", false},
		{"double star suffix", []string{"**.png"}, "/img/deep/logo.png", true},
		{"double star suffix, wrong extension", []string{"**.png"}, "/img/logo.pngx", false},
		{"double star in the middle", []string{"/api/**/health"}, "/api/v1/internal/health", true},
		{"double star in the middle, wrong tail", []string{"/api/**/health"}, "/api/v1/internal/status", false},

		// the default style the skeleton will use
		{"skeleton build", []string{"/build/**", "/favicon.ico"}, "/build/assets/app-4f2a.js", true},
		{"skeleton favicon", []string{"/build/**", "/favicon.ico"}, "/favicon.ico", true},
		{"skeleton real route", []string{"/build/**", "/favicon.ico"}, "/api/tasks/7", false},

		// a literal ? or # in the path is part of the path, nothing is cut
		{"percent-3F in the path is not a query separator", []string{"/favicon.ico"}, "/favicon.ico?anything", false},
		{"percent-23 in the path is not a fragment separator", []string{"/favicon.ico"}, "/favicon.ico#frag", false},
		{"question mark hidden before a deeper path", []string{"/build/*"}, "/build/x?/../../admin", false},
		{"a question mark is just a character for **", []string{"/build/**"}, "/build/app.js?v=3", true},
		{"a question mark is just a character for *", []string{"/search?*"}, "/search?q=1", true},
		{"hash is just a character", []string{"/docs/*"}, "/docs/a#b", true},
		{"cannot hide a route behind an ignored name", []string{"/favicon.ico"}, "/favicon.ico%3Fx", false},

		// never ignore what looks like traversal or a doubled slash
		{"dot dot", []string{"/build/**"}, "/build/../secret", false},
		{"dot dot deeper", []string{"/build/**"}, "/build/a/../../admin", false},
		{"dot dot at the end", []string{"/build/**"}, "/build/a/..", false},
		{"single dot", []string{"/build/**"}, "/build/./x", false},
		{"double slash inside", []string{"/build/**"}, "/build//x", false},
		{"double slash at the start", []string{"/build/**"}, "//build/x", false},
		{"double slash at the end", []string{"/build/**"}, "/build/a//", false},
		{"double slash with exact pattern", []string{"//favicon.ico"}, "//favicon.ico", false},
		{"dots inside a name are fine", []string{"/build/**"}, "/build/a..b/..hidden/x...js", true},
		{"a trailing slash alone is fine", []string{"/build/**"}, "/build/a/", true},

		// no patterns, blank patterns, empty path
		{"no patterns ignores nothing", nil, "/build/app.js", false},
		{"blank patterns are skipped", []string{"", "  "}, "/anything", false},
		{"empty path", []string{"/build/**"}, "", false},
		{"several patterns, second matches", []string{"/build/*", "/robots.txt"}, "/robots.txt", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PathIgnored(tt.patterns, tt.path); got != tt.want {
				t.Fatalf("PathIgnored(%v, %q) = %v, want %v", tt.patterns, tt.path, got, tt.want)
			}
		})
	}
}

func TestPathIgnored_PathologicalPatternsStayFast(t *testing.T) {
	long := "/" + strings.Repeat("a/", 2000) + "z"
	if PathIgnored([]string{"/**a**a**a**a**a**a**a**b"}, long) {
		t.Fatal("must not match")
	}
	if !PathIgnored([]string{"/**/z"}, long) {
		t.Fatal("must match")
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
