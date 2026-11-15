package goravel

import (
	"context"
	"testing"

	contractshttp "github.com/goravel/framework/contracts/http"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// serveWithRouteMiddleware runs the global middleware and, inside it, a route
// middleware, the way Goravel chains them.
func serveWithRouteMiddleware(t *testing.T, routeMiddleware func(m httpMocks)) {
	t.Helper()
	ctx, span := rootSpan(context.Background())
	var m httpMocks
	m = newHTTPMocks(ctx, t, "GET", "", 200, func() { routeMiddleware(m) })
	Middleware(appWith(t, enabledClient(nil), "", nil)).Handle(m.ctx)
	span.End()
}

func TestSample_RouteMiddlewareSetsTheRateOfTheRequest(t *testing.T) {
	out := setupWith(t, func(c *nightwatch.Config) { c.RequestSampleRate = 0 })

	serveWithRouteMiddleware(t, func(m httpMocks) {
		m.req.EXPECT().Next().Unset()
		m.req.EXPECT().Next().Return()
		SampleAlways().Handle(m.ctx)
	})

	if len(out.ofType("request")) != 1 {
		t.Fatal("SampleAlways must keep the request although the global rate is 0")
	}
}

func TestSample_NeverAndRateZeroDropTheRequest(t *testing.T) {
	for name, mw := range map[string]contractshttp.Middleware{"never": SampleNever(), "rate 0": Sample(0)} {
		out := setup(t)

		serveWithRouteMiddleware(t, func(m httpMocks) {
			m.req.EXPECT().Next().Unset()
			m.req.EXPECT().Next().Return()
			mw.Handle(m.ctx)
		})

		if got := out.ofType("request"); len(got) != 0 {
			t.Fatalf("%s: requests = %v", name, got)
		}
	}
}
