package goravel

import (
	"context"
	"testing"

	contractshttp "github.com/goravel/framework/contracts/http"
)

func TestMiddleware_RecordsTheRouteNameAndTheHandlerAsTheAction(t *testing.T) {
	out := setup(t)
	ctx, span := rootSpan(context.Background())
	m := newHTTPMocks(ctx, t, "GET", "", 200, func() {})
	m.req.EXPECT().Info().Unset()
	m.req.EXPECT().Info().Return(contractshttp.Info{
		Name: "tasks.show", Handler: "example/app/http/controllers.(*TaskController).Show-fm", Path: "/tasks/{id}",
	})

	Middleware(appWith(t, enabledClient(nil), "", nil)).Handle(m.ctx)
	span.End()

	request := out.ofType("request")[0]
	if request["route_name"] != "tasks.show" || request["route_action"] != "example/app/http/controllers.(*TaskController).Show" {
		t.Fatalf("route name %v, action %v", request["route_name"], request["route_action"])
	}
}

func TestMiddleware_AnUnmatchedRouteHasNoNameOrAction(t *testing.T) {
	out := setup(t)
	ctx, span := rootSpan(context.Background())
	m := newHTTPMocks(ctx, t, "GET", "", 404, func() {})

	Middleware(appWith(t, enabledClient(nil), "", nil)).Handle(m.ctx)
	span.End()

	if request := out.ofType("request")[0]; request["route_name"] != "" || request["route_action"] != "" {
		t.Fatalf("route name %v, action %v", request["route_name"], request["route_action"])
	}
}
