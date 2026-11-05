package controllers

import (
	"errors"
	"goravel/app/facades"
	"goravel/app/jobs"
	nethttp "net/http"
	"os"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/mail"
	contractsqueue "github.com/goravel/framework/contracts/queue"

	nightwatch "github.com/vortechron/nightwatch-go"
	nwgoravel "github.com/vortechron/nightwatch-go/goravel"
)

type TaskController struct{}

func NewTaskController() *TaskController { return &TaskController{} }

// Show runs a query, uses the cache and writes a log line.
func (c *TaskController) Show(ctx http.Context) http.Response {
	id := ctx.Request().Route("id")
	reqCtx := ctx.Context()

	cache := facades.Cache().WithContext(reqCtx)
	if !cache.Has("task:" + id) {
		_ = cache.Put("task:"+id, "seen", time.Minute)
	}

	var rows []map[string]any
	if err := facades.Orm().WithContext(reqCtx).Query().Raw("select * from tasks where id = ?", id).Scan(&rows); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
	}

	facades.Log().WithContext(reqCtx).Info("task loaded")

	return ctx.Response().Json(http.StatusOK, http.Json{"rows": len(rows), "cached": cache.Get("task:" + id)})
}

// Boom fails with an unhandled error and a 500.
func (c *TaskController) Boom(ctx http.Context) http.Response {
	err := errors.New("task could not be loaded")
	nightwatch.ReportError(ctx.Context(), err)

	return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
}

// Handled catches an error on purpose and reports it as handled.
func (c *TaskController) Handled(ctx http.Context) http.Response {
	nightwatch.ReportHandled(ctx.Context(), errors.New("optional step skipped"))

	return ctx.Response().Json(http.StatusOK, http.Json{"ok": true})
}

// Welcome queues a job. The job runs in the worker on the same trace.
func (c *TaskController) Welcome(ctx http.Context) http.Response {
	queue := nwgoravel.Queue(ctx.Context(), facades.Queue())
	err := queue.Job(&jobs.SendWelcome{}, []contractsqueue.Arg{{Type: "string", Value: "new.user@example.test"}}).
		OnQueue(facades.Config().GetString("queue.connections.redis.queue")).Dispatch()
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
	}

	return ctx.Response().Json(http.StatusAccepted, http.Json{"queued": true})
}

// Mail sends a mail inside the request.
func (c *TaskController) Mail(ctx http.Context) http.Response {
	sender := nwgoravel.Mail(ctx.Context(), facades.Mail())
	err := sender.To([]string{"person@example.test"}).Subject("Hello from the example").
		Content(mail.Content{Html: "<p>Hello</p>"}).Send()
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"sent": true})
}

// Outgoing calls another HTTP service.
func (c *TaskController) Outgoing(ctx http.Context) http.Response {
	req, err := nethttp.NewRequestWithContext(ctx.Context(), nethttp.MethodGet, os.Getenv("OUTGOING_URL"), nil)
	if err != nil {
		return ctx.Response().Json(http.StatusBadGateway, http.Json{"error": err.Error()})
	}

	resp, err := (&nethttp.Client{Transport: nwgoravel.Transport(nil)}).Do(req)
	if err != nil {
		return ctx.Response().Json(http.StatusBadGateway, http.Json{"error": err.Error()})
	}
	defer resp.Body.Close()

	return ctx.Response().Json(http.StatusOK, http.Json{"status": resp.StatusCode})
}

// Login signs user 7 in and returns a token.
func (c *TaskController) Login(ctx http.Context) http.Response {
	token, err := facades.Auth(ctx).LoginUsingID(7)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"token": token})
}

// Me reads the token and answers with the signed-in user id.
func (c *TaskController) Me(ctx http.Context) http.Response {
	token := strings.TrimPrefix(ctx.Request().Header("Authorization"), "Bearer ")
	if _, err := facades.Auth(ctx).Parse(token); err != nil {
		return ctx.Response().Json(http.StatusUnauthorized, http.Json{"error": err.Error()})
	}

	id, _ := facades.Auth(ctx).ID()

	return ctx.Response().Json(http.StatusOK, http.Json{"id": id})
}

// Notify records a notification the app sent.
func (c *TaskController) Notify(ctx http.Context) http.Response {
	_, end := nightwatch.StartNotification(ctx.Context(), nightwatch.NotificationInfo{Channel: "database", Class: "InvoicePaid"})
	end(nil)

	return ctx.Response().Json(http.StatusOK, http.Json{"notified": true})
}
