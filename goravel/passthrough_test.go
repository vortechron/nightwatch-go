package goravel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/mail"
	"github.com/goravel/framework/contracts/queue"
	mocksfoundation "github.com/goravel/framework/mocks/foundation"
	mockshttp "github.com/goravel/framework/mocks/http"
	mocksqueue "github.com/goravel/framework/mocks/queue"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// Without a Nightwatch context (disabled, or code running outside a request,
// command, job or scheduled task) every helper must be pure pass-through: the
// same object back, no span, no extra job argument.

func TestActive_OnlyNightwatchExecutionsAreMarked(t *testing.T) {
	setup(t)
	if nightwatch.Active(context.Background()) {
		t.Fatal("a plain context is not active")
	}
	ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "c"})
	defer end(0, nil)
	if !nightwatch.Active(ctx) || !nightwatch.Active(nightwatch.MarkActive(context.Background())) {
		t.Fatal("commands and marked contexts are active")
	}
}

func TestQueue_PassThroughWhenInactive(t *testing.T) {
	out := setup(t)
	inner := &fakeQueue{}

	if got := Queue(context.Background(), inner); got != queue.Queue(inner) {
		t.Fatal("must return the very same queue")
	}
	if err := Queue(context.Background(), inner).Job(&fakeJob{}, []queue.Arg{{Type: "string", Value: "x"}}).Dispatch(); err != nil {
		t.Fatal(err)
	}
	if len(inner.args) != 1 || inner.args[0].Value != "x" {
		t.Fatalf("no envelope may be added: %v", inner.args)
	}
	if len(out.ofType("queued-job")) != 0 {
		t.Fatal("no spans")
	}
}

func TestQueue_EnvelopeIsAddedOnlyForAnActiveContext(t *testing.T) {
	setup(t)
	inner := &fakeQueue{}
	ctx, done := dispatchCtx(t)
	defer done()

	_ = Queue(ctx, inner).Job(&fakeJob{}, []queue.Arg{{Type: "string", Value: "x"}}).Dispatch()
	if len(inner.args) != 2 || !nightwatch.IsJobEnvelope(inner.args[1].Value.(string)) {
		t.Fatalf("active context: %v", inner.args)
	}
}

func TestMailCacheAndTransport_PassThroughWhenInactive(t *testing.T) {
	out := setup(t)
	bg := context.Background()

	mailer := &fakeMail{}
	if Mail(bg, mailer) != mail.Mail(mailer) {
		t.Fatal("mail must be returned as is")
	}

	driver := &fakeDriver{data: map[string]any{}}
	if Cache(bg, driver, "x") != any(driver) {
		t.Fatal("cache must be returned as is")
	}
	wrapped := WrapCache(&fakeCache{driver: driver})
	if wrapped.WithContext(bg) != any(driver) || wrapped.Store("file").WithContext(bg) != any(driver) {
		t.Fatal("WithContext without an active context must hand back the real driver")
	}

	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	req, _ := http.NewRequestWithContext(bg, http.MethodGet, target.URL, nil)
	resp, err := (&http.Client{Transport: Transport(nil)}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if len(out.ofType("outgoing-request"))+len(out.ofType("cache-event"))+len(out.ofType("mail")) != 0 {
		t.Fatal("no spans without an active context")
	}
}

func TestWorker_ToleratesJobsWithAndWithoutAnEnvelope(t *testing.T) {
	out := setup(t)
	ctx, done := dispatchCtx(t)
	inner := &fakeQueue{}
	_ = Queue(ctx, inner).Job(&fakeJob{}, []queue.Arg{{Type: "string", Value: "hello"}}).Dispatch()
	envelope := inner.args[1].Value.(string)
	done()

	for name, wrap := range map[string]func(queue.Queue) queue.Queue{"tracing": WrapQueue, "disabled": TolerateEnvelopes} {
		t.Run(name, func(t *testing.T) {
			reg := &fakeQueue{}
			wrap(reg).Register([]queue.Job{&fakeJob{}})
			job := reg.registered[0]

			if err := job.Handle("hello", envelope); err != nil {
				t.Fatal(err)
			}
			if got := job.(*tracedJob).Job.(*fakeJob).got; len(got) != 1 || got[0] != "hello" {
				t.Fatalf("with envelope: the job must see only its own arguments, got %v", got)
			}
			if err := job.Handle("hello"); err != nil {
				t.Fatal(err)
			}
			if got := job.(*tracedJob).Job.(*fakeJob).got; len(got) != 1 || got[0] != "hello" {
				t.Fatalf("without envelope: arguments untouched, got %v", got)
			}
			job.(*tracedJob).Job.(*fakeJob).err = errors.New("boom")
			if err := job.Handle("x", envelope); err == nil {
				t.Fatal("job errors pass through")
			}
		})
	}

	// Only the tracing worker records attempts: 3 handles in that subtest.
	if got := len(out.ofType("job-attempt")); got != 3 {
		t.Fatalf("tracing worker attempts: %d (the disabled worker must record none)", got)
	}
}

func TestProvider_DisabledStillStripsEnvelopesButWrapsNothingElse(t *testing.T) {
	for name, clientErr := range map[string]error{"disabled client": nil, "no client": errors.New("none")} {
		t.Run(name, func(t *testing.T) {
			app := mocksfoundation.NewApplication(t)
			if clientErr == nil {
				app.EXPECT().Make(BindingClient).Return(nightwatch.New(nightwatch.Config{}), nil)
			} else {
				app.EXPECT().Make(BindingClient).Return(nil, clientErr)
			}
			// Only the queue job registry is touched; MakeCache, MakeSchedule and
			// MakeArtisan are not expected, so a call to them fails the test.
			app.EXPECT().MakeQueue().Return(mocksqueue.NewQueue(t))
			app.EXPECT().Instance(binding.Queue, mock.Anything).Once()
			app.EXPECT().Fresh(binding.Queue).Once()

			NightwatchServiceProvider{}.Boot(app)
		})
	}

	app := mocksfoundation.NewApplication(t)
	app.EXPECT().Make(BindingClient).Return(nil, errors.New("none"))
	app.EXPECT().MakeQueue().Return(nil)
	NightwatchServiceProvider{}.Boot(app) // no queue at all: nothing to do
}

func TestMiddleware_MarksTheRequestContextActiveOnlyWhenEnabled(t *testing.T) {
	setup(t)
	for name, tc := range map[string]struct {
		client *nightwatch.Client
		want   bool
	}{"enabled": {enabledClient(nil), true}, "disabled": {nightwatch.New(nightwatch.Config{}), false}} {
		t.Run(name, func(t *testing.T) {
			ctx, span := rootSpan(context.Background())
			defer span.End()
			var active bool
			var m httpMocks
			if tc.want {
				m = newHTTPMocks(ctx, t, "GET", "", 200, func() {})
				m.req.EXPECT().Next().Unset()
				m.req.EXPECT().Next().Run(func() { active = nightwatch.Active(m.ctx.Context()) })
			} else {
				m.ctx = mockshttp.NewContext(t)
				m.req = mockshttp.NewContextRequest(t)
				m.ctx.EXPECT().Request().Return(m.req)
				m.ctx.EXPECT().Context().Return(ctx).Maybe()
				m.req.EXPECT().Next().Run(func() { active = nightwatch.Active(m.ctx.Context()) })
			}
			Middleware(appWith(t, tc.client, "", errors.New("guest"))).Handle(m.ctx)
			if active != tc.want {
				t.Fatalf("active=%v want %v", active, tc.want)
			}
		})
	}
}
