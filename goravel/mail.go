package goravel

import (
	"context"
	"fmt"

	"github.com/goravel/framework/contracts/mail"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// Mail wraps a mailer so Send and Queue become mail records of the execution
// in ctx. Goravel's mail API carries no context, so wrap per request:
//
//	nwgoravel.Mail(ctx, facades.Mail()).To([]string{"a@b.c"}).Send(m)
//
// The mailer name shown in Omni is "smtp", the only transport Goravel's mailer
// has (its mail config has no mailer name). Pass a name to override it.
func Mail(ctx context.Context, inner mail.Mail, mailer ...string) mail.Mail {
	if !nightwatch.Active(ctx) {
		return inner
	}

	name := defaultMailer
	if len(mailer) > 0 && mailer[0] != "" {
		name = mailer[0]
	}

	return &tracedMail{inner: inner, ctx: ctx, mailer: name}
}

const defaultMailer = "smtp"

// tracedMail is a value-like builder: every method returns a copy, so a
// wrapper can be reused without recipients leaking between mails.
type tracedMail struct {
	inner       mail.Mail
	mailer      string
	ctx         context.Context //nolint:containedctx // bound to one execution on purpose
	to, cc, bcc int
	attachments int
	subject     string
}

func (m *tracedMail) with(inner mail.Mail, change func(*tracedMail)) mail.Mail {
	next := *m
	next.inner = inner
	change(&next)

	return &next
}

func (m *tracedMail) To(addresses []string) mail.Mail {
	return m.with(m.inner.To(addresses), func(n *tracedMail) { n.to += len(addresses) })
}

func (m *tracedMail) Cc(addresses []string) mail.Mail {
	return m.with(m.inner.Cc(addresses), func(n *tracedMail) { n.cc += len(addresses) })
}

func (m *tracedMail) Bcc(addresses []string) mail.Mail {
	return m.with(m.inner.Bcc(addresses), func(n *tracedMail) { n.bcc += len(addresses) })
}

func (m *tracedMail) Attach(files []string) mail.Mail {
	return m.with(m.inner.Attach(files), func(n *tracedMail) { n.attachments += len(files) })
}

func (m *tracedMail) Subject(subject string) mail.Mail {
	return m.with(m.inner.Subject(subject), func(n *tracedMail) { n.subject = subject })
}

func (m *tracedMail) Content(content mail.Content) mail.Mail {
	return m.with(m.inner.Content(content), func(*tracedMail) {})
}

func (m *tracedMail) From(address mail.Address) mail.Mail {
	return m.with(m.inner.From(address), func(*tracedMail) {})
}

func (m *tracedMail) Headers(headers map[string]string) mail.Mail {
	return m.with(m.inner.Headers(headers), func(*tracedMail) {})
}

func (m *tracedMail) Send(mailables ...mail.Mailable) error {
	_, end := nightwatch.StartMail(m.ctx, m.info(mailables))
	err := m.inner.Send(mailables...)
	end(err)

	return err //nolint:wrapcheck // passthrough of the mail error
}

func (m *tracedMail) Queue(mailables ...mail.Mailable) error {
	_, end := nightwatch.StartMail(m.ctx, m.info(mailables))
	err := m.inner.Queue(mailables...)
	end(err)

	return err //nolint:wrapcheck // passthrough of the mail error
}

// info describes the mail. A Mailable adds its own recipients and subject.
func (m *tracedMail) info(mailables []mail.Mailable) nightwatch.MailInfo {
	info := nightwatch.MailInfo{
		Mailer: m.mailer, Class: "mail", Subject: m.subject,
		To: m.to, Cc: m.cc, Bcc: m.bcc, Attachments: m.attachments,
	}

	for _, mailable := range mailables {
		info.Class = fmt.Sprintf("%T", mailable)
		info.Attachments += len(mailable.Attachments())

		if envelope := mailable.Envelope(); envelope != nil {
			info.Subject = envelope.Subject
			info.To += len(envelope.To)
			info.Cc += len(envelope.Cc)
			info.Bcc += len(envelope.Bcc)
		}
	}

	return info
}
