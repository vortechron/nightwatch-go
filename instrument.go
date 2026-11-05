package nightwatch

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationScope = "github.com/vortechron/nightwatch-go"

func tracer() trace.Tracer {
	return otel.GetTracerProvider().Tracer(instrumentationScope)
}

// EndFunc ends a span. A non-nil error marks the span failed and records it
// as an exception.
type EndFunc func(err error)

func finish(span trace.Span, err error) {
	if err != nil {
		RecordExceptionOnSpan(span, err, false)
		span.SetStatus(codes.Error, err.Error())
	}

	span.End()
}

// ReportError reports an unhandled error on the current span, with the Go
// stack of the call site. Use it where the application fails.
func ReportError(ctx context.Context, err error) {
	RecordExceptionOnSpan(trace.SpanFromContext(ctx), err, false)
}

// ReportHandled reports an error the application caught on purpose (the
// "handled" flag in Omni).
func ReportHandled(ctx context.Context, err error) {
	RecordExceptionOnSpan(trace.SpanFromContext(ctx), err, true)
}

// RecordExceptionOnSpan adds an "exception" event with a stack trace.
func RecordExceptionOnSpan(span trace.Span, err error, handled bool) {
	if err == nil || !span.IsRecording() {
		return
	}

	span.AddEvent(eventException, trace.WithAttributes(
		attribute.String(attrExceptionType, exceptionClass(err)),
		attribute.String(attrExceptionMsg, err.Error()),
		attribute.String(attrExceptionStack, string(debug.Stack())),
		attribute.Bool(AttrExceptionHandled, handled),
	))
}

// exceptionClass names the error type. Wrapping added by fmt.Errorf("%w") is
// removed so the class names the error that was wrapped.
func exceptionClass(err error) string {
	for {
		if !strings.HasPrefix(fmt.Sprintf("%T", err), "*fmt.wrapError") {
			return fmt.Sprintf("%T", err)
		}

		inner := errors.Unwrap(err)
		if inner == nil {
			return fmt.Sprintf("%T", err)
		}

		err = inner
	}
}

// User identifies the signed-in user of an execution.
type User struct {
	ID       string
	Name     string
	Username string
}

// SetUser stores the signed-in user on the current span. Omni shows the id on
// every record of the execution, and the name on the user page.
func SetUser(ctx context.Context, user User) {
	span := trace.SpanFromContext(ctx)
	if user.ID == "" || !span.IsRecording() {
		return
	}

	span.SetAttributes(
		attribute.String(attrEnduserID, user.ID),
		attribute.String(AttrUserName, user.Name),
		attribute.String(AttrUserUsername, user.Username),
	)
}

// JobInfo describes a queued job.
type JobInfo struct {
	Name       string
	Connection string
	Queue      string
}

// JobEnvelope is what travels with a queued job so the worker continues the
// trace of the code that queued it.
type JobEnvelope struct {
	TraceParent string
	JobID       string
	Connection  string
	Queue       string
}

const envelopePrefix = "nwtp1|"

// StartJobDispatch records a queued-job at the moment the job is pushed. Call
// EncodeJobEnvelope with the returned context and ship the result with the job.
func StartJobDispatch(ctx context.Context, job JobInfo) (context.Context, EndFunc) {
	jobID := uuid.NewString()

	ctx, span := tracer().Start(ctx, "queue.dispatch "+job.Name,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String(AttrRecord, RecordQueuedJob),
			attribute.String(AttrJobID, jobID),
			attribute.String(AttrJobName, job.Name),
			attribute.String(AttrJobConnection, job.Connection),
			attribute.String(AttrJobQueue, job.Queue),
		))

	return context.WithValue(ctx, jobIDKey{}, jobID), func(err error) { finish(span, err) }
}

type jobIDKey struct{}

// EncodeJobEnvelope returns "nwtp1|<traceparent>|<job id>|<connection>|<queue>"
// for the span in ctx, or "" when there is none.
func EncodeJobEnvelope(ctx context.Context, connection, queue string) string {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return ""
	}

	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)

	jobID, _ := ctx.Value(jobIDKey{}).(string)

	return envelopePrefix + strings.Join([]string{carrier.Get("traceparent"), jobID, connection, queue}, "|")
}

// DecodeJobEnvelope parses EncodeJobEnvelope output.
func DecodeJobEnvelope(value string) (JobEnvelope, bool) {
	rest, found := strings.CutPrefix(value, envelopePrefix)
	if !found {
		return JobEnvelope{}, false
	}

	parts := strings.SplitN(rest, "|", 4)
	if len(parts) != 4 || parts[0] == "" {
		return JobEnvelope{}, false
	}

	return JobEnvelope{TraceParent: parts[0], JobID: parts[1], Connection: parts[2], Queue: parts[3]}, true
}

// IsJobEnvelope reports whether value looks like an envelope.
func IsJobEnvelope(value string) bool {
	return strings.HasPrefix(value, envelopePrefix)
}

// StartJobAttempt starts the root span of one run of a job. The envelope may
// be empty (a job queued outside any trace): the attempt then starts its own
// trace.
func StartJobAttempt(ctx context.Context, env JobEnvelope, job JobInfo, attempt int) (context.Context, EndFunc) {
	if env.TraceParent != "" {
		ctx = propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": env.TraceParent})
	}

	jobID := env.JobID
	if jobID == "" {
		jobID = uuid.NewString()
	}

	ctx, span := tracer().Start(ctx, "queue.process "+job.Name,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String(AttrRecord, RecordJobAttempt),
			attribute.String(AttrJobID, jobID),
			attribute.String(AttrJobName, job.Name),
			attribute.String(AttrJobConnection, job.Connection),
			attribute.String(AttrJobQueue, job.Queue),
			attribute.Int(AttrJobAttempt, attempt),
		))

	return ctx, func(err error) { finish(span, err) }
}

