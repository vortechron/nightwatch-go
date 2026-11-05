package nightwatch

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func useHarness(t *testing.T, h *harness) {
	t.Helper()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(h.tp)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
}

func TestInstrument_QueueRoundTripKeepsTrace(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)

	var carrier string
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		dctx, end := StartJobDispatch(ctx, JobInfo{Name: "jobs.SendEmail", Connection: "redis", Queue: "default"})
		carrier = EncodeJobEnvelope(dctx, "redis", "default", false)
		end(nil)
	})

	env, ok := DecodeJobEnvelope(carrier)
	if !ok || env.JobID == "" || env.Connection != "redis" || env.Queue != "default" {
		t.Fatalf("envelope: %q", carrier)
	}

	wctx, endAttempt := StartJobAttempt(context.Background(), env, JobInfo{Name: "jobs.SendEmail", Connection: "redis", Queue: "default"}, 1)
	_ = wctx
	endAttempt(errors.New("boom"))

	mustValidate(t, h.sink.all())
	req := h.sink.ofType("request")[0]
	queued := h.sink.ofType("queued-job")[0]
	attempt := h.sink.ofType("job-attempt")[0]
	if queued["job_id"] != env.JobID || attempt["job_id"] != env.JobID || attempt["trace_id"] != req["trace_id"] {
		t.Fatalf("not linked:\nqueued=%v\nattempt=%v\nreq=%v", queued, attempt, req["trace_id"])
	}
	if attempt["status"] != "failed" || len(h.sink.ofType("exception")) != 1 {
		t.Fatalf("failure not recorded: %v", attempt)
	}
}

func TestInstrument_JobWithoutEnvelopeStartsOwnTrace(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)
	if _, ok := DecodeJobEnvelope("not an envelope"); ok {
		t.Fatal("garbage must not decode")
	}
	_, end := StartJobAttempt(context.Background(), JobEnvelope{}, JobInfo{Name: "jobs.Cron"}, 3)
	end(nil)
	attempt := h.sink.ofType("job-attempt")[0]
	if attempt["attempt"] != 3 || attempt["job_id"] == "" {
		t.Fatalf("attempt %v", attempt)
	}
	mustValidate(t, h.sink.all())
}

func TestInstrument_CommandAndSchedule(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)

	ctx, end := StartCommand(context.Background(), CommandInfo{Name: "cleanup", Line: "artisan cleanup --force"})
	_, endCache := StartCache(ctx, "redis", "k")
	endCache(CacheHit, time.Minute, nil)
	end(0, nil)

	_, endTask := StartScheduledTask(context.Background(), TaskInfo{Name: "purge", Cron: "* * * * *", Timezone: "UTC", OnOneServer: true})
	endTask(TaskSkipped, nil)

	mustValidate(t, h.sink.all())
	if c := h.sink.ofType("command")[0]; c["exit_code"] != int64(0) || c["cache_events"] != 1 {
		t.Fatalf("command %v", c)
	}
	if s := h.sink.ofType("scheduled-task")[0]; s["status"] != "skipped" || s["on_one_server"] != true {
		t.Fatalf("task %v", s)
	}
}

func TestInstrument_MailNotificationUserAndHandled(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = SetUser(ctx, User{ID: "7", Name: "Ada", Username: "ada"})
		if UserFromContext(ctx) != "7" {
			t.Error("the user id must be kept in the context")
		}
		_, endMail := StartMail(ctx, MailInfo{Mailer: "smtp", Class: "Welcome", Subject: "Hi", To: 1})
		endMail(nil)
		_, endN := StartNotification(ctx, NotificationInfo{Channel: "mail", Class: "Alert"})
		endN(errors.New("down"))
		ReportHandled(ctx, errors.New("expected"))
		ReportError(ctx, errors.New("unexpected"))
	})
	mustValidate(t, h.sink.all())
	if h.sink.ofType("request")[0]["user"] != "7" || len(h.sink.ofType("user")) != 1 {
		t.Fatal("user")
	}
	if h.sink.ofType("notification")[0]["failed"] != true || h.sink.ofType("mail")[0]["subject"] != "Hi" {
		t.Fatal("mail/notification")
	}
	excs := h.sink.ofType("exception")
	if len(excs) != 3 {
		t.Fatalf("want 3 exceptions got %d", len(excs))
	}
	byMsg := map[string]Record{}
	for _, e := range excs {
		byMsg[e["message"].(string)] = e
	}
	if byMsg["expected"]["handled"] != true || byMsg["unexpected"]["handled"] != false {
		t.Fatalf("handled flags: %v", byMsg)
	}
	if byMsg["unexpected"]["file"] == "" {
		t.Fatalf("stack missing: %v", byMsg["unexpected"])
	}
}

func TestExceptionClass(t *testing.T) {
	type custom struct{ error }
	wrapped := errors.Join(errors.New("a"))
	for _, tt := range []struct {
		err  error
		want string
	}{
		{custom{errors.New("x")}, "nightwatch.custom"},
		{wrapped, "*errors.joinError"},
	} {
		if got := exceptionClass(tt.err); got != tt.want {
			t.Errorf("got %s want %s", got, tt.want)
		}
	}
}

func TestEnvelope_StrictAndCarriesTheUser(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)
	var carrier string
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = SetUser(ctx, User{ID: "u|1", Name: "x"})
		dctx, end := StartJobDispatch(ctx, JobInfo{Name: "j"})
		carrier = EncodeJobEnvelope(dctx, "re|dis", "q ueue", true)
		end(nil)
	})
	env, ok := DecodeJobEnvelope(carrier)
	if !ok || env.User != "u|1" || env.Connection != "re|dis" || env.Queue != "q ueue" || !env.Sync || !IsJobEnvelope(carrier) {
		t.Fatalf("round trip failed: %+v %q", env, carrier)
	}
	if EncodeJobEnvelope(context.Background(), "", "", false) != "" {
		t.Fatal("no dispatch span, no envelope")
	}

	tampered := strings.Replace(carrier, "re%7Cdis", "other", 1)
	for name, bad := range map[string]string{
		"checksum":   tampered,
		"truncated":  carrier[:len(carrier)-3],
		"empty":      "",
		"plain text": "hello|world",
		"tag":        strings.Replace(carrier, "nwtp2", "nwtp1", 1),
	} {
		if IsJobEnvelope(bad) {
			t.Errorf("%s must not be accepted", name)
		}
	}

	body := "nwtp2|00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01|not-a-uuid|c|q|u|0"
	if IsJobEnvelope(body + "|" + fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(body)))) {
		t.Fatal("a bad job id must be rejected even with a good checksum")
	}
	body = "nwtp2|garbage|" + uuid.NewString() + "|c|q|u|0"
	if IsJobEnvelope(body + "|" + fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(body)))) {
		t.Fatal("a bad traceparent must be rejected even with a good checksum")
	}
}
