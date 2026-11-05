package goravel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/goravel/framework/contracts/cache"
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/mail"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/contracts/schedule"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// sink collects records sent by a translator.
type sink struct {
	mu      sync.Mutex
	records []nightwatch.Record
}

func (s *sink) Enqueue(records []nightwatch.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, records...)
}

func (s *sink) ofType(kind string) []nightwatch.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []nightwatch.Record
	for _, r := range s.records {
		if r.Type() == kind {
			out = append(out, r)
		}
	}

	return out
}

func setup(t *testing.T) *sink {
	t.Helper()
	cfg := nightwatch.ConfigFromEnv(func(k string) string {
		return map[string]string{"NIGHTWATCH_TOKEN": "t"}[k]
	}, "host")
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	out := &sink{}
	tr := nightwatch.NewTranslator(cfg, out)
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tr.SpanExporter()))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = tp.Shutdown(context.Background()) })

	return out
}

// ---- cache ----

type fakeDriver struct {
	cache.Driver
	data map[string]any
}

func (f *fakeDriver) Has(key string) bool { _, ok := f.data[key]; return ok }
func (f *fakeDriver) Get(key string, _ ...any) any {
	return f.data[key]
}
func (f *fakeDriver) Put(key string, v any, _ time.Duration) error { f.data[key] = v; return nil }
func (f *fakeDriver) Add(key string, v any, _ time.Duration) bool  { f.data[key] = v; return true }
func (f *fakeDriver) Forever(key string, v any) bool               { f.data[key] = v; return true }
func (f *fakeDriver) Forget(key string) bool {
	_, ok := f.data[key]
	delete(f.data, key)

	return ok
}

func (f *fakeDriver) Remember(key string, _ time.Duration, cb func() (any, error)) (any, error) {
	if v, ok := f.data[key]; ok {
		return v, nil
	}

	return cb()
}

func TestCache_RecordsEvents(t *testing.T) {
	out := setup(t)
	ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "c"})
	d := Cache(ctx, &fakeDriver{data: map[string]any{}}, "redis")
	d.Get("a")
	_ = d.Put("a", 1, time.Minute)
	d.Get("a")
	d.Has("a")
	d.Add("b", 1, time.Minute)
	d.Forever("c", 1)
	d.Forget("a")
	d.Forget("zzz")
	_, _ = d.Remember("r", time.Minute, func() (any, error) { return 1, nil })
	end(0, nil)

	types := map[string]int{}
	for _, r := range out.ofType("cache-event") {
		types[r["type"].(string)]++
	}
	if types["miss"] != 2 || types["hit"] != 2 || types["write"] != 3 || types["delete"] != 1 || types["delete-failure"] != 1 {
		t.Fatalf("types: %v", types)
	}
	if out.ofType("command")[0]["cache_events"] != 9 {
		t.Fatalf("command: %v", out.ofType("command")[0])
	}
}

type fakeCache struct {
	cache.Cache
	driver cache.Driver
}

func (f *fakeCache) WithContext(context.Context) cache.Driver { return f.driver }
func (f *fakeCache) Store(string) cache.Driver                { return &fakeStore{Driver: f.driver} }

type fakeStore struct{ cache.Driver }

func (f *fakeStore) WithContext(context.Context) cache.Driver { return f.Driver }

func TestWrapCache_WithContextIsTraced(t *testing.T) {
	out := setup(t)
	wrapped := WrapCache(&fakeCache{driver: &fakeDriver{data: map[string]any{}}})
	ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "c"})
	wrapped.WithContext(ctx).Has("x")
	wrapped.Store("file").WithContext(ctx).Has("y")
	end(0, nil)
	events := out.ofType("cache-event")
	if len(events) != 2 || events[1]["store"] != "file" && events[0]["store"] != "file" {
		t.Fatalf("events: %v", events)
	}
}

// ---- schedule ----

type fakeEvent struct {
	schedule.Event
	name, cron string
}

func (e *fakeEvent) GetName() string              { return e.name }
func (e *fakeEvent) GetCron() string              { return e.cron }
func (e *fakeEvent) IsOnOneServer() bool          { return true }
func (e *fakeEvent) GetSkipIfStillRunning() bool  { return true }
func (e *fakeEvent) GetDelayIfStillRunning() bool { return false }

