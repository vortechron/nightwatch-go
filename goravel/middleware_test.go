package goravel

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/goravel/framework/contracts/binding"
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	contractshttp "github.com/goravel/framework/contracts/http"
	mocksauth "github.com/goravel/framework/mocks/auth"
	mockscache "github.com/goravel/framework/mocks/cache"
	mocksconfig "github.com/goravel/framework/mocks/config"
	mocksconsole "github.com/goravel/framework/mocks/console"
	mocksfoundation "github.com/goravel/framework/mocks/foundation"
	mockshttp "github.com/goravel/framework/mocks/http"
	mocksqueue "github.com/goravel/framework/mocks/queue"
	mocksschedule "github.com/goravel/framework/mocks/schedule"

	nightwatch "github.com/vortechron/nightwatch-go"
)

type httpMocks struct {
	ctx  *mockshttp.Context
	req  *mockshttp.ContextRequest
	resp *mockshttp.ContextResponse
	orig *mockshttp.ResponseOrigin
}

func newHTTPMocks(ctx context.Context, t *testing.T, method, body string, status int, next func()) httpMocks {
	t.Helper()
	m := httpMocks{
		ctx:  mockshttp.NewContext(t),
		req:  mockshttp.NewContextRequest(t),
		resp: mockshttp.NewContextResponse(t),
		orig: mockshttp.NewResponseOrigin(t),
	}
	origin := httptest.NewRequestWithContext(ctx, method, "/tasks/7?a=1", strings.NewReader(body))
	origin.Header.Set("Content-Type", "application/json")
	origin.Header.Set("Authorization", "Bearer topsecret")
	if body == "" {
		origin.ContentLength = 5
	}

	current := ctx
	m.ctx.EXPECT().Context().RunAndReturn(func() context.Context { return current }).Maybe()
	m.ctx.EXPECT().WithContext(mock.Anything).Run(func(c context.Context) { current = c }).Maybe()
	m.ctx.EXPECT().Request().Return(m.req).Maybe()
	m.ctx.EXPECT().Response().Return(m.resp).Maybe()
	m.req.EXPECT().Path().Return("/tasks/7").Maybe()
	m.req.EXPECT().Headers().Return(origin.Header).Maybe()
	m.req.EXPECT().Origin().Return(origin).Maybe()
	m.req.EXPECT().Next().Run(func() { next() }).Maybe()
	m.resp.EXPECT().Origin().Return(m.orig).Maybe()
	m.orig.EXPECT().Size().Return(42).Maybe()
	m.orig.EXPECT().Status().Return(status).Maybe()

	return m
}

func appWith(t *testing.T, client *nightwatch.Client, authID string, authErr error) *mocksfoundation.Application {
	t.Helper()
	app := mocksfoundation.NewApplication(t)
	app.EXPECT().Make(BindingClient).Return(client, nil).Maybe()
	a := mocksauth.NewAuth(t)
	a.EXPECT().ID().Return(authID, authErr).Maybe()
	app.EXPECT().MakeAuth(mock.Anything).Return(a).Maybe()

	return app
}

func enabledClient(mutate func(*nightwatch.Config)) *nightwatch.Client {
	cfg := nightwatch.ConfigFromEnv(func(k string) string {
		return map[string]string{"NIGHTWATCH_TOKEN": "t"}[k]
	}, "h")
	if mutate != nil {
		mutate(&cfg)
	}

	return nightwatch.New(cfg)
}

