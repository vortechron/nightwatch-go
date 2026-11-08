package goravel

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/schedule"
)

type fakeEvent struct {
	schedule.Event
	name, cron, command string
	callback            func()
}

func (e *fakeEvent) GetName() string              { return e.name }
func (e *fakeEvent) GetCron() string              { return e.cron }
func (e *fakeEvent) GetCommand() string           { return e.command }
func (e *fakeEvent) GetCallback() func()          { return e.callback }
func (e *fakeEvent) IsOnOneServer() bool          { return true }
func (e *fakeEvent) GetSkipIfStillRunning() bool  { return true }
func (e *fakeEvent) GetDelayIfStillRunning() bool { return false }

type fakeSchedule struct {
	schedule.Schedule
	registered []schedule.Event
}

func (f *fakeSchedule) Register(events []schedule.Event) { f.registered = events }

type recordingArtisan struct {
	console.Artisan
	called []string
}

func (r *recordingArtisan) Call(command string) error {
	r.called = append(r.called, command)

	return nil
}

func TestSchedule_CallEventsAreRecorded(t *testing.T) {
	out := setup(t)
	inner := &fakeSchedule{}
	ran := 0
	WrapSchedule(inner, nil).Register([]schedule.Event{
		&fakeEvent{name: "purge", cron: "* * * * *", callback: func() { ran++ }},
		&fakeEvent{name: "boom", callback: func() { panic("bad") }},
	})

	inner.registered[0].GetCallback()()
	func() {
		defer func() { _ = recover() }()
		inner.registered[1].GetCallback()()
	}()

	tasks := out.ofType("scheduled-task")
	if ran != 1 || len(tasks) != 2 || tasks[0]["name"] != "purge" || tasks[0]["status"] != "processed" || tasks[0]["on_one_server"] != true {
		t.Fatalf("tasks: %v", tasks)
	}
	if tasks[1]["status"] != "failed" {
		t.Fatalf("a panic fails the task: %v", tasks[1])
	}
	if info := taskInfo(nil); info.Name != "closure" {
		t.Fatal("nil event")
	}
}

func TestSchedule_CommandEventsRecordATaskAndRunTheCommandThroughArtisan(t *testing.T) {
	out := setup(t)
	inner := &fakeSchedule{}
	artisan := &recordingArtisan{}
	WrapSchedule(inner, artisan).Register([]schedule.Event{&fakeEvent{name: "emails:send", command: "emails:send", cron: "0 * * * *"}})

	event := inner.registered[0]
	if event.GetCommand() != "" {
		t.Fatal("the scheduler must not run the command itself, the task does")
	}
	event.GetCallback()()

	if len(artisan.called) != 1 || artisan.called[0] != "emails:send" {
		t.Fatalf("artisan: %v", artisan.called)
	}
	if tasks := out.ofType("scheduled-task"); len(tasks) != 1 || tasks[0]["name"] != "emails:send" {
		t.Fatalf("tasks: %v", tasks)
	}
}

func TestSchedule_CommandEventWithoutArtisanIsLeftAlone(t *testing.T) {
	setup(t)
	inner := &fakeSchedule{}
	WrapSchedule(inner, nil).Register([]schedule.Event{&fakeEvent{command: "x"}})
	if inner.registered[0].GetCommand() != "x" || inner.registered[0].GetCallback() != nil {
		t.Fatal("without artisan the event must run as before")
	}
}

func TestSchedule_TaskContextGivesTheCallbackItsExecution(t *testing.T) {
	out, translator := setupTranslator(t)
	inner := &fakeSchedule{}
	wrapped := WrapSchedule(inner, nil)
	wrapped.Register([]schedule.Event{&fakeEvent{name: "purge", callback: func() {
		_, span := otel.Tracer("db").Start(TaskContext(wrapped, "purge"), "SELECT jobs", trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(attribute.String("db.system.name", "sqlite"), attribute.String("db.query.text", "select 1")))
		span.End()
	}}})

	inner.registered[0].GetCallback()()
	time.Sleep(5 * time.Millisecond)
	translator.Sweep()
	translator.Drain()

	tasks, queries := out.ofType("scheduled-task"), out.ofType("query")
	if len(tasks) != 1 || len(queries) != 1 {
		t.Fatalf("tasks %d queries %d, want 1 and 1", len(tasks), len(queries))
	}
	if queries[0]["execution_id"] == "" || queries[0]["execution_source"] != "schedule" {
		t.Fatalf("the query does not belong to the task: %v", queries[0])
	}
	if TaskContext(wrapped, "purge") == nil || TaskContext(nil, "purge") == nil {
		t.Fatal("TaskContext always returns a context")
	}
}

func TestSchedule_AnIgnoredTaskLeavesNothingNotEvenItsQueries(t *testing.T) {
	out, translator := setupTranslator(t)
	inner := &fakeSchedule{}
	wrapped := WrapSchedule(inner, nil, IgnoreTasks("flush"))
	body := func(name string) func() {
		return func() {
			_, span := otel.Tracer("db").Start(TaskContext(wrapped, name), "SELECT "+name, trace.WithSpanKind(trace.SpanKindClient),
				trace.WithAttributes(attribute.String("db.system.name", "sqlite"), attribute.String("db.query.text", "select 1")))
			span.End()
		}
	}
	wrapped.Register([]schedule.Event{&fakeEvent{name: "flush", callback: body("flush")}, &fakeEvent{name: "keep", callback: body("keep")}})

	inner.registered[0].GetCallback()()
	inner.registered[1].GetCallback()()
	time.Sleep(5 * time.Millisecond)
	translator.Sweep()
	translator.Drain()

	tasks, queries := out.ofType("scheduled-task"), out.ofType("query")
	if len(tasks) != 1 || tasks[0]["name"] != "keep" || len(queries) != 1 {
		t.Fatalf("want only the task that is not ignored and its query: tasks %v queries %v", tasks, queries)
	}
}