type fakeSchedule struct {
	schedule.Schedule
	event *fakeEvent
	run   func()
}

func (f *fakeSchedule) Call(cb func()) schedule.Event { f.run = cb; return f.event }

func TestSchedule_CallIsRecorded(t *testing.T) {
	out := setup(t)
	inner := &fakeSchedule{event: &fakeEvent{name: "purge", cron: "* * * * *"}}
	wrapped := WrapSchedule(inner)
	wrapped.Call(func() {})
	inner.run()
	tasks := out.ofType("scheduled-task")
	if len(tasks) != 1 || tasks[0]["name"] != "purge" || tasks[0]["status"] != "processed" || tasks[0]["on_one_server"] != true {
		t.Fatalf("tasks: %v", tasks)
	}

	wrapped.Call(func() { panic("bad") })
	func() {
		defer func() { _ = recover() }()
		inner.run()
	}()
	if got := out.ofType("scheduled-task"); len(got) != 2 || got[1]["status"] != "failed" {
		t.Fatalf("panic task: %v", got)
	}
	if info := taskInfo(nil); info.Name != "closure" {
		t.Fatal("nil event")
	}
}

// ---- console ----

type fakeCommand struct {
	console.Command
	err error
}

func (f *fakeCommand) Signature() string            { return "emails:send" }
func (f *fakeCommand) Handle(console.Context) error { return f.err }

type fakeConsoleCtx struct {
	console.Context
	ctx context.Context
}

func (f fakeConsoleCtx) Value(key any) any { return f.ctx.Value(key) }

type fakeArtisan struct {
	console.Artisan
	registered []console.Command
}

func (f *fakeArtisan) Register(c []console.Command) { f.registered = c }

func TestArtisan_CommandsAreRecorded(t *testing.T) {
	out := setup(t)
	inner := &fakeArtisan{}
	WrapArtisan(inner).Register([]console.Command{&fakeCommand{}, &fakeCommand{err: errors.New("fail")}})
	cctx := fakeConsoleCtx{ctx: context.Background()}
	if err := inner.registered[0].Handle(cctx); err != nil {
		t.Fatal(err)
	}
	if err := inner.registered[1].Handle(cctx); err == nil {
		t.Fatal("error must pass through")
	}
	cmds := out.ofType("command")
	if len(cmds) != 2 || cmds[0]["exit_code"] != int64(0) || cmds[1]["exit_code"] != int64(1) || cmds[0]["name"] != "emails:send" {
		t.Fatalf("commands: %v", cmds)
	}
}

func TestTracedConsoleContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &tracedConsoleContext{traced: ctx}
	if c.Err() != nil {
		t.Fatal("err")
	}
	cancel()
	<-c.Done()
	if _, ok := c.Deadline(); ok || c.Value("k") != nil || c.Err() == nil {
		t.Fatal("traced context passthrough")
	}
}

// ---- mail ----

type fakeMail struct {
	mail.Mail
	sent  int
	fail  error
	calls []string
}

func (f *fakeMail) To(_ []string) mail.Mail             { f.calls = append(f.calls, "to"); return f }
func (f *fakeMail) Cc(_ []string) mail.Mail             { return f }
func (f *fakeMail) Bcc(_ []string) mail.Mail            { return f }
func (f *fakeMail) Attach(_ []string) mail.Mail         { return f }
func (f *fakeMail) Subject(_ string) mail.Mail          { return f }
func (f *fakeMail) Content(mail.Content) mail.Mail      { return f }
func (f *fakeMail) From(mail.Address) mail.Mail         { return f }
func (f *fakeMail) Headers(map[string]string) mail.Mail { return f }
func (f *fakeMail) Send(...mail.Mailable) error         { f.sent++; return f.fail }
func (f *fakeMail) Queue(...mail.Mailable) error        { f.sent++; return f.fail }

type fakeMailable struct{ mail.Mailable }

func (fakeMailable) Attachments() []string { return []string{"a.pdf"} }
func (fakeMailable) Envelope() *mail.Envelope {
	return &mail.Envelope{Subject: "Welcome", To: []string{"a@b.c"}, Cc: []string{"c@b.c"}, Bcc: []string{"d@b.c"}}
}

