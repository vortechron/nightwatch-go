package goravel

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/mail"

	nightwatch "github.com/vortechron/nightwatch-go"
)

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

func TestArtisan_CommandsAreRecordedAndFlushed(t *testing.T) {
	out := setup(t)
	inner := &fakeArtisan{}
	var flushes atomic.Int32
	WrapArtisan(inner, func(context.Context) { flushes.Add(1) }).Register([]console.Command{&fakeCommand{}, &fakeCommand{err: errors.New("fail")}})
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
	if flushes.Load() != 2 {
		t.Fatalf("flushes: %d", flushes.Load())
	}
}

func TestArtisan_PanicIsRecordedFlushedAndRethrown(t *testing.T) {
	out := setup(t)
	inner := &fakeArtisan{}
	var flushes atomic.Int32
	WrapArtisan(inner, func(context.Context) { flushes.Add(1) }).Register([]console.Command{&panicCommand{}})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("must rethrow")
			}
		}()
		_ = inner.registered[0].Handle(fakeConsoleCtx{ctx: context.Background()})
	}()
	if out.ofType("command")[0]["exit_code"] != int64(1) || flushes.Load() != 1 {
		t.Fatal("panic handling")
	}
	nilFlush := &tracedCommand{Command: &fakeCommand{}}
	nilFlush.flushNow() // no flush function: must not panic
}

type panicCommand struct{ fakeCommand }

func (panicCommand) Handle(console.Context) error { panic("boom") }

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

type fakeMail struct {
	mail.Mail
	sent int
	fail error
}

func (f *fakeMail) To(_ []string) mail.Mail             { return f }
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
	if err := Mail(ctx, inner, "ses").Queue(); err == nil {
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
	if first["to"] != int64(2) || first["cc"] != int64(1) || first["attachments"] != int64(1) || first["mailer"] != "smtp" {
		t.Fatalf("first mail: %v", first)
	}
	if byClass["goravel.fakeMailableWelcome"]["bcc"] != int64(1) {
		t.Fatalf("mailable: %v", byClass)
	}
	if byClass["mail"]["mailer"] != "ses" {
		t.Fatalf("custom mailer: %v", byClass["mail"])
	}
	if m.(*tracedMail).to != 0 {
		t.Fatal("wrapper must not keep recipients between mails")
	}
}

// runArtisan stands in for Goravel's console application: Run executes the
// registered commands by name, like the real one, and never exits.
type runArtisan struct {
	console.Artisan
	registered []console.Command
	runs       [][]string
	exitFlags  []bool
	err        error
	panics     bool
	inside     func() // runs while "the command" executes
}

func (r *runArtisan) Register(c []console.Command) { r.registered = append(r.registered, c...) }

func (r *runArtisan) Run(args []string, exit bool) error {
	r.runs = append(r.runs, args)
	r.exitFlags = append(r.exitFlags, exit)

	if r.panics {
		panic("boom")
	}

	if r.inside != nil {
		r.inside()
	}

	return r.err
}

type exitRecorder struct{ codes []int }

func (e *exitRecorder) exit(code int) { e.codes = append(e.codes, code) }

func wrapRun(inner *runArtisan) (*artisanFacade, *exitRecorder, *atomic.Int32) {
	flushes := &atomic.Int32{}
	facade := WrapArtisan(inner, func(context.Context) { flushes.Add(1) }).(*artisanFacade)
	exits := &exitRecorder{}
	facade.exit = exits.exit

	return facade, exits, flushes
}

func TestArtisanRun_EveryCommandRunIsRecordedEvenBuiltInOnes(t *testing.T) {
	out := setup(t)
	inner := &runArtisan{}
	facade, exits, flushes := wrapRun(inner)

	// schedule:list is registered by the framework before this client wraps
	// anything, so the per-command wrapper never sees it. Run does.
	if err := facade.Run([]string{"./app", "artisan", "schedule:list", "--no-ansi"}, true); err != nil {
		t.Fatal(err)
	}

	cmds := out.ofType("command")
	if len(cmds) != 1 || cmds[0]["name"] != "schedule:list" || cmds[0]["command"] != "schedule:list --no-ansi" || cmds[0]["exit_code"] != int64(0) {
		t.Fatalf("command record: %v", cmds)
	}
	if flushes.Load() != 1 || len(exits.codes) != 1 || exits.codes[0] != 0 {
		t.Fatalf("it must flush before the process exits: flushes=%d exits=%v", flushes.Load(), exits.codes)
	}
	if inner.exitFlags[0] {
		t.Fatal("the inner Run must not exit by itself: the exit comes after the flush")
	}
}

