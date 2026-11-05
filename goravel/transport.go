package goravel

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// Transport wraps an HTTP transport so every outgoing request becomes an
// outgoing-request record of the execution in the request's context. Use it on
// a plain *http.Client and pass the context with http.NewRequestWithContext.
// Goravel's own HTTP client is instrumented by the framework when
// enable_telemetry is set for the client in config/http.go.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}

	return &gatedTransport{base: base, traced: otelhttp.NewTransport(base)}
}

// gatedTransport traces only requests whose context belongs to a Nightwatch
// execution. Any other request goes straight to the base transport.
type gatedTransport struct {
	base   http.RoundTripper
	traced http.RoundTripper
}

func (g *gatedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if nightwatch.Active(req.Context()) {
		return g.traced.RoundTrip(req) //nolint:wrapcheck // passthrough
	}

	return g.base.RoundTrip(req) //nolint:wrapcheck // passthrough
}
