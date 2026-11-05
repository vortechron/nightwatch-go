package goravel

import (
	"time"

	"github.com/goravel/framework/contracts/schedule"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// WrapSchedule records every run of a task added with Call as a
// scheduled-task execution. Tasks added with Command run through the console
// and are recorded as commands instead (see WrapArtisan).
func WrapSchedule(inner schedule.Schedule) schedule.Schedule {
	return &scheduleFacade{Schedule: inner}
}

type scheduleFacade struct{ schedule.Schedule }

func (s *scheduleFacade) Call(callback func()) schedule.Event {
	var event schedule.Event

	// The builder methods (Cron, Name ...) run after Call returns, so the
	// task details are read when the task runs, not now.
	event = s.Schedule.Call(func() {
		_, end := nightwatch.StartScheduledTask(contextBackground(), taskInfo(event))

		defer func() {
			if recovered := recover(); recovered != nil {
				end(nightwatch.TaskFailed, panicError(recovered))
				panic(recovered)
			}
		}()

		callback()
		end(nightwatch.TaskProcessed, nil)
	})

	return event
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
