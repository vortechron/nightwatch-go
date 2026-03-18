package ginmw

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	nightwatch "github.com/laravel/nightwatch-go"
)

// Middleware returns a Gin middleware that instruments HTTP requests.
//
// Usage:
//
//	nw, _ := nightwatch.New(nightwatch.WithToken("..."), nightwatch.WithBaseURL("https://omni.vortechron.com"))
//	r := gin.Default()
//	r.Use(ginmw.Middleware(nw))
func Middleware(nw *nightwatch.Nightwatch) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !nw.Config().Enabled {
			c.Next()
			return
		}

		state := nw.BeginRequest(c.Request)
		ctx := nightwatch.WithState(c.Request.Context(), state)
		c.Request = c.Request.WithContext(ctx)

		// Set route info EARLY so query/log/exception records created during
		// the handler can reference execution_preview (e.g. "GET /tasks/:id").
		// Gin resolves the route before invoking the handler chain, so
		// c.FullPath() is already available here.
		state.RoutePath = c.FullPath()
		if state.RoutePath == "" {
			state.RoutePath = c.Request.URL.Path
		}
		state.RouteMethods = []string{strings.ToUpper(c.Request.Method)}
		state.URL = c.Request.URL.String()

		// Panic recovery + request finalization
		defer func() {
			if rec := recover(); rec != nil {
				nw.ReportPanic(state, rec)
				if !c.Writer.Written() {
					c.Writer.WriteHeader(http.StatusInternalServerError)
				}
			}

			nw.EndRequest(state, c.Writer.Status(), c.Writer.Size())
		}()

		// Transition to action stage
		nw.TransitionStage(state, nightwatch.StageAction)
		c.Next()
		nw.TransitionStage(state, nightwatch.StageAfterMiddleware)
	}
}
