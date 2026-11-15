# Commands

## Introduction

The provider wraps Goravel's artisan, so every command run is a `command` execution with its exit code. That includes the commands the framework registered before the client started, such as `schedule:list`, `migrate` and `queue:work`. The client flushes when a command ends, because the process may exit right after it.

## Passing the command's context

Goravel's `console.Context` is a `context.Context`. Pass it to the database and the log, and the command's work joins its execution. This comes from the example app:

```go
func (c *Hello) Handle(ctx console.Context) error {
	var rows []map[string]any
	if err := facades.Orm().WithContext(ctx).Query().Raw("select * from tasks").Scan(&rows); err != nil {
		return err
	}

	facades.Log().WithContext(ctx).Info("hello from the command")

	return nil
}
```

A command called from other code (`Artisan().Call`, or a scheduled command) starts its own execution.

## Exit codes

The exit codes follow Goravel's: 0 on success, 1 on failure, and 0 when the command was cancelled.

## Sampling

`NIGHTWATCH_COMMAND_SAMPLE_RATE` sets the share of command runs kept.
