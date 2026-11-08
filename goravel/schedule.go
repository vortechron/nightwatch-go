package goravel

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

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
func WrapSchedule(inner schedule.Schedule, artisan console.Artisan, options ...ScheduleOption) schedule.Schedule {
	facade := &scheduleFacade{Schedule: inner, artisan: artisan, ignored: map[string]bool{}, running: map[string]context.Context{}}
	for _, option := range options {
		option(facade)
	}

	return facade
}

// ScheduleOption changes how WrapSchedule records tasks.
type ScheduleOption func(*scheduleFacade)

// IgnoreTasks names scheduled tasks that are not recorded. A run is still an
// execution that is dropped as a whole, so its queries, logs and exceptions
// vanish with it. Use it for a task that is part of the telemetry path or runs
// so often that its records are only noise.
func IgnoreTasks(names ...string) ScheduleOption {
	return func(s *scheduleFacade) {
		for _, name := range names {
			s.ignored[name] = true
		}
	}
}

type scheduleFacade struct {
	schedule.Schedule
	artisan console.Artisan
	ignored map[string]bool

	mu      sync.Mutex
	running map[string]context.Context // task name to the context of its run in progress
}

// TaskContext returns the context of the run of task name that is in progress,
// or context.Background() when there is none (or s is not the wrapped
// schedule). A scheduler callback has no parameters, so this is how it gets the
// task's execution: pass the result to the database, cache and HTTP calls it
// makes, and they belong to the task instead of being records with no
// execution. It is keyed by name, so tasks must not overlap themselves
// (SkipIfStillRunning).
func TaskContext(s schedule.Schedule, name string) context.Context {
	facade, ok := s.(*scheduleFacade)
	if !ok {
		return context.Background()
	}

	facade.mu.Lock()
	defer facade.mu.Unlock()

	if ctx, found := facade.running[name]; found {
		return ctx
	}

	return context.Background()
}

func (s *scheduleFacade) begin(info nightwatch.TaskInfo) (context.Context, nightwatch.TaskEndFunc) {
	ctx, end := nightwatch.StartScheduledTask(context.Background(), info)
	if s.ignored[info.Name] {
		trace.SpanFromContext(ctx).SetAttributes(attribute.Bool(nightwatch.AttrIgnore, true))
	}

	s.mu.Lock()
	s.running[info.Name] = ctx
	s.mu.Unlock()

	return ctx, func(status string, err error) {
		s.mu.Lock()
		delete(s.running, info.Name)
		s.mu.Unlock()
		end(status, err)
	}
}

func (s *scheduleFacade) Register(events []schedule.Event) {
	wrapped := make([]schedule.Event, len(events))
	for i, event := range events {
		wrapped[i] = &tracedEvent{Event: event, artisan: s.artisan, facade: s}
	}

	s.Schedule.Register(wrapped)
}

// tracedEvent changes only what the scheduler runs. The scheduler asks the
// event for its command first, so a command event answers "" and hands over a
// callback that runs the command inside the task span instead.
type tracedEvent struct {
	schedule.Event
	artisan console.Artisan
	facade  *scheduleFacade
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
		_, end := e.facade.begin(taskInfo(e.Event))

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
