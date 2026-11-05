package goravel

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	contractshttp "github.com/goravel/framework/contracts/http"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// maxPayloadBytes is how much of a request body is read for payload capture.
const maxPayloadBytes = 64 * 1024

// UserResolver turns the signed-in user id into a full User (name, username).
// Return false to keep just the id.
type UserResolver func(ctx contractshttp.Context, id string) (nightwatch.User, bool)

// Option changes the middleware.
type Option func(*middleware)

// WithUserResolver adds names to the user record.
func WithUserResolver(resolve UserResolver) Option {
	return func(m *middleware) { m.resolve = resolve }
}

type middleware struct {
	app     func() contractsfoundation.Application
	resolve UserResolver
}

// Middleware enriches the span that Goravel's telemetry middleware opened
// with what Nightwatch needs and OpenTelemetry's HTTP convention leaves out:
// the path and query, redacted headers, body sizes, the signed-in user, the
// redacted body of failed requests (only with NIGHTWATCH_CAPTURE_REQUEST_PAYLOAD)
// and panics with a stack trace. Register it after the telemetry middleware.
func Middleware(app contractsfoundation.Application, options ...Option) contractshttp.Middleware {
	return LazyMiddleware(func() contractsfoundation.Application { return app }, options...)
}

// LazyMiddleware is Middleware for use in WithMiddleware, which runs before
// the application exists. getApp is called per request, for example
// `nwgoravel.LazyMiddleware(facades.App)`.
func LazyMiddleware(getApp func() contractsfoundation.Application, options ...Option) contractshttp.Middleware {
	m := &middleware{app: getApp}
	for _, option := range options {
		option(m)
	}

	return m
}

func (m *middleware) Signature() string { return "nightwatch" }

func (m *middleware) client() *nightwatch.Client {
	if app := m.app(); app != nil {
		return ClientFrom(app)
	}

	return nil
}

func (m *middleware) Handle(ctx contractshttp.Context) {
	client := m.client()
	if client == nil || !client.Enabled() {
		ctx.Request().Next()

		return
	}

	ctx.WithContext(nightwatch.MarkActive(ctx.Context()))

	cfg := client.Config()
	span := trace.SpanFromContext(ctx.Context())
	req := ctx.Request()

	span.SetAttributes(requestAttributes(req, cfg.RedactHeaders, cfg.RedactQuery)...)
	body := captureBody(req, cfg)

	// Sign-in usually happens in an earlier middleware: carry the user into the
	// context so jobs dispatched by the handler report the same user.
	m.identify(ctx)

	defer func() {
		recovered := recover()
		if recovered != nil {
			nightwatch.RecordExceptionOnSpan(span, panicError(recovered), false)
		}

		m.finish(ctx, span, cfg, body)

		if recovered != nil {
			panic(recovered)
		}
	}()

	req.Next()
}

func requestAttributes(req contractshttp.ContextRequest, redactHeaders, redactQuery []string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("url.path", req.Path()),
		attribute.String("nightwatch.request.headers", nightwatch.HeadersToJSON(req.Headers(), redactHeaders)),
	}

	if origin := req.Origin(); origin != nil {
		attrs = append(attrs, originAttributes(origin, redactQuery)...)
	}

	return attrs
}

// originAttributes reads the query and body size. The query is redacted here,
// at capture time, so a token in a link never sits in a span attribute.
func originAttributes(origin *http.Request, redactQuery []string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{}

	if origin.URL != nil && origin.URL.RawQuery != "" {
		if query := nightwatch.RedactQuery(origin.URL.RawQuery, redactQuery); query != "" {
			attrs = append(attrs, attribute.String("url.query", query))
		}
	}

	if origin.ContentLength > 0 {
		attrs = append(attrs, attribute.Int64("http.request.body.size", origin.ContentLength))
	}

	return attrs
}

// captureBody reads a JSON request body (at most 64 KB) when payload capture
// is on, and puts it back so the handlers still see it. It returns the body
// already redacted, so a secret never reaches a span attribute; "" when off.
func captureBody(req contractshttp.ContextRequest, cfg nightwatch.Config) string {
	origin := req.Origin()
	if !cfg.CaptureRequestPayload || origin == nil || origin.Body == nil ||
		!strings.Contains(origin.Header.Get("Content-Type"), "json") {
		return ""
	}

	raw, err := io.ReadAll(io.LimitReader(origin.Body, maxPayloadBytes+1))
	rest := origin.Body
	origin.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(raw), rest), rest}

	if err != nil || len(raw) > maxPayloadBytes {
		return ""
	}

	return nightwatch.RedactPayloadJSON(string(raw), cfg.RedactPayloadFields)
}

// identify stores the signed-in user in the request context and span.
func (m *middleware) identify(ctx contractshttp.Context) {
	id, ok := m.userID(ctx)
	if !ok {
		return
	}

	user := nightwatch.User{ID: id}
	if m.resolve != nil {
		if resolved, found := m.resolve(ctx, id); found {
			user = resolved
		}
	}

	ctx.WithContext(nightwatch.SetUser(ctx.Context(), user))
}

// finish adds what is only known once the handlers have run.
func (m *middleware) finish(ctx contractshttp.Context, span trace.Span, cfg nightwatch.Config, body string) {
	status := 0

	if origin := ctx.Response().Origin(); origin != nil {
		status = origin.Status()
		span.SetAttributes(
			attribute.Int("http.response.body.size", origin.Size()),
			attribute.Int("http.response.status_code", status),
		)
	}

	if cfg.CaptureRequestPayload && body != "" && status == http.StatusInternalServerError {
		span.SetAttributes(attribute.String(nightwatch.AttrPayload, body))
	}

	// The user may have signed in during the request.
	if nightwatch.UserFromContext(ctx.Context()) == "" {
		m.identify(ctx)
	}
}

// userID asks Goravel auth for the signed-in user. Auth panics or errors when
// nobody is signed in, which only means this is a guest.
func (m *middleware) userID(ctx contractshttp.Context) (id string, ok bool) {
	defer func() {
		if recover() != nil {
			id, ok = "", false
		}
	}()

	auth := m.app().MakeAuth(ctx)
	if auth == nil {
		return "", false
	}

	id, err := auth.ID()

	return id, err == nil && id != ""
}