func rootSpan(ctx context.Context) (context.Context, trace.Span) {
	return otel.Tracer("t").Start(ctx, "GET /tasks/:id", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", "GET"), attribute.String("http.route", "/tasks/:id")))
}

func TestMiddleware_EnrichesRequestSpanAndIdentifiesTheUserBeforeTheHandlers(t *testing.T) {
	out := setup(t)
	ctx, span := rootSpan(context.Background())
	var userInHandler string
	var mw contractshttp.Middleware
	m := newHTTPMocks(ctx, t, "GET", "", 200, func() {})
	m.req.EXPECT().Next().Unset()
	m.req.EXPECT().Next().Run(func() { userInHandler = nightwatch.UserFromContext(m.ctx.Context()) })

	resolve := func(_ contractshttp.Context, id string) (nightwatch.User, bool) {
		return nightwatch.User{ID: id, Name: "Ada"}, true
	}
	mw = Middleware(appWith(t, enabledClient(nil), "42", nil), WithUserResolver(resolve))
	if mw.Signature() != "nightwatch" {
		t.Fatal("signature")
	}
	mw.Handle(m.ctx)
	span.End()

	if userInHandler != "42" {
		t.Fatal("jobs dispatched in a handler need the user, so it must be in the context before the handlers run")
	}
	req := out.ofType("request")
	if len(req) != 1 || req[0]["user"] != "42" || req[0]["response_size"] != int64(42) || req[0]["request_size"] != int64(5) {
		t.Fatalf("request: %v", req)
	}
	if h := req[0]["headers"].(string); strings.Contains(h, "topsecret") || !strings.Contains(h, "[redacted]") {
		t.Fatalf("headers: %s", h)
	}
	if len(out.ofType("user")) != 1 {
		t.Fatal("user record")
	}
}

func TestMiddleware_UserWhoSignsInDuringTheRequestIsStillFound(t *testing.T) {
	out := setup(t)
	ctx, span := rootSpan(context.Background())
	m := newHTTPMocks(ctx, t, "GET", "", 200, func() {})

	app := mocksfoundation.NewApplication(t)
	app.EXPECT().Make(BindingClient).Return(enabledClient(nil), nil).Maybe()
	guest := mocksauth.NewAuth(t)
	guest.EXPECT().ID().Return("", errors.New("guest")).Once()
	member := mocksauth.NewAuth(t)
	member.EXPECT().ID().Return("9", nil).Once()
	app.EXPECT().MakeAuth(mock.Anything).Return(guest).Once()
	app.EXPECT().MakeAuth(mock.Anything).Return(member).Once()

	Middleware(app).Handle(m.ctx)
	span.End()

	if out.ofType("request")[0]["user"] != "9" {
		t.Fatalf("request: %v", out.ofType("request")[0])
	}
}

func TestMiddleware_PanicIsRecordedAndRethrown(t *testing.T) {
	out := setup(t)
	ctx, span := rootSpan(context.Background())
	m := newHTTPMocks(ctx, t, "GET", "", 500, func() { panic("kaboom") })

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic must be rethrown")
			}
		}()
		Middleware(appWith(t, enabledClient(nil), "", errors.New("guest"))).Handle(m.ctx)
	}()
	span.End()

	excs := out.ofType("exception")
	if len(excs) != 1 || excs[0]["class"] != "panic" || excs[0]["handled"] != false {
		t.Fatalf("exceptions: %v", excs)
	}
	if out.ofType("request")[0]["user"] != "" {
		t.Fatal("guest must have no user")
	}
}

func TestMiddleware_DisabledClientJustContinues(t *testing.T) {
	ran := false
	m := mockshttp.NewContext(t)
	req := mockshttp.NewContextRequest(t)
	m.EXPECT().Request().Return(req)
	req.EXPECT().Next().Run(func() { ran = true })

	Middleware(appWith(t, nightwatch.New(nightwatch.Config{}), "", nil)).Handle(m)
	if !ran {
		t.Fatal("must call Next")
	}

	m2 := mockshttp.NewContext(t)
	req2 := mockshttp.NewContextRequest(t)
	m2.EXPECT().Request().Return(req2)
	req2.EXPECT().Next().Run(func() { ran = false })
	noClient := mocksfoundation.NewApplication(t)
	noClient.EXPECT().Make(BindingClient).Return(nil, errors.New("no binding"))
	Middleware(noClient).Handle(m2)
	if ran {
		t.Fatal("must call Next without a client")
	}
}

func TestMiddleware_PayloadIsOffByDefault(t *testing.T) {
	out := setup(t)
	ctx, span := rootSpan(context.Background())
	m := newHTTPMocks(ctx, t, "POST", `{"password":"p","a":1}`, 500, func() {})
	Middleware(appWith(t, enabledClient(nil), "", errors.New("guest"))).Handle(m.ctx)
	span.End()
	if out.ofType("request")[0]["payload"] != "" {
		t.Fatal("payload capture must be off by default")
	}
}

