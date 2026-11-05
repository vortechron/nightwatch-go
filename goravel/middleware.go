package goravel

import (
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	contractshttp "github.com/goravel/framework/contracts/http"

	nightwatch "github.com/vortechron/nightwatch-go"
)

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
	redact  []string
	resolve UserResolver
}

// Middleware enriches the span that Goravel's telemetry middleware opened
// with what Nightwatch needs and OpenTelemetry's HTTP convention leaves out:
// the path and query, redacted headers, body sizes, the signed-in user and
// panics with a stack trace. Register it after the telemetry middleware.
func Middleware(app contractsfoundation.Application, options ...Option) contractshttp.Middleware {
	return LazyMiddleware(func() contractsfoundation.Application { return app }, options...)
}

// LazyMiddleware is Middleware for use in WithMiddleware, which runs before
// the application exists. getApp is called when a request needs the user, for
// example `nwgoravel.LazyMiddleware(facades.App)`.
func LazyMiddleware(getApp func() contractsfoundation.Application, options ...Option) contractshttp.Middleware {
	m := &middleware{app: getApp, redact: Client().Config().RedactHeaders}
	for _, option := range options {
		option(m)
	}

	return m
}

func (m *middleware) Signature() string { return "nightwatch" }

func (m *middleware) Handle(ctx contractshttp.Context) {
	span := trace.SpanFromContext(ctx.Context())
	req := ctx.Request()

	span.SetAttributes(requestAttributes(req, m.redact)...)

	defer func() {
		recovered := recover()
		if recovered != nil {
			nightwatch.RecordExceptionOnSpan(span, fmt.Errorf("panic: %v", recovered), false)
		}

		m.finish(ctx, span)

		if recovered != nil {
			panic(recovered)
		}
	}()

	req.Next()
}

func requestAttributes(req contractshttp.ContextRequest, redact []string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("url.path", req.Path()),
		attribute.String("nightwatch.request.headers", nightwatch.HeadersToJSON(req.Headers(), redact)),
	}

	if origin := req.Origin(); origin != nil {
		attrs = append(attrs, originAttributes(origin)...)
	}

	return attrs
}

func originAttributes(origin *http.Request) []attribute.KeyValue {
	attrs := []attribute.KeyValue{}
	if origin.URL != nil && origin.URL.RawQuery != "" {
		attrs = append(attrs, attribute.String("url.query", origin.URL.RawQuery))
	}

	if origin.ContentLength > 0 {
		attrs = append(attrs, attribute.Int64("http.request.body.size", origin.ContentLength))
	}

	return attrs
}

// finish adds what is only known once the handlers have run.
func (m *middleware) finish(ctx contractshttp.Context, span trace.Span) {
	if origin := ctx.Response().Origin(); origin != nil {
		span.SetAttributes(
			attribute.Int("http.response.body.size", origin.Size()),
			attribute.Int("http.response.status_code", origin.Status()),
		)
	}

	if id, ok := m.userID(ctx); ok {
		user := nightwatch.User{ID: id}
		if m.resolve != nil {
			if resolved, found := m.resolve(ctx, id); found {
				user = resolved
			}
		}

		nightwatch.SetUser(ctx.Context(), user)
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
