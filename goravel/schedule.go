package goravel

import (
	"context"
	"time"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/schedule"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// WrapSchedule records every run of a registered event as a scheduled-task
// execution. It wraps the events when they are registered, because the builder
// calls (Cron, Name, ...) return the framework's own event, not ours.
//
// For Call events the callback runs inside the task. For Command events the
// task runs the command through artisan, which is wrapped too, so a command
// run produces a scheduled-task record and a command record, like Laravel.
// artisan may be nil when the app has none; command events then run unwrapped.
func WrapSchedule(inner schedule.Schedule, artisan console.Artisan) schedule.Schedule {
	return &scheduleFacade{Schedule: inner, artisan: artisan}
}

type scheduleFacade struct {
	schedule.Schedule
	artisan console.Artisan
}

func (s *scheduleFacade) Register(events []schedule.Event) {
	wrapped := make([]schedule.Event, len(events))
	for i, event := range events {
		wrapped[i] = &tracedEvent{Event: event, artisan: s.artisan}
	}

	s.Schedule.Register(wrapped)
}

// tracedEvent changes only what the scheduler runs. The scheduler asks the
// event for its command first, so a command event answers "" and hands over a
// callback that runs the command inside the task span instead.
type tracedEvent struct {
	schedule.Event
	artisan console.Artisan
}

func (e *tracedEvent) GetCommand() string {
	if e.artisan != nil {
		return ""
	}

	return e.Event.GetCommand()
}

func (e *tracedEvent) GetCallback() func() {
	command := e.Event.GetCommand()
	callback := e.Event.GetCallback()

	if command != "" && e.artisan != nil {
		return e.traced(func() {
			_ = e.artisan.Call(command)
		})
	}

	if callback == nil {
		return nil
	}

	return e.traced(callback)
}

func (e *tracedEvent) traced(run func()) func() {
	return func() {
		_, end := nightwatch.StartScheduledTask(context.Background(), taskInfo(e.Event))

		defer func() {
			if recovered := recover(); recovered != nil {
				end(nightwatch.TaskFailed, panicError(recovered))
				panic(recovered)
			}
		}()

		run()
		end(nightwatch.TaskProcessed, nil)
	}
}

func taskInfo(event schedule.Event) nightwatch.TaskInfo {
	info := nightwatch.TaskInfo{Name: "closure", Timezone: time.Local.String()}
	if event == nil {
		return info
	}

	if name := event.GetName(); name != "" {
		info.Name = name
	}

	info.Cron = event.GetCron()
	info.OnOneServer = event.IsOnOneServer()
	info.WithoutOverlapping = event.GetSkipIfStillRunning() || event.GetDelayIfStillRunning()

	return info
}
