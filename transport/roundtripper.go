package transport

import (
	"net/http"
	"strconv"
	"time"

	nightwatch "github.com/laravel/nightwatch-go"
	"github.com/laravel/nightwatch-go/sensor"
)

// Transport wraps an http.RoundTripper to capture outgoing HTTP request telemetry.
// It extracts the ExecutionState from the request's context to associate the
// outgoing request with the parent execution.
//
// Usage:
//
//	nw, _ := nightwatch.New(nightwatch.WithToken("..."))
//	client := &http.Client{
//	    Transport: transport.New(nw),
//	}
type Transport struct {
	Base http.RoundTripper
	nw   *nightwatch.Nightwatch
}

// New creates a new instrumented Transport.
func New(nw *nightwatch.Nightwatch) *Transport {
	return &Transport{
		Base: http.DefaultTransport,
		nw:   nw,
	}
}

// NewWithBase creates a new instrumented Transport wrapping a custom base transport.
func NewWithBase(nw *nightwatch.Nightwatch, base http.RoundTripper) *Transport {
	return &Transport{
		Base: base,
		nw:   nw,
	}
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	state := nightwatch.StateFromContext(req.Context())
	if state == nil || !t.nw.Config().Enabled || t.nw.Config().IgnoreOutgoingRequests {
		return t.base().RoundTrip(req)
	}

	startTime := time.Now()

	resp, err := t.base().RoundTrip(req)

	duration := time.Since(startTime)

	if err != nil {
		// Record the failed request with status 0
		record := sensor.OutgoingRequestRecord(
			state,
			req.Method,
			sanitizeURL(req),
			req.URL.Host,
			0,
			requestSize(req),
			0,
			startTime,
			duration,
		)
		_ = t.nw.Ingester().Write(record)
		return resp, err
	}

	record := sensor.OutgoingRequestRecord(
		state,
		req.Method,
		sanitizeURL(req),
		req.URL.Host,
		resp.StatusCode,
		requestSize(req),
		responseSize(resp),
		startTime,
		duration,
	)
	_ = t.nw.Ingester().Write(record)

	return resp, nil
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

// sanitizeURL returns the URL without userinfo (credentials).
func sanitizeURL(req *http.Request) string {
	u := *req.URL
	u.User = nil
	return u.String()
}

func requestSize(req *http.Request) int {
	if req.ContentLength > 0 {
		return int(req.ContentLength)
	}
	return 0
}

func responseSize(resp *http.Response) int {
	if resp.ContentLength > 0 {
		return int(resp.ContentLength)
	}
	if v := resp.Header.Get("Content-Length"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}