func TestMiddleware_PayloadOfAFailedRequestIsRedactedAndTheBodyStillReadable(t *testing.T) {
	out := setupWith(t, func(c *nightwatch.Config) { c.CaptureRequestPayload = true })
	ctx, span := rootSpan(context.Background())
	var downstream string
	body := `{"password":"p","a":1}`
	m := newHTTPMocks(ctx, t, "POST", body, 500, func() {})
	m.req.EXPECT().Next().Unset()
	m.req.EXPECT().Next().Run(func() {
		raw, _ := io.ReadAll(m.req.Origin().Body)
		downstream = string(raw)
	})

	client := enabledClient(func(c *nightwatch.Config) { c.CaptureRequestPayload = true; c.RootGrace = 0 })
	Middleware(appWith(t, client, "", errors.New("guest"))).Handle(m.ctx)
	span.End()

	if downstream != body {
		t.Fatalf("the handlers must still read the whole body, got %q", downstream)
	}
	if got := out.ofType("request")[0]["payload"]; got != `{"a":1,"password":"[redacted]"}` {
		t.Fatalf("payload: %v", got)
	}
}

func TestMiddleware_PayloadOfASuccessfulRequestIsNotKept(t *testing.T) {
	out := setupWith(t, func(c *nightwatch.Config) { c.CaptureRequestPayload = true })
	ctx, span := rootSpan(context.Background())
	m := newHTTPMocks(ctx, t, "POST", `{"a":1}`, 200, func() {})
	client := enabledClient(func(c *nightwatch.Config) { c.CaptureRequestPayload = true })
	Middleware(appWith(t, client, "", errors.New("guest"))).Handle(m.ctx)
	span.End()
	if out.ofType("request")[0]["payload"] != "" {
		t.Fatal("only 500 responses keep the body")
	}
}

func TestCaptureBody_LimitsAndSkips(t *testing.T) {
	cfg := nightwatch.Config{CaptureRequestPayload: true, RedactPayloadFields: []string{"password"}}
	mk := func(contentType, body string) *mockshttp.ContextRequest {
		req := mockshttp.NewContextRequest(t)
		origin := httptest.NewRequestWithContext(context.Background(), "POST", "/", strings.NewReader(body))
		origin.Header.Set("Content-Type", contentType)
		req.EXPECT().Origin().Return(origin)

		return req
	}
	if got := captureBody(mk("application/json", `{"password":"p","a":1}`), cfg); got != `{"a":1,"password":"[redacted]"}` {
		t.Fatalf("got %q", got)
	}
	if captureBody(mk("text/plain", `{"a":1}`), cfg) != "" {
		t.Fatal("only JSON bodies")
	}
	if captureBody(mk("application/json", strings.Repeat("x", maxPayloadBytes+10)), cfg) != "" {
		t.Fatal("oversized bodies are skipped")
	}
	if captureBody(mk("application/json", `not json`), cfg) != "" {
		t.Fatal("invalid JSON never passes through")
	}
	off := cfg
	off.CaptureRequestPayload = false
	if captureBody(mk("application/json", `{}`), off) != "" {
		t.Fatal("off")
	}
}

func TestMiddleware_AuthPanicMeansGuest(t *testing.T) {
	app := mocksfoundation.NewApplication(t)
	app.EXPECT().MakeAuth(mock.Anything).Run(func(...contractshttp.Context) { panic("no guard") }).Return(nil)
	m := &middleware{app: func() contractsfoundation.Application { return app }}
	if id, ok := m.userID(nil); ok || id != "" {
		t.Fatal("panic in auth must read as guest")
	}
	app2 := mocksfoundation.NewApplication(t)
	app2.EXPECT().MakeAuth(mock.Anything).Return(nil)
	if _, ok := (&middleware{app: func() contractsfoundation.Application { return app2 }}).userID(nil); ok {
		t.Fatal("nil auth is a guest")
	}
}

