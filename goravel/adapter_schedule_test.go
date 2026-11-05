package goravel

import (
	"testing"

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
