package nightwatch

import (
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// headRecord holds the fields every record starts with.
func (t *Translator) headRecord(typ string, at time.Time, group, traceID string) Record {
	record := Record{
		"v":         1,
		"t":         typ,
		"timestamp": unixSeconds(at),
		"deploy":    TinyText(t.cfg.Deploy),
		"server":    TinyText(t.cfg.Server),
		"trace_id":  traceID,
	}
	if group != "" {
		record["_group"] = group
	}

	return record
}

// executionCounters adds the tallies that every execution record carries.
// Fields that only exist in PHP (lazy loads, file I/O, hydrated models) are 0.
func executionCounters(record Record, c counters) {
	record["exceptions"] = c.exceptions
	record["logs"] = c.logs
	record["queries"] = c.queries
	record["lazy_loads"] = 0
	record["jobs_queued"] = c.jobsQueued
	record["mail"] = c.mail
	record["notifications"] = c.notifications
	record["outgoing_requests"] = c.outgoing
	record["files_read"] = 0
	record["files_written"] = 0
	record["cache_events"] = c.cacheHits
	record["hydrated_models"] = 0
}

func spanDuration(span sdktrace.ReadOnlySpan) time.Duration {
	return span.EndTime().Sub(span.StartTime())
}

func spanFailed(span sdktrace.ReadOnlySpan) bool {
	return span.Status().Code == codes.Error
}

// buildExecution turns a root span into its execution record. It reports
// false when a redaction callback panicked: the execution is then not sent.
func (t *Translator) buildExecution(root sdktrace.ReadOnlySpan, x execInfo, c counters, preview string) (Record, bool) {
	switch x.kind {
	case RecordJobAttempt:
		return t.jobAttemptRecord(root, x, c, preview), true
	case RecordCommand:
		return t.commandRecord(root, x, c, preview)
	case RecordScheduledTask:
		return t.scheduledTaskRecord(root, x, c, preview), true
	default:
		return t.requestRecord(root, x, c, preview)
	}
}

func (t *Translator) peakBytes(attrs attrSet) int64 {
	if value := attrs.int(AttrPeakMemory); value > 0 {
		return value
	}

	return t.mem.read(t.now())
}

// stagedDurations fills the three stages non-request executions have.
func stagedDurations(record Record, root sdktrace.ReadOnlySpan) {
	duration := micros(spanDuration(root))
	record["duration"] = duration
	record["bootstrap"] = int64(0)
	record["action"] = duration
	record["terminating"] = int64(0)
}

func (t *Translator) commandRecord(root sdktrace.ReadOnlySpan, x execInfo, c counters, preview string) (Record, bool) {
	attrs := newAttrSet(root.Attributes())

	exit := attrs.int(AttrCommandExitCode)
	if !attrs.has(AttrCommandExitCode) && spanFailed(root) {
		exit = 1
	}

	command := Command{
		Class:    attrs.str(AttrCommandClass),
		Name:     attrs.str(AttrCommandName),
		Command:  attrs.str(AttrCommandLine, AttrCommandName),
		ExitCode: int(max(min(exit, 255), 0)),
		Duration: spanDuration(root),
	}

	hooks := t.load()

	redacted, keep := filtered(hooks, nil, hooks.redactCommands, command)
	if !keep {
		return nil, false
	}

	record := t.headRecord(RecordCommand, root.StartTime(), NameGroup(command.Name), x.traceID)

	record["class"] = command.Class
	record["name"] = command.Name
	record["command"] = redacted.Command
	record["exit_code"] = int64(command.ExitCode)
	stagedDurations(record, root)
	executionCounters(record, c)
	record["peak_memory_usage"] = t.peakBytes(attrs)
	record["exception_preview"] = trimPreview(preview)
	record["context"] = "{}"

	return record, true
}

func (t *Translator) scheduledTaskRecord(root sdktrace.ReadOnlySpan, x execInfo, c counters, preview string) Record {
	attrs := newAttrSet(root.Attributes())
	name, cron, zone := attrs.str(AttrTaskName), attrs.str(AttrTaskCron), attrs.str(AttrTaskTimezone)
	repeat := int(attrs.int(AttrTaskRepeat))
	record := t.headRecord(RecordScheduledTask, root.StartTime(), ScheduledTaskGroup(name, cron, zone, repeat), x.traceID)

	record["name"] = name
	record["cron"] = cron
	record["timezone"] = zone
	record["repeat_seconds"] = repeat
	record["without_overlapping"] = attrs.bool(AttrTaskOverlap)
	record["on_one_server"] = attrs.bool(AttrTaskOneServer)
	record["run_in_background"] = attrs.bool(AttrTaskBackground)
	record["even_in_maintenance_mode"] = attrs.bool(AttrTaskMaintenance)
	record["status"] = enumOr(attrs.str(AttrTaskStatus), statusFor(root), "processed", "failed", "skipped")
	record["duration"] = micros(spanDuration(root))
	executionCounters(record, c)
	record["peak_memory_usage"] = t.peakBytes(attrs)
	record["exception_preview"] = trimPreview(preview)
	record["context"] = "{}"

	return record
}

func (t *Translator) jobAttemptRecord(root sdktrace.ReadOnlySpan, x execInfo, c counters, preview string) Record {
	attrs := newAttrSet(root.Attributes())
	name := attrs.str(AttrJobName)
	record := t.headRecord(RecordJobAttempt, root.StartTime(), NameGroup(name), x.traceID)

	attempt := int(attrs.int(AttrJobAttempt))
	if attempt < 1 {
		attempt = 1
	}

	record["user"] = x.user
	record["job_id"] = attrs.str(AttrJobID)
	record["attempt_id"] = x.id
	record["attempt"] = attempt
	record["name"] = name
	record["connection"] = attrs.str(AttrJobConnection)
	record["queue"] = attrs.str(AttrJobQueue)
	record["status"] = enumOr(attrs.str(AttrJobStatus), statusFor(root), "processed", "released", "failed")
	record["duration"] = micros(spanDuration(root))
	executionCounters(record, c)
	record["peak_memory_usage"] = t.peakBytes(attrs)
	record["exception_preview"] = trimPreview(preview)
	record["context"] = "{}"

	return record
}

// statusFor maps the OpenTelemetry span status to a Nightwatch status.
func statusFor(span sdktrace.ReadOnlySpan) string {
	if spanFailed(span) {
		return "failed"
	}

	return "processed"
}

// enumOr returns wanted when it is one of the allowed values, else fallback.
func enumOr(wanted, fallback string, allowed ...string) string {
	for _, value := range allowed {
		if wanted == value {
			return wanted
		}
	}

	return fallback
}
