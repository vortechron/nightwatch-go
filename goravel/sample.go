package goravel

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// Sample is a route middleware that sets the sample rate of the requests it
// handles (Laravel's Laravel\Nightwatch\Http\Middleware\Sample::rate). Put it
// on a route, a route group, or the fallback route to sample bot traffic:
//
//	facades.Route().Middleware(nwgoravel.Sample(0.5)).Get("/users", users.Index)
//
// It needs the Nightwatch middleware to run first (it is global), and it
// changes nothing when the client is disabled.
func Sample(rate float64) contractshttp.Middleware {
	return &sampleMiddleware{rate: rate}
}

// SampleAlways keeps every request of the route: Sample(1).
func SampleAlways() contractshttp.Middleware { return Sample(1) }

// SampleNever drops every request of the route: Sample(0). An unhandled
// exception can still bring a request back with the exception sample rate.
func SampleNever() contractshttp.Middleware { return Sample(0) }

type sampleMiddleware struct{ rate float64 }

func (m *sampleMiddleware) Signature() string { return "nightwatch.sample" }

func (m *sampleMiddleware) Handle(ctx contractshttp.Context) {
	nightwatch.Sample(ctx.Context(), m.rate)
	ctx.Request().Next()
}