func TestMail_SendIsRecorded(t *testing.T) {
	out := setup(t)
	ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "c"})
	inner := &fakeMail{}
	m := Mail(ctx, inner)
	chain := m.To([]string{"x@y.z", "q@y.z"}).Cc([]string{"c"}).Bcc([]string{"b"}).Attach([]string{"f"}).Subject("Hi").
		Content(mail.Content{}).From(mail.Address{}).Headers(nil)
	if err := chain.Send(); err != nil {
		t.Fatal(err)
	}
	if err := m.Send(fakeMailable{}); err != nil {
		t.Fatal(err)
	}
	inner.fail = errors.New("smtp down")
	if err := m.Queue(); err == nil {
		t.Fatal("error must pass through")
	}
	end(0, nil)
	mails := out.ofType("mail")
	if len(mails) != 3 {
		t.Fatalf("mails: %d", len(mails))
	}
	byClass := map[string]nightwatch.Record{}
	for _, r := range mails {
		byClass[r["class"].(string)+r["subject"].(string)] = r
	}
	first := byClass["mailHi"]
	if first["to"] != int64(2) || first["cc"] != int64(1) || first["attachments"] != int64(1) {
		t.Fatalf("first mail: %v", first)
	}
	if byClass["goravel.fakeMailableWelcome"]["bcc"] != int64(1) {
		t.Fatalf("mailable: %v", byClass)
	}
	if m.(*tracedMail).to != 0 {
		t.Fatal("wrapper must not keep recipients between mails")
	}
}

// ---- queue ----

type fakeJob struct {
	queue.Job
	got []any
	err error
	ctx context.Context
}

func (j *fakeJob) Signature() string        { return "jobs.SendEmail" }
func (j *fakeJob) Handle(args ...any) error { j.got = args; return j.err }

type ctxJob struct {
	fakeJob
}

func (j *ctxJob) HandleContext(ctx context.Context, args ...any) error {
	j.ctx = ctx
	j.got = args

	return nil
}

type fakePending struct {
	queue.PendingJob
	owner *fakeQueue
}

func (p *fakePending) OnConnection(c string) queue.PendingJob {
	p.owner.opts = append(p.owner.opts, "conn:"+c)
	return p
}

func (p *fakePending) OnQueue(q string) queue.PendingJob {
	p.owner.opts = append(p.owner.opts, "queue:"+q)
	return p
}

func (p *fakePending) Delay(time.Time) queue.PendingJob {
	p.owner.opts = append(p.owner.opts, "delay")
	return p
}
func (p *fakePending) Dispatch() error     { p.owner.dispatched++; return nil }
func (p *fakePending) DispatchSync() error { p.owner.dispatched++; return nil }

type fakeQueue struct {
	queue.Queue
	args       []queue.Arg
	opts       []string
	dispatched int
	registered []queue.Job
}

func (f *fakeQueue) Job(_ queue.Job, args ...[]queue.Arg) queue.PendingJob {
	if len(args) > 0 {
		f.args = args[0]
	}

	return &fakePending{owner: f}
}
func (f *fakeQueue) Register(jobs []queue.Job) { f.registered = jobs }

func TestQueue_DispatchCarriesEnvelopeAndWorkerContinuesTrace(t *testing.T) {
	out := setup(t)
	inner := &fakeQueue{}
	ctx, endCmd := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "dispatcher"})

	err := Queue(ctx, inner).Job(&fakeJob{}, []queue.Arg{{Type: "string", Value: "hello"}}).
		OnConnection("redis").OnQueue("mail").Delay(time.Now()).Dispatch()
	if err != nil || inner.dispatched != 1 {
		t.Fatalf("dispatch: %v", err)
	}
	if err = Queue(ctx, inner).Job(&fakeJob{}).DispatchSync(); err != nil {
		t.Fatal(err)
	}
	endCmd(0, nil)
	if len(inner.opts) < 3 {
		t.Fatalf("options not applied: %v", inner.opts)
	}

	// First dispatch had one real argument plus the envelope.
	inner2 := &fakeQueue{}
	_ = Queue(ctx, inner2).Job(&fakeJob{}, []queue.Arg{{Type: "string", Value: "hello"}}).OnQueue("mail").Dispatch()
	if len(inner2.args) != 2 || inner2.args[0].Value != "hello" {
		t.Fatalf("args: %v", inner2.args)
	}
	envelope := inner2.args[1].Value.(string)

	// Worker side.
	reg := &fakeQueue{}
	WrapQueue(reg).Register([]queue.Job{&fakeJob{err: errors.New("boom")}, &ctxJob{}})
	failing, withCtx := reg.registered[0], reg.registered[1]

	if err = failing.Handle("hello", envelope); err == nil {
		t.Fatal("job error must pass through")
	}
	_ = failing.Handle("hello", envelope)
	if err = withCtx.Handle("hello", envelope); err != nil {
		t.Fatal(err)
	}
	if got := withCtx.(*tracedJob).Job.(*ctxJob).got; len(got) != 1 || got[0] != "hello" {
		t.Fatalf("envelope must be stripped: %v", got)
	}
	if withCtx.(*tracedJob).Job.(*ctxJob).ctx == nil {
		t.Fatal("HandleContext must get a context")
	}

	attempts := out.ofType("job-attempt")
	if len(attempts) != 3 {
		t.Fatalf("attempts: %d", len(attempts))
	}
	if attempts[0]["status"] != "failed" || attempts[0]["queue"] != "mail" || attempts[1]["attempt"] != 2 {
		t.Fatalf("attempt records: %v / %v", attempts[0], attempts[1])
	}
	if _, offers := failing.(queue.JobWithShouldRetry); offers {
		t.Fatal("a job without ShouldRetry must not gain one")
	}
}