func TestConfigureTelemetry_AddsExportersAndFastProcessors(t *testing.T) {
	cfg := mocksconfig.NewConfig(t)
	cfg.EXPECT().GetString("telemetry.traces.exporter").Return("")
	cfg.EXPECT().GetString("telemetry.logs.exporter").Return(LogExporterName) // set by an earlier call: fine
	cfg.EXPECT().GetDuration("telemetry.traces.processor.interval").Return(5 * time.Second)
	cfg.EXPECT().GetDuration("telemetry.logs.processor.interval").Return(100 * time.Millisecond)
	cfg.EXPECT().Add("telemetry.exporters.nightwatch_traces", mock.Anything).Once()
	cfg.EXPECT().Add("telemetry.exporters.nightwatch_logs", mock.Anything).Once()
	cfg.EXPECT().Add("telemetry.traces.exporter", TraceExporterName).Once()
	cfg.EXPECT().Add("telemetry.logs.exporter", LogExporterName).Once()
	cfg.EXPECT().Add("telemetry.traces.processor.interval", "500ms").Once() // slower than 500 ms: tightened
	// the logs interval is already faster: left alone

	if err := ConfigureTelemetry(cfg, enabledClient(nil)); err != nil {
		t.Fatal(err)
	}
}

func TestConfigureTelemetry_RefusesToReplaceAnExistingExporter(t *testing.T) {
	cfg := mocksconfig.NewConfig(t)
	cfg.EXPECT().GetString("telemetry.traces.exporter").Return("otlptrace")

	err := ConfigureTelemetry(cfg, enabledClient(nil))
	if !errors.Is(err, ErrExporterConflict) || !strings.Contains(err.Error(), "otlptrace") {
		t.Fatalf("want a clear conflict error, got %v", err)
	}

	cfg2 := mocksconfig.NewConfig(t)
	cfg2.EXPECT().GetString("telemetry.traces.exporter").Return("")
	cfg2.EXPECT().GetString("telemetry.logs.exporter").Return("console")
	if err = ConfigureTelemetry(cfg2, enabledClient(nil)); !errors.Is(err, ErrExporterConflict) {
		t.Fatalf("logs exporter conflict: %v", err)
	}
}

