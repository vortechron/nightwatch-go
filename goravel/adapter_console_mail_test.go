package goravel

import (
	"context"
	"errors"
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
