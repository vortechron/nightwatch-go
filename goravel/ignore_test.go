package goravel

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/trace"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"

	mockshttp "github.com/goravel/framework/mocks/http"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// newHTTPMocks serves the path /tasks/7.
func TestMiddleware_IgnoredPathsLeaveNoRecordAndNoCounters(t *testing.T) {
	tests := []struct {
		name    string
		env     []string // NIGHTWATCH_IGNORE_PATHS
		option  []string // WithIgnorePaths
		ignored bool
	}{
		{"from the option", nil, []string{"/tasks/*"}, true},
		{"from the environment", []string{"/tasks/*"}, nil, true},
		{"both lists are used", []string{"/other/*"}, []string{"/tasks/7"}, true},
		{"no match", []string{"/build/*"}, []string{"/favicon.ico"}, false},
		{"nothing configured", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := setup(t)
			ctx, span := rootSpan(context.Background())
			var activeInside bool
			m := newHTTPMocks(ctx, t, "GET", "", 200, func() {})
			m.req.EXPECT().Next().Unset()
			m.req.EXPECT().Next().Run(func() {
				activeInside = nightwatch.Active(m.ctx.Context())
				// work inside the request: it must vanish together with it
				nightwatch.ReportError(m.ctx.Context(), errors.New("boom"))
			})

			client := enabledClient(func(c *nightwatch.Config) { c.IgnorePaths = tt.env })
			var options []Option
			if tt.option != nil {
				options = append(options, WithIgnorePaths(tt.option...))
			}
			Middleware(appWith(t, client, "", errors.New("guest")), options...).Handle(m.ctx)
			span.End()

			recorded := len(out.ofType("request")) == 1
			if recorded == tt.ignored {
				t.Fatalf("ignored=%v but request recorded=%v", tt.ignored, recorded)
			}
			if tt.ignored && (len(out.ofType("exception")) != 0 || activeInside) {
				t.Fatalf("an ignored request must not be an active execution and must leave nothing: %v active=%v", out.ofType("exception"), activeInside)
			}
			if !tt.ignored && !activeInside {
				t.Fatal("a normal request is active")
			}
		})
	}
}

func TestMiddleware_IgnoredRequestStillRunsTheHandlers(t *testing.T) {
	setup(t)
	ran := false
	m := mockshttp.NewContext(t)
	req := mockshttp.NewContextRequest(t)
	m.EXPECT().Request().Return(req)
	m.EXPECT().Context().Return(context.Background()).Maybe()
	req.EXPECT().Origin().Return(httptest.NewRequestWithContext(context.Background(), "GET", "/build/app.js?v=1", nil))
	req.EXPECT().Next().Run(func() { ran = true })

	client := enabledClient(func(c *nightwatch.Config) { c.IgnorePaths = []string{"/build/**"} })
	Middleware(appWith(t, client, "", nil)).Handle(m)
	if !ran {
		t.Fatal("the handler chain must still run")
	}
}

// requestFor builds a request the way a client sends it: the target is the raw
// request line, so %3F, %23 and %2e stay encoded there and are decoded in
// URL.Path, which is what the router routes on.
func contextFor(t *testing.T, target string) *mockshttp.Context {
	t.Helper()
	ctx := mockshttp.NewContext(t)
	req := mockshttp.NewContextRequest(t)
	ctx.EXPECT().Request().Return(req).Maybe()
	req.EXPECT().Origin().Return(httptest.NewRequestWithContext(context.Background(), "GET", target, nil)).Maybe()
	req.EXPECT().Path().Return("must not be used while Origin() has a URL").Maybe()

	return ctx
}

func TestIgnoreFilter_DecidesBeforeAnySpanIsStarted(t *testing.T) {
	enabled := enabledClient(func(c *nightwatch.Config) { c.IgnorePaths = []string{"/build/**"} })
	filter := IgnoreFilter(func() contractsfoundation.Application { return appWith(t, enabled, "", nil) }, WithIgnorePaths("/favicon.ico"))

	for target, want := range map[string]bool{
		"/build/app.js":     false,
		"/build/app.js?v=2": false, // the query string is not part of the path
		"/build/a/b/c.css":  false,
		"/favicon.ico":      false,
		"/favicon.ico?v=1":  false,
		"/api/tasks/7":      true,
		"/":                 true,
		"/build":            true,
		"/favicon.ico/":     true,
	} {
		if got := filter(contextFor(t, target)); got != want {
			t.Errorf("trace(%q) = %v, want %v", target, got, want)
		}
	}

	// A disabled client has no opinion: telemetry keeps its own behaviour.
	off := IgnoreFilter(func() contractsfoundation.Application {
		return appWith(t, nightwatch.New(nightwatch.Config{IgnorePaths: []string{"/build/**"}}), "", nil)
	})
	if !off(contextFor(t, "/build/app.js")) {
		t.Fatal("disabled client: the filter must let everything through")
	}
}

// The tricks: an encoded ? or # makes the decoded path contain that character,
// which is part of the path and not a query. Dot segments and doubled slashes
// are never ignored. The middleware and the filter must agree on all of them.
func TestIgnore_CraftedPathsAreNotHidden(t *testing.T) {
	client := enabledClient(func(c *nightwatch.Config) { c.IgnorePaths = []string{"/favicon.ico", "/build/**"} })
	filter := IgnoreFilter(func() contractsfoundation.Application { return appWith(t, client, "", nil) })

	tests := []struct {
		target  string
		ignored bool
	}{
		{"/favicon.ico", true},
		{"/favicon.ico?x=1", true},
		{"/favicon.ico%3Fanything", false}, // decodes to /favicon.ico?anything
		{"/favicon.ico%3f", false},
		{"/favicon.ico%23frag", false}, // decodes to /favicon.ico#frag
		{"/build/app.js", true},
		{"/build/%2e%2e/admin", false}, // decodes to /build/../admin
		{"/build/%2E%2E/admin", false},
		{"/build/a/../../admin", false},
		{"/build//x", false},
		{"//build/x", false},
		{"/build/./x", false},
		{"/build/x%3F/../admin", false},
		{"/build/app%2ejs", true}, // decodes to /build/app.js: a normal asset
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			if got := !filter(contextFor(t, tt.target)); got != tt.ignored {
				t.Fatalf("filter ignored=%v, want %v", got, tt.ignored)
			}

			out := setup(t)
			span := startRequestSpan(t)
			m := newHTTPMocks(span.ctx, t, "GET", "", 200, func() {})
			m.req.EXPECT().Origin().Unset()
			origin := httptest.NewRequestWithContext(context.Background(), "GET", tt.target, nil)
			m.req.EXPECT().Origin().Return(origin).Maybe()
			Middleware(appWith(t, client, "", errors.New("guest"))).Handle(m.ctx)
			span.span.End()

			if recorded := len(out.ofType("request")) == 1; recorded == tt.ignored {
				t.Fatalf("middleware: ignored=%v but recorded=%v (the filter said ignored=%v)", tt.ignored, recorded, !filter(contextFor(t, tt.target)))
			}
		})
	}
}

type requestSpan struct {
	ctx  context.Context
	span trace.Span
}

func startRequestSpan(t *testing.T) requestSpan {
	t.Helper()
	ctx, span := rootSpan(context.Background())

	return requestSpan{ctx: ctx, span: span}
}
