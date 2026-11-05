package goravel

import (
	"context"
	"errors"
	"testing"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"

	mockshttp "github.com/goravel/framework/mocks/http"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// newHTTPMocks serves the path /tasks/7.
func TestMiddleware_IgnoredPathsLeaveNoRecordAndNoCounters(t *testing.T) {
	tests := []struct {
		name    string
		env     []string // NIGHTWATCH_IGNORE_PATHS
		option  []string // WithIgnorePaths
		ignored bool
	}{
		{"from the option", nil, []string{"/tasks/*"}, true},
		{"from the environment", []string{"/tasks/*"}, nil, true},
		{"both lists are used", []string{"/other/*"}, []string{"/tasks/7"}, true},
		{"no match", []string{"/build/*"}, []string{"/favicon.ico"}, false},
		{"nothing configured", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := setup(t)
			ctx, span := rootSpan(context.Background())
			var activeInside bool
			m := newHTTPMocks(ctx, t, "GET", "", 200, func() {})
			m.req.EXPECT().Next().Unset()
			m.req.EXPECT().Next().Run(func() {
				activeInside = nightwatch.Active(m.ctx.Context())
				// work inside the request: it must vanish together with it
				nightwatch.ReportError(m.ctx.Context(), errors.New("boom"))
			})

			client := enabledClient(func(c *nightwatch.Config) { c.IgnorePaths = tt.env })
			var options []Option
			if tt.option != nil {
				options = append(options, WithIgnorePaths(tt.option...))
			}
			Middleware(appWith(t, client, "", errors.New("guest")), options...).Handle(m.ctx)
			span.End()

			recorded := len(out.ofType("request")) == 1
			if recorded == tt.ignored {
				t.Fatalf("ignored=%v but request recorded=%v", tt.ignored, recorded)
			}
			if tt.ignored && (len(out.ofType("exception")) != 0 || activeInside) {
				t.Fatalf("an ignored request must not be an active execution and must leave nothing: %v active=%v", out.ofType("exception"), activeInside)
			}
			if !tt.ignored && !activeInside {
				t.Fatal("a normal request is active")
			}
		})
	}
}

func TestMiddleware_IgnoredRequestStillRunsTheHandlers(t *testing.T) {
	setup(t)
	ran := false
	m := mockshttp.NewContext(t)
	req := mockshttp.NewContextRequest(t)
	m.EXPECT().Request().Return(req)
	m.EXPECT().Context().Return(context.Background()).Maybe()
	req.EXPECT().Path().Return("/build/app.js?v=1")
	req.EXPECT().Next().Run(func() { ran = true })

	client := enabledClient(func(c *nightwatch.Config) { c.IgnorePaths = []string{"/build/*"} })
	Middleware(appWith(t, client, "", nil)).Handle(m)
	if !ran {
		t.Fatal("the handler chain must still run")
	}
}

func TestIgnoreFilter_DecidesBeforeAnySpanIsStarted(t *testing.T) {
	filterFor := func(c *nightwatch.Client, options ...Option) func(string) bool {
		filter := IgnoreFilter(func() contractsfoundation.Application { return appWith(t, c, "", nil) }, options...)

		return func(path string) bool {
			ctx := mockshttp.NewContext(t)
			req := mockshttp.NewContextRequest(t)
			ctx.EXPECT().Request().Return(req).Maybe()
			req.EXPECT().Path().Return(path).Maybe()

			return filter(ctx)
		}
	}

	enabled := enabledClient(func(c *nightwatch.Config) { c.IgnorePaths = []string{"/build/*"} })
	trace := filterFor(enabled, WithIgnorePaths("/favicon.ico"))
	for path, want := range map[string]bool{
		"/build/app.js": false, "/build/app.js?v=2": false, "/favicon.ico": false,
		"/api/tasks/7": true, "/": true, "/build": true,
	} {
		if got := trace(path); got != want {
			t.Errorf("trace(%q) = %v, want %v", path, got, want)
		}
	}

	// A disabled client has no opinion: telemetry keeps its own behaviour.
	if !filterFor(nightwatch.New(nightwatch.Config{IgnorePaths: []string{"/build/*"}}))("/build/app.js") {
		t.Fatal("disabled client: the filter must let everything through")
	}
}