func TestProvider_RegisterBootWrapAndShutdown(t *testing.T) {
	client := enabledClient(func(c *nightwatch.Config) { c.FlushInterval = time.Hour })
	app := mocksfoundation.NewApplication(t)
	cfg := mocksconfig.NewConfig(t)

	var factory func(contractsfoundation.Application) (any, error)
	app.EXPECT().Singleton(BindingClient, mock.Anything).Run(func(_ any, cb func(contractsfoundation.Application) (any, error)) { factory = cb }).Once()
	app.EXPECT().Make(BindingClient).Return(client, nil)
	app.EXPECT().MakeConfig().Return(cfg)
	cfg.EXPECT().GetString(mock.Anything).Return("")
	cfg.EXPECT().GetDuration(mock.Anything).Return(time.Duration(0))
	cfg.EXPECT().Add(mock.Anything, mock.Anything)

	artisan := mocksconsole.NewArtisan(t)
	app.EXPECT().MakeCache().Return(mockscache.NewCache(t))
	app.EXPECT().MakeSchedule().Return(mocksschedule.NewSchedule(t))
	app.EXPECT().MakeArtisan().Return(artisan)
	app.EXPECT().MakeQueue().Return(mocksqueue.NewQueue(t))
	app.EXPECT().Instance(binding.Cache, mock.Anything).Once()
	app.EXPECT().Instance(binding.Schedule, mock.Anything).Once()
	app.EXPECT().Instance(binding.Artisan, mock.Anything).Once()
	app.EXPECT().Instance(binding.Queue, mock.Anything).Once()
	app.EXPECT().Fresh(binding.Cache, binding.Schedule, binding.Artisan, binding.Queue).Once()

	var p NightwatchServiceProvider
	p.Register(app)

	created, err := factory(app)
	if err != nil || created.(*nightwatch.Client) == nil {
		t.Fatal("the binding must build a client")
	}
	custom := NightwatchServiceProvider{Config: &nightwatch.Config{Enabled: false}}
	app2 := mocksfoundation.NewApplication(t)
	app2.EXPECT().Singleton(BindingClient, mock.Anything).Run(func(_ any, cb func(contractsfoundation.Application) (any, error)) { factory = cb })
	app2.EXPECT().Make(BindingClient).Return(nil, errors.New("not resolved"))
	custom.Register(app2)
	if built, _ := factory(app2); built.(*nightwatch.Client).Enabled() {
		t.Fatal("the config override must be used")
	}

	p.Boot(app)

	runner := p.Runners(app)[0]
	if !runner.ShouldRun() || runner.Signature() != "nightwatch" || runner.Run() != nil {
		t.Fatal("runner basics")
	}
	if runner.(contractsfoundation.RunnerWithShutdownPriority).ShutdownPriority() != 200 {
		t.Fatal("must shut down after the telemetry runner")
	}
	if err = runner.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestProvider_RegisterPanicsOnAnExporterConflict(t *testing.T) {
	client := enabledClient(nil)
	app := mocksfoundation.NewApplication(t)
	cfg := mocksconfig.NewConfig(t)
	app.EXPECT().Singleton(BindingClient, mock.Anything)
	app.EXPECT().Make(BindingClient).Return(client, nil)
	app.EXPECT().MakeConfig().Return(cfg)
	cfg.EXPECT().GetString("telemetry.traces.exporter").Return("otlptrace")

	defer func() {
		err, _ := recover().(error)
		if !errors.Is(err, ErrExporterConflict) {
			t.Fatalf("want a conflict panic, got %v", err)
		}
	}()
	NightwatchServiceProvider{}.Register(app)
}

func TestProvider_DisabledDoesNothing(t *testing.T) {
	app := mocksfoundation.NewApplication(t)
	app.EXPECT().Singleton(BindingClient, mock.Anything)
	app.EXPECT().Make(BindingClient).Return(nightwatch.New(nightwatch.Config{}), nil)

	var p NightwatchServiceProvider
	p.Register(app)
	p.Boot(app)

	runner := p.Runners(app)[0]
	if runner.ShouldRun() {
		t.Fatal("disabled")
	}

	missing := mocksfoundation.NewApplication(t)
	missing.EXPECT().Make(BindingClient).Return(nil, errors.New("none"))
	p.Boot(missing)
	if p.Runners(missing)[0].ShouldRun() {
		t.Fatal("no client")
	}
}

func TestFlusherAndFactories(t *testing.T) {
	client := enabledClient(nil)
	flusher(client)(context.Background())

	if exp, err := TraceExporter(client)(context.Background()); err != nil || exp == nil {
		t.Fatal("trace exporter")
	}
	if exp, err := LogExporter(client)(context.Background()); err != nil || exp == nil {
		t.Fatal("log exporter")
	}
	if originAttributes(httptest.NewRequestWithContext(context.Background(), "POST", "/a?x=1", strings.NewReader("hello")), nil) == nil {
		t.Fatal("origin attributes")
	}
}

func TestOriginAttributes_QueryIsRedactedAtCaptureTime(t *testing.T) {
	names := []string{"token", "api_key"}
	value := func(target string) string {
		req := httptest.NewRequestWithContext(context.Background(), "GET", target, nil)
		for _, attr := range originAttributes(req, names) {
			if attr.Key == "url.query" {
				return attr.Value.AsString()
			}
		}

		return "<none>"
	}
	tests := map[string]string{
		"/a?page=2":                  "page=2",
		"/a?token=abc&page=2":        "page=2&token=%5Bredacted%5D",
		"/a?API_KEY=abc":             "API_KEY=%5Bredacted%5D",
		"/a?page=2&api_key=x&b=%20c": "api_key=%5Bredacted%5D&b=+c&page=2",
		"/a":                         "<none>",
		"/a?token=%zz":               "<none>",
	}
	for target, want := range tests {
		if got := value(target); got != want {
			t.Errorf("%s: got %q want %q", target, got, want)
		}
	}
}
