package jobs

import (
	"context"
	"fmt"
	"goravel/app/facades"
	"time"

	"github.com/goravel/framework/contracts/mail"
	"github.com/goravel/framework/contracts/queue"

	nwgoravel "github.com/vortechron/nightwatch-go/goravel"
)

const (
	maxAttempts = 3
	retryDelay  = 5 * time.Second
)

// SendWelcome queries the database, writes to the cache and sends a mail.
type SendWelcome struct{}

func (j *SendWelcome) Signature() string { return "send_welcome" }

// Handle is required by Goravel. HandleContext below is what runs: it has the
// context of the job attempt, so the work joins the trace of the request.
func (j *SendWelcome) Handle(args ...any) error {
	return j.HandleContext(context.Background(), args...)
}

func (j *SendWelcome) HandleContext(ctx context.Context, args ...any) error {
	if len(args) == 0 {
		return fmt.Errorf("send_welcome needs an address")
	}

	address, _ := args[0].(string)

	var rows []map[string]any
	if err := facades.Orm().WithContext(ctx).Query().Raw("select * from tasks where id = ?", 7).Scan(&rows); err != nil {
		return err
	}

	_ = facades.Cache().WithContext(ctx).Put("welcome:"+address, "sent", 0)
	facades.Log().WithContext(ctx).Info("sending welcome mail")

	return nwgoravel.Mail(ctx, facades.Mail()).To([]string{address}).Subject("Welcome").
		Content(mail.Content{Html: "<p>Welcome</p>"}).Send()
}

// ShouldRetry stops after three attempts and waits between them. Without it
// Goravel retries a failing job at once and without limit, and every retry
// opens a new SMTP connection.
func (j *SendWelcome) ShouldRetry(_ error, attempt int) (bool, time.Duration) {
	return attempt < maxAttempts, retryDelay
}

var _ nwgoravel.ContextJob = (*SendWelcome)(nil)

var _ queue.JobWithShouldRetry = (*SendWelcome)(nil)
