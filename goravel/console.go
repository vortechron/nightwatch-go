package goravel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/support/color"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// panicError turns a recovered value into an error without dumping it. A
// panic value can be a struct that holds a request, a token or a password, and
// printing it with %v would copy that into the exception record. Errors and
// strings are used as they are; anything else is reported by its type only.
// listCommand is what `artisan` alone runs.
const listCommand = "list"

func panicError(recovered any) error {
	switch value := recovered.(type) {
	case error:
		return value
	case string:
		return fmt.Errorf("panic: %s", value) //nolint:err113 // built from the panic value
	default:
		return fmt.Errorf("panic: %T", value) //nolint:err113 // type only, never the value
	}
}

// WrapArtisan records every artisan command run as a command execution with
// its exit code, and flushes before the process exits.
//
// It does this in Run, so commands the framework registered before this client
// started (schedule:list, migrate, queue:work ...) are recorded too. A command
// registered through the wrapper joins the run's execution instead of starting
// a second one, so the work it does with its context is linked to it. Commands
// that are called from other code (Artisan().Call, scheduled commands) start
// their own execution.
//
// flush is called when a command ends, because a command is short lived and
// the process may exit right after it. Run replaces the inner exit with its own
// after the flush; the exit codes follow Goravel's (0, 1 on failure, 0 when
// cancelled).
func WrapArtisan(inner console.Artisan, flush func(context.Context)) console.Artisan {
	return &artisanFacade{Artisan: inner, flush: flush, exit: os.Exit, run: &runState{}}
}

type artisanFacade struct {
	console.Artisan
	flush func(context.Context)
	exit  func(int)
	run   *runState
}

// runState is the artisan run in progress, if any.
type runState struct {
	mu     sync.Mutex
	name   string
	ctx    context.Context //nolint:containedctx // the context of one command run
	active bool
}

func (s *runState) begin(name string, ctx context.Context) { //nolint:revive // keeps the pair together
	s.mu.Lock()
	defer s.mu.Unlock()
	s.name, s.ctx, s.active = name, ctx, true
}

func (s *runState) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.name, s.ctx, s.active = "", nil, false
}

// current returns the context of the run when it is the run of this command.
func (s *runState) current(name string) (context.Context, bool) {
	if s == nil {
		return nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.ctx, s.active && s.name == name
}

// Run traces an artisan invocation. Runs that are not artisan runs (a normal
// start of the app) pass through untouched.
func (a *artisanFacade) Run(args []string, exitIfArtisan bool) error {
	name, line, ok := artisanInvocation(args)
	if !ok {
		return a.Artisan.Run(args, exitIfArtisan) //nolint:wrapcheck // passthrough
	}

	ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: name, Line: line})
	a.run.begin(name, ctx)

	err := a.runInner(args, end)

	a.run.finish()
	end(exitCode(err), err)
	a.flushNow()

	if exitIfArtisan {
		a.exitFor(err)
	}

	return err
}

// runInner runs the command without letting the inner Run exit the process.
// A panic is recorded and flushed before it goes on.
func (a *artisanFacade) runInner(args []string, end nightwatch.CommandEndFunc) error {
	defer func() {
		if recovered := recover(); recovered != nil {
			a.run.finish()
			end(1, panicError(recovered))
			a.flushNow()
			panic(recovered)
		}
	}()

	return a.Artisan.Run(args, false) //nolint:wrapcheck // passthrough
}

func (a *artisanFacade) exitFor(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		a.exit(0)

		return
	}

	color.Errorln(err.Error())
	a.exit(1)
}

func (a *artisanFacade) flushNow() {
	if a.flush == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()

	a.flush(ctx)
}

// artisanInvocation reads the command name from ["./app", "artisan", name, ...].
// Flags before the name are skipped; "artisan" alone means "list", as in Goravel.
func artisanInvocation(args []string) (name, line string, ok bool) {
	index := slices.Index(args, "artisan")
	if index < 0 {
		return "", "", false
	}

	rest := args[index+1:]
	if len(rest) == 0 {
		return listCommand, listCommand, true
	}

	name = listCommand

	for _, arg := range rest {
		if !strings.HasPrefix(arg, "-") {
			name = arg

			break
		}
	}

	return name, strings.Join(rest, " "), true
}

func (a *artisanFacade) Register(commands []console.Command) {
	wrapped := make([]console.Command, len(commands))
	for i, command := range commands {
		wrapped[i] = &tracedCommand{Command: command, flush: a.flush, run: a.run}
	}

	a.Artisan.Register(wrapped)
}

type tracedCommand struct {
	console.Command
	flush func(context.Context)
	run   *runState
}

func (c *tracedCommand) Handle(ctx console.Context) (err error) {
	// The artisan run that asked for this command already has the execution:
	// join it, so the command's work is linked and there is one record.
	if runCtx, joined := c.run.current(c.Signature()); joined {
		return c.Command.Handle(&tracedConsoleContext{Context: ctx, traced: runCtx}) //nolint:wrapcheck // passthrough
	}

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
