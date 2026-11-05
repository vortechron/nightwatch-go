package goravel

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/console"

	nightwatch "github.com/vortechron/nightwatch-go"
)

func panicError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}

	return fmt.Errorf("panic: %v", recovered)
}

// WrapArtisan records every run of a registered command as a command
// execution with its exit code. flush is called when a command ends, because a
// command is short lived and the process may exit right after it.
func WrapArtisan(inner console.Artisan, flush func(context.Context)) console.Artisan {
	return &artisanFacade{Artisan: inner, flush: flush}
}

type artisanFacade struct {
	console.Artisan
	flush func(context.Context)
}

func (a *artisanFacade) Register(commands []console.Command) {
	wrapped := make([]console.Command, len(commands))
	for i, command := range commands {
		wrapped[i] = &tracedCommand{Command: command, flush: a.flush}
	}

	a.Artisan.Register(wrapped)
}

type tracedCommand struct {
	console.Command
	flush func(context.Context)
}

func (c *tracedCommand) Handle(ctx console.Context) (err error) {
	info := nightwatch.CommandInfo{
		Name:  c.Signature(),
		Class: fmt.Sprintf("%T", c.Command),
		Line:  strings.Join(os.Args[1:], " "),
	}

	traced, end := nightwatch.StartCommand(ctx, info)
	ctx = &tracedConsoleContext{Context: ctx, traced: traced}

	defer func() {
		if recovered := recover(); recovered != nil {
			end(1, panicError(recovered))
			c.flushNow()
			panic(recovered)
		}

		end(exitCode(err), err)
		c.flushNow()
	}()

	return c.Command.Handle(ctx)
}

func exitCode(err error) int {
	if err != nil {
		return 1
	}

	return 0
}

// tracedConsoleContext keeps the console helpers (Ask, Option ...) of the
// original context but answers context.Context calls with the traced one, so
// work the command does with ctx (queries, HTTP calls) joins its trace.
type tracedConsoleContext struct {
	console.Context
	traced context.Context
}

func (c *tracedConsoleContext) Deadline() (time.Time, bool) { return c.traced.Deadline() }

func (c *tracedConsoleContext) Done() <-chan struct{} { return c.traced.Done() }

func (c *tracedConsoleContext) Err() error { return c.traced.Err() }

func (c *tracedConsoleContext) Value(key any) any { return c.traced.Value(key) }

// flushNow sends what is buffered; waiting for the timer would lose the
// records of a command whose process exits right away.
func (c *tracedCommand) flushNow() {
	if c.flush == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()

	c.flush(ctx)
}

const flushTimeout = 5 * time.Second
