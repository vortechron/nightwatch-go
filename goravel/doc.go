// Package goravel wires the nightwatch client into a Goravel v1.18 app.
//
// Add one provider after the framework providers:
//
//	&nwgoravel.ServiceProvider{}
//
// and add the HTTP middleware after Goravel's telemetry middleware:
//
//	WithMiddleware(func(m configuration.Middleware) {
//		m.Append(telemetryhttp.Telemetry(), nwgoravel.LazyMiddleware(facades.App))
//	})
//
// The provider points Goravel telemetry at Nightwatch, starts the sender,
// wraps the cache, scheduler, console commands and queue jobs, and flushes on
// shutdown. APIs that carry no context.Context (mail, queue dispatch) need the
// small wrappers Mail(ctx) and Queue(ctx).
package goravel