func TestSplitEnvelope_PlainArgsUntouched(t *testing.T) {
	env, rest := splitEnvelope([]any{"a", 2})
	if env.TraceParent != "" || len(rest) != 2 {
		t.Fatal("plain args must pass through")
	}
	if _, rest = splitEnvelope(nil); len(rest) != 0 {
		t.Fatal("nil")
	}
}

func TestAttemptCounter_Bounded(t *testing.T) {
	c := newAttemptCounter()
	if c.next("") != 1 || c.next("a") != 1 || c.next("a") != 2 {
		t.Fatal("count")
	}
	c.done("a")
	if c.next("a") != 1 {
		t.Fatal("done must reset")
	}
	for i := 0; i <= maxTrackedJobs; i++ {
		c.next(string(rune(i+1000)) + "x")
	}
	if len(c.counts) > maxTrackedJobs {
		t.Fatal("must stay bounded")
	}
}

// ---- client helpers ----

func TestClientFactories(t *testing.T) {
	SetClient(nightwatch.New(nightwatch.Config{Enabled: false}))
	if Client().Enabled() {
		t.Fatal("set client")
	}
	if exp, err := TraceExporter()(context.Background()); err != nil || exp == nil {
		t.Fatal("trace exporter")
	}
	if exp, err := LogExporter()(context.Background()); err != nil || exp == nil {
		t.Fatal("log exporter")
	}
}

func TestShutdownRunner(t *testing.T) {
	SetClient(nightwatch.New(nightwatch.Config{Enabled: false}))
	r := ServiceProvider{}.Runners(nil)[0].(*shutdownRunner)
	if r.ShouldRun() || r.Signature() != "nightwatch" || r.Run() != nil || r.ShutdownPriority() != 200 {
		t.Fatal("runner basics")
	}
	if err := r.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestRequestAttributes(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/a?x=1", nil)
	req.ContentLength = 12
	attrs := originAttributes(req)
	if len(attrs) != 2 {
		t.Fatalf("attrs: %v", attrs)
	}
}

type retryJob struct{ fakeJob }

func (retryJob) ShouldRetry(error, int) (bool, time.Duration) { return false, time.Second }

func TestWrapJob_KeepsOwnRetryRule(t *testing.T) {
	wrapped := wrapJob(&retryJob{}, newAttemptCounter())
	retry, ok := wrapped.(queue.JobWithShouldRetry)
	if !ok {
		t.Fatal("must offer ShouldRetry")
	}
	if again, delay := retry.ShouldRetry(nil, 1); again || delay != time.Second {
		t.Fatal("must delegate")
	}
}

func TestTransport_RecordsOutgoingRequest(t *testing.T) {
	out := setup(t)
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()

	ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "c"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.URL+"/x", nil)
	resp, err := (&http.Client{Transport: Transport(nil)}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	end(0, nil)

	got := out.ofType("outgoing-request")
	if len(got) != 1 || got[0]["status_code"] != int64(200) || got[0]["execution_source"] != "command" {
		t.Fatalf("outgoing: %v", got)
	}
}