func TestArtisanRun_ExitCodesFollowGoravel(t *testing.T) {
	setup(t)
	tests := []struct {
		name     string
		err      error
		exit     bool
		wantCode []int
		wantErr  bool
	}{
		{"failure exits 1", errors.New("bad"), true, []int{1}, true},
		{"cancelled exits 0", context.Canceled, true, []int{0}, true},
		{"no exit flag returns the error", errors.New("bad"), false, nil, true},
		{"no exit flag, success", nil, false, nil, false},
		{"success exits 0", nil, true, []int{0}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facade, exits, _ := wrapRun(&runArtisan{err: tt.err})
			err := facade.Run([]string{"./app", "artisan", "x"}, tt.exit)
			if (err != nil) != tt.wantErr || len(exits.codes) != len(tt.wantCode) || (len(tt.wantCode) == 1 && exits.codes[0] != tt.wantCode[0]) {
				t.Fatalf("err=%v exits=%v", err, exits.codes)
			}
		})
	}
}

func TestArtisanRun_FailedCommandIsRecordedWithExitCode1(t *testing.T) {
	out := setup(t)
	facade, _, _ := wrapRun(&runArtisan{err: errors.New("bad")})
	_ = facade.Run([]string{"./app", "artisan", "migrate"}, false)
	if cmds := out.ofType("command"); len(cmds) != 1 || cmds[0]["exit_code"] != int64(1) {
		t.Fatalf("commands: %v", cmds)
	}
}

func TestArtisanRun_NamesAndNonArtisanRuns(t *testing.T) {
	out := setup(t)

	// a normal start of the app is not a command
	inner := &runArtisan{}
	facade, exits, flushes := wrapRun(inner)
	if err := facade.Run([]string{"./app"}, true); err != nil {
		t.Fatal(err)
	}
	if len(out.ofType("command")) != 0 || flushes.Load() != 0 || len(exits.codes) != 0 || !inner.exitFlags[0] {
		t.Fatal("a run without artisan must be passed through untouched")
	}

	// "artisan" alone lists the commands; flags before the name are skipped
	facade, _, _ = wrapRun(&runArtisan{})
	_ = facade.Run([]string{"./app", "artisan"}, false)
	_ = facade.Run([]string{"./app", "artisan", "--no-ansi", "cache:clear", "--tags=a"}, false)
	names := []any{}
	for _, c := range out.ofType("command") {
		names = append(names, c["name"])
	}
	if len(names) != 2 || names[0] != "list" || names[1] != "cache:clear" {
		t.Fatalf("names: %v", names)
	}
}

func TestArtisanRun_RegisteredCommandJoinsTheRunInsteadOfDoubling(t *testing.T) {
	out := setup(t)
	inner := &runArtisan{}
	facade, _, flushes := wrapRun(inner)
	facade.Register([]console.Command{&fakeCommand{}, &otherCommand{}})

	inner.inside = func() {
		cctx := fakeConsoleCtx{ctx: context.Background()}
		_ = inner.registered[0].Handle(cctx) // the command that was asked for: joins the run
		_ = inner.registered[1].Handle(cctx) // another command called from inside: its own record
	}
	if err := facade.Run([]string{"./app", "artisan", "emails:send"}, false); err != nil {
		t.Fatal(err)
	}

	names := map[any]int{}
	for _, c := range out.ofType("command") {
		names[c["name"]]++
	}
	if names["emails:send"] != 1 || names["other:run"] != 1 || len(names) != 2 {
		t.Fatalf("one record per command, not two for the same run: %v", names)
	}
	if flushes.Load() != 2 {
		t.Fatalf("flushes: %d (the run, and the inner command's own)", flushes.Load())
	}
}

type otherCommand struct{ fakeCommand }

func (otherCommand) Signature() string { return "other:run" }

func TestArtisanRun_PanicIsRecordedFlushedAndRethrown(t *testing.T) {
	out := setup(t)
	facade, _, flushes := wrapRun(&runArtisan{panics: true})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("must rethrow")
			}
		}()
		_ = facade.Run([]string{"./app", "artisan", "x"}, true)
	}()
	if cmds := out.ofType("command"); len(cmds) != 1 || cmds[0]["exit_code"] != int64(1) || flushes.Load() != 1 {
		t.Fatalf("panic handling: %v flushes=%d", cmds, flushes.Load())
	}
}

func TestPanicError_NeverPrintsTheValue(t *testing.T) {
	secret := struct {
		Token   string
		Request *struct{ Body string }
	}{Token: "s3cret-token", Request: &struct{ Body string }{Body: "password=hunter2"}}

	got := panicError(secret).Error()
	if strings.Contains(got, "s3cret") || strings.Contains(got, "hunter2") || !strings.HasPrefix(got, "panic: struct") {
		t.Fatalf("a struct panic must be reported by type only: %q", got)
	}
	if panicError("plain text").Error() != "panic: plain text" {
		t.Fatal("strings are kept")
	}
	original := errors.New("boom")
	if !errors.Is(panicError(original), original) {
		t.Fatal("errors are kept")
	}
}
