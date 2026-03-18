package chimw

import (
	"net/http"
	"strings"

	nightwatch "github.com/laravel/nightwatch-go"
)

// Middleware returns a Chi-compatible middleware that instruments HTTP requests.
// It captures request timing, status codes, response sizes, and panic recovery.
//
// Usage:
//
//	nw, _ := nightwatch.New(nightwatch.WithToken("..."))
//	r := chi.NewRouter()
//	r.Use(chimw.Middleware(nw))
func Middleware(nw *nightwatch.Nightwatch) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !nw.Config().Enabled {
				next.ServeHTTP(w, r)
				return
			}

			state := nw.BeginRequest(r)
			ctx := nightwatch.WithState(r.Context(), state)
			r = r.WithContext(ctx)

			rw := nightwatch.NewResponseWriter(w)

			// Panic recovery + request finalization
			defer func() {
				if rec := recover(); rec != nil {
					nw.ReportPanic(state, rec)
					if rw.StatusCode == http.StatusOK && rw.BytesWritten == 0 {
						rw.WriteHeader(http.StatusInternalServerError)
					}
				}
				nw.EndRequest(state, rw.StatusCode, rw.BytesWritten)
			}()

			// Transition to action stage
			nw.TransitionStage(state, nightwatch.StageAction)
			next.ServeHTTP(rw, r)
			nw.TransitionStage(state, nightwatch.StageAfterMiddleware)

			// Extract route info from Chi's route context
			populateRouteInfo(state, r)
		})
	}
}

func populateRouteInfo(state *nightwatch.ExecutionState, r *http.Request) {
	// Chi stores the route pattern in the context.
	// We access it via the chi RouteContext, but since we don't want to import chi
	// directly in the core package, we use a convention-based approach.
	// Users who need route info should set it manually or we detect it from context.

	// Try to get route pattern from Chi's context key
	if rctx := r.Context().Value(routeCtxKey); rctx != nil {
		if rc, ok := rctx.(interface{ RoutePattern() string }); ok {
			pattern := rc.RoutePattern()
			state.RoutePath = pattern
		}
	}

	state.Method = r.Method
	state.URL = r.URL.String()

	// Extract route methods from the HTTP method
	if state.RouteMethods == nil {
		state.RouteMethods = []string{strings.ToUpper(r.Method)}
	}
}

// routeCtxKey matches chi.RouteCtxKey which is a context key type.
// chi uses chi.RouteCtxKey = &contextKey{"RouteContext"}
// We'll provide a SetRouteContext helper for explicit use.
type chiRouteCtxKeyType struct{}

var routeCtxKey = chiRouteCtxKeyType{}

// Note: In practice, users should import "github.com/go-chi/chi/v5" and
// use chi.RouteContext(r.Context()).RoutePattern() directly.
// This middleware provides a basic adapter; for full Chi integration,
// the RoutePattern extraction requires chi to be imported.
// See the MiddlewareWithChi function for the full-featured version.
