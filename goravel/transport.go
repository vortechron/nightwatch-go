package goravel

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
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

	return otelhttp.NewTransport(base)
}
