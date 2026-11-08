package goravel

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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

func TestIgnoreFilter_LetsEveryRequestOpenASpan(t *testing.T) {
	enabled := enabledClient(func(c *nightwatch.Config) { c.IgnorePaths = []string{"/build/**"} })
	filter := IgnoreFilter(func() contractsfoundation.Application { return appWith(t, enabled, "", nil) }, WithIgnorePaths("/favicon.ico"))

	for _, target := range []string{"/build/app.js", "/favicon.ico", "/api/tasks/7", "/"} {
		if !filter(contextFor(t, target)) {
			t.Errorf("trace(%q) = false: an ignored request needs its span so the middleware can drop what runs inside it", target)
		}
	}
}

// The tricks: an encoded ? or # makes the decoded path contain that character,
// which is part of the path and not a query. Dot segments and doubled slashes
// are never ignored. The middleware and the filter must agree on all of them.
func TestIgnore_CraftedPathsAreNotHidden(t *testing.T) {
	client := enabledClient(func(c *nightwatch.Config) { c.IgnorePaths = []string{"/favicon.ico", "/build/**"} })
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
			out := setup(t)
			span := startRequestSpan(t)
			m := newHTTPMocks(span.ctx, t, "GET", "", 200, func() {})
			m.req.EXPECT().Origin().Unset()
			origin := httptest.NewRequestWithContext(context.Background(), "GET", tt.target, nil)
			m.req.EXPECT().Origin().Return(origin).Maybe()
			Middleware(appWith(t, client, "", errors.New("guest"))).Handle(m.ctx)
			span.span.End()

			if recorded := len(out.ofType("request")) == 1; recorded == tt.ignored {
				t.Fatalf("middleware: ignored=%v but recorded=%v", tt.ignored, recorded)
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

// A query that runs inside an ignored request must vanish with it. With the
// telemetry middleware filtered out, the request has no span, so the query's
// span became a root of its own: Omni got a "query" record with an empty
// execution for every ignored request. The filter must therefore let the
// telemetry middleware open the request span; the nightwatch middleware then
// marks it ignored and the translator drops everything under it.
func TestIgnoredRequest_LeavesNoQueryRecordNotEvenAnOrphan(t *testing.T) {
	out, translator := setupTranslator(t)
	client := enabledClient(func(c *nightwatch.Config) { c.IgnorePaths = []string{"/api/omni/**"} })
	app := appWith(t, client, "", nil)
	filter := IgnoreFilter(func() contractsfoundation.Application { return app })
	httpCtx := contextFor(t, "/api/omni/ingest")

	// What Goravel's telemetry middleware does: no span when the filter says no.
	ctx := context.Background()
	var root trace.Span
	if filter(httpCtx) {
		ctx, root = rootSpan(ctx)
	}
	// What the application's database layer does inside the handler.
	_, query := otel.Tracer("db").Start(ctx, "SELECT apps", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("db.system.name", "sqlite"),
		attribute.String("db.query.text", "select * from apps where api_token = ?"),
	))
	query.End()
	if root != nil {
		Middleware(app).Handle(ignoredRequest(ctx, t, "/api/omni/ingest"))
		root.End()
	}

	time.Sleep(5 * time.Millisecond)
	translator.Sweep()
	translator.Drain()

	if got := len(out.ofType("query")) + len(out.ofType("request")); got != 0 {
		t.Fatalf("an ignored request left %d records: queries %v requests %v", got, out.ofType("query"), out.ofType("request"))
	}
}

// ignoredRequest is a request context for path whose handler chain does nothing.
func ignoredRequest(ctx context.Context, t *testing.T, path string) *mockshttp.Context {
	t.Helper()
	m := mockshttp.NewContext(t)
	req := mockshttp.NewContextRequest(t)
	m.EXPECT().Request().Return(req).Maybe()
	m.EXPECT().Context().Return(ctx).Maybe()
	m.EXPECT().WithContext(ctx).Maybe()
	req.EXPECT().Origin().Return(httptest.NewRequestWithContext(ctx, "GET", path, nil)).Maybe()
	req.EXPECT().Next().Maybe()

	return m
}
