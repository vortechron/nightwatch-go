package goravel

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/goravel/framework/contracts/binding"
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

func newHTTPMocks(t *testing.T, ctx context.Context, next func()) httpMocks {
	t.Helper()
	m := httpMocks{
		ctx:  mockshttp.NewContext(t),
		req:  mockshttp.NewContextRequest(t),
		resp: mockshttp.NewContextResponse(t),
		orig: mockshttp.NewResponseOrigin(t),
	}
	origin := httptest.NewRequest("GET", "/tasks/7?a=1", nil)
	origin.ContentLength = 5
	m.ctx.EXPECT().Context().Return(ctx).Maybe()
	m.ctx.EXPECT().Request().Return(m.req).Maybe()
	m.ctx.EXPECT().Response().Return(m.resp).Maybe()
	m.req.EXPECT().Path().Return("/tasks/7")
	m.req.EXPECT().Headers().Return(origin.Header)
	m.req.EXPECT().Origin().Return(origin)
	m.req.EXPECT().Next().Run(func() { next() })
	m.resp.EXPECT().Origin().Return(m.orig)
	m.orig.EXPECT().Size().Return(42)
	m.orig.EXPECT().Status().Return(200)

	return m
}

func authReturning(t *testing.T, id string, err error) *mocksfoundation.Application {
	t.Helper()
	app := mocksfoundation.NewApplication(t)
	a := mocksauth.NewAuth(t)
	a.EXPECT().ID().Return(id, err)
	app.EXPECT().MakeAuth(mock.Anything).Return(a)

	return app
}

func TestMiddleware_EnrichesRequestSpan(t *testing.T) {
	out := setup(t)
	SetClient(nightwatch.New(nightwatch.Config{RedactHeaders: []string{"Authorization"}}))
	ctx, span := otel.Tracer("t").Start(context.Background(), "GET /tasks/:id", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrString("http.request.method", "GET"), attrString("http.route", "/tasks/:id")))

	m := newHTTPMocks(t, ctx, func() {})
	app := authReturning(t, "42", nil)
	resolve := func(_ contractshttp.Context, id string) (nightwatch.User, bool) {
		return nightwatch.User{ID: id, Name: "Ada"}, true
	}
	mw := Middleware(app, WithUserResolver(resolve))
	if mw.Signature() != "nightwatch" {
		t.Fatal("signature")
	}
	mw.Handle(m.ctx)
	span.End()

	req := out.ofType("request")
	if len(req) != 1 || req[0]["user"] != "42" || req[0]["response_size"] != int64(42) || req[0]["request_size"] != int64(5) {
		t.Fatalf("request: %v", req)
	}
	if req[0]["url"] != "://" && req[0]["headers"] == "" {
		t.Fatalf("url/headers: %v", req[0])
	}
	if len(out.ofType("user")) != 1 {
		t.Fatal("user record")
	}
}

func TestMiddleware_PanicIsRecordedAndRethrown(t *testing.T) {
	out := setup(t)
	SetClient(nightwatch.New(nightwatch.Config{}))
	ctx, span := otel.Tracer("t").Start(context.Background(), "r", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrString("http.request.method", "GET")))
	m := newHTTPMocks(t, ctx, func() { panic("kaboom") })
	app := authReturning(t, "", errors.New("guest"))

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic must be rethrown")
			}
		}()
		Middleware(app).Handle(m.ctx)
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

func TestMiddleware_AuthPanicMeansGuest(t *testing.T) {
	setup(t)
	SetClient(nightwatch.New(nightwatch.Config{}))
	app := mocksfoundation.NewApplication(t)
	app.EXPECT().MakeAuth(mock.Anything).Run(func(...contractshttp.Context) { panic("no guard") }).Return(nil)
	m := &middleware{app: app}
	if id, ok := m.userID(nil); ok || id != "" {
		t.Fatal("panic in auth must read as guest")
	}
	app2 := mocksfoundation.NewApplication(t)
	app2.EXPECT().MakeAuth(mock.Anything).Return(nil)
	if _, ok := (&middleware{app: app2}).userID(nil); ok {
		t.Fatal("nil auth is a guest")
	}
}

func TestConfigureTelemetry(t *testing.T) {
	cfg := mocksconfig.NewConfig(t)
	cfg.EXPECT().Add("telemetry.exporters.nightwatch_traces", mock.Anything).Once()
	cfg.EXPECT().Add("telemetry.exporters.nightwatch_logs", mock.Anything).Once()
	cfg.EXPECT().Add("telemetry.traces.exporter", TraceExporterName).Once()
	cfg.EXPECT().Add("telemetry.logs.exporter", LogExporterName).Once()
	ConfigureTelemetry(cfg)
}

func TestProvider_RegisterBootWrap(t *testing.T) {
	SetClient(nightwatch.New(nightwatch.Config{Enabled: true, Token: "t", FlushInterval: 1, BatchSize: 1, TraceMaxWait: 1}))
	t.Cleanup(func() { SetClient(nightwatch.New(nightwatch.Config{})) })

	app := mocksfoundation.NewApplication(t)
	cfg := mocksconfig.NewConfig(t)
	cfg.EXPECT().Add(mock.Anything, mock.Anything).Times(4)
	app.EXPECT().MakeConfig().Return(cfg)
	app.EXPECT().MakeCache().Return(mockscache.NewCache(t))
	app.EXPECT().MakeSchedule().Return(mocksschedule.NewSchedule(t))
	app.EXPECT().MakeArtisan().Return(mocksconsole.NewArtisan(t))
	app.EXPECT().MakeQueue().Return(mocksqueue.NewQueue(t))
	app.EXPECT().Instance(binding.Cache, mock.Anything).Once()
	app.EXPECT().Instance(binding.Schedule, mock.Anything).Once()
	app.EXPECT().Instance(binding.Artisan, mock.Anything).Once()
	app.EXPECT().Instance(binding.Queue, mock.Anything).Once()
	app.EXPECT().Fresh(binding.Cache, binding.Schedule, binding.Artisan, binding.Queue).Once()

	var p ServiceProvider
	p.Register(app)
	p.Boot(app)

	ctx, cancel := context.WithTimeout(context.Background(), 2e9)
	defer cancel()
	if err := Client().Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestProvider_DisabledDoesNothing(t *testing.T) {
	SetClient(nightwatch.New(nightwatch.Config{}))
	app := mocksfoundation.NewApplication(t)
	var p ServiceProvider
	p.Register(app)
	p.Boot(app)
}

func attrString(k, v string) attribute.KeyValue { return attribute.String(k, v) }
