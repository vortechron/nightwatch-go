# Scheduled Tasks

## Introduction

The provider wraps Goravel's scheduler, so every run of a registered event is a `scheduled-task` execution. For `Call` events the callback runs inside the task. For `Command` events the task runs the command through artisan, which is wrapped too, so one run gives a `scheduled-task` record and a `command` record, as in Laravel.

## Passing the task's context

A scheduler callback takes no parameters, so the queries and cache calls inside it have no context to join. `nwgoravel.TaskContext` returns the context of the run in progress:

```go
return sched.Call(func() {
	ctx := nwgoravel.TaskContext(sched, "prune-tokens")
	_, _ = db.WithContext(ctx).Query().Exec("delete from tokens where expires_at < now()")
}).Hourly().Name("prune-tokens").SkipIfStillRunning()
```

Here `sched` is `facades.Schedule()` and `db` is `facades.Orm()`. `TaskContext` is keyed by the task name, so a task must not overlap itself: use `SkipIfStillRunning`. Without a run in progress, it returns `context.Background()`.

## Sampling

`NIGHTWATCH_SCHEDULED_TASK_SAMPLE_RATE` sets the share of task runs kept.

## Filtering

`NIGHTWATCH_IGNORE_SCHEDULED_TASKS` lists task names, comma separated, that are not recorded. A run is still an execution that is dropped as a whole, so its queries, logs and exceptions go with it. Use it for a task that is part of the telemetry path, or one that runs so often that its records are noise.

```ini
NIGHTWATCH_IGNORE_SCHEDULED_TASKS=prune-tokens,heartbeat
```

`nwgoravel.IgnoreTasks(names...)` is the same option for `nwgoravel.WrapSchedule`.