// CommandInfo describes a console command run.
type CommandInfo struct {
	Name  string
	Class string
	Line  string
}

// CommandEndFunc ends a command span with its exit code.
type CommandEndFunc func(exitCode int, err error)

// StartCommand starts the root span of a console command.
func StartCommand(ctx context.Context, info CommandInfo) (context.Context, CommandEndFunc) {
	ctx, span := tracer().Start(ctx, "command "+info.Name, trace.WithNewRoot(), trace.WithAttributes(
		attribute.String(AttrRecord, RecordCommand),
		attribute.String(AttrCommandName, info.Name),
		attribute.String(AttrCommandClass, info.Class),
		attribute.String(AttrCommandLine, info.Line),
	))

	return ctx, func(exitCode int, err error) {
		span.SetAttributes(attribute.Int(AttrCommandExitCode, exitCode))
		finish(span, err)
	}
}

// Scheduled task statuses.
const (
	TaskProcessed = "processed"
	TaskFailed    = "failed"
	TaskSkipped   = "skipped"
)

// TaskInfo describes a scheduled task.
type TaskInfo struct {
	Name                  string
	Cron                  string
	Timezone              string
	RepeatSeconds         int
	WithoutOverlapping    bool
	OnOneServer           bool
	RunInBackground       bool
	EvenInMaintenanceMode bool
}

// TaskEndFunc ends a scheduled task span with its status.
type TaskEndFunc func(status string, err error)

// StartScheduledTask starts the root span of a scheduled task run.
func StartScheduledTask(ctx context.Context, info TaskInfo) (context.Context, TaskEndFunc) {
	ctx, span := tracer().Start(ctx, "schedule "+info.Name, trace.WithNewRoot(), trace.WithAttributes(
		attribute.String(AttrRecord, RecordScheduledTask),
		attribute.String(AttrTaskName, info.Name),
		attribute.String(AttrTaskCron, info.Cron),
		attribute.String(AttrTaskTimezone, info.Timezone),
		attribute.Int(AttrTaskRepeat, info.RepeatSeconds),
		attribute.Bool(AttrTaskOverlap, info.WithoutOverlapping),
		attribute.Bool(AttrTaskOneServer, info.OnOneServer),
		attribute.Bool(AttrTaskBackground, info.RunInBackground),
		attribute.Bool(AttrTaskMaintenance, info.EvenInMaintenanceMode),
	))

	return ctx, func(status string, err error) {
		span.SetAttributes(attribute.String(AttrTaskStatus, status))
		finish(span, err)
	}
}

// Cache event types.
const (
	CacheHit           = "hit"
	CacheMiss          = "miss"
	CacheWrite         = "write"
	CacheWriteFailure  = "write-failure"
	CacheDelete        = "delete"
	CacheDeleteFailure = "delete-failure"
)

// CacheEndFunc ends a cache span with the event type and time to live.
type CacheEndFunc func(eventType string, ttl time.Duration, err error)

// StartCache times one cache call.
func StartCache(ctx context.Context, store, key string) (context.Context, CacheEndFunc) {
	ctx, span := tracer().Start(ctx, "cache "+key, trace.WithAttributes(
		attribute.String(AttrRecord, RecordCacheEvent),
		attribute.String(AttrCacheStore, store),
		attribute.String(AttrCacheKey, key),
	))

	return ctx, func(eventType string, ttl time.Duration, err error) {
		span.SetAttributes(
			attribute.String(AttrCacheType, eventType),
			attribute.Int64(AttrCacheTTL, int64(ttl.Seconds())),
		)
		finish(span, err)
	}
}

// MailInfo describes a sent mail. To, Cc, Bcc and Attachments are counts.
type MailInfo struct {
	Mailer      string
	Class       string
	Subject     string
	To          int
	Cc          int
	Bcc         int
	Attachments int
}

// StartMail times sending one mail.
func StartMail(ctx context.Context, info MailInfo) (context.Context, EndFunc) {
	ctx, span := tracer().Start(ctx, "mail "+info.Class, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String(AttrRecord, RecordMail),
		attribute.String(AttrMailMailer, info.Mailer),
		attribute.String(AttrMailClass, info.Class),
		attribute.String(AttrMailSubject, info.Subject),
		attribute.Int(AttrMailTo, info.To),
		attribute.Int(AttrMailCc, info.Cc),
		attribute.Int(AttrMailBcc, info.Bcc),
		attribute.Int(AttrMailAttachments, info.Attachments),
	))

	return ctx, func(err error) { finish(span, err) }
}

// NotificationInfo describes a sent notification.
type NotificationInfo struct {
	Channel string
	Class   string
}

// StartNotification times sending one notification.
func StartNotification(ctx context.Context, info NotificationInfo) (context.Context, EndFunc) {
	ctx, span := tracer().Start(ctx, "notification "+info.Class, trace.WithAttributes(
		attribute.String(AttrRecord, RecordNotification),
		attribute.String(AttrNotificationChan, info.Channel),
		attribute.String(AttrNotificationClass, info.Class),
	))

	return ctx, func(err error) { finish(span, err) }
}
