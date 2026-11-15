package nightwatch

import (
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const stageAction = "action"

// childHead is the head of a record that belongs to an execution.
func (t *Translator) childHead(typ string, at time.Time, group string, x execInfo) Record {
	record := t.headRecord(typ, at, group, x.traceID)
	record["execution_source"] = x.source
	record["execution_id"] = x.id
	record["execution_preview"] = trimPreview(x.preview)
	record["execution_stage"] = stageFor(x)
	record["user"] = x.user

	return record
}

func stageFor(x execInfo) string {
	if x.source == "" {
		return ""
	}

	return stageAction
}

// spanRecord turns a child span into a record, or reports false when the span
// is plain internal work that Nightwatch has no record for.
func (t *Translator) spanRecord(span sdktrace.ReadOnlySpan, x execInfo) (Record, bool) {
	attrs := newAttrSet(span.Attributes())

	switch attrs.str(AttrRecord) {
	case RecordQueuedJob:
		return t.queuedJobRecord(span, attrs, x)
	case RecordCacheEvent:
		return t.cacheEventRecord(span, attrs, x)
	case RecordMail:
		return t.mailRecord(span, attrs, x)
	case RecordNotification:
		return t.notificationRecord(span, attrs, x)
	}

	if span.SpanKind() != trace.SpanKindClient {
		return nil, false
	}

	switch {
	case attrs.has(attrDBQuery):
		return t.queryRecord(span, attrs, x)
	case attrs.has(attrHTTPMethod):
		return t.outgoingRecord(span, attrs, x)
	default:
		return nil, false
	}
}

var driverNames = map[string]string{
	"postgresql":           "pgsql",
	"microsoft.sql_server": "sqlsrv",
}

func laravelDriver(system string) string {
	if name, ok := driverNames[system]; ok {
		return name
	}

	return system
}

func connectionType(attrs attrSet) string {
	if explicit := attrs.str(AttrQueryConnTyp); explicit != "" {
		return explicit
	}

	switch attrs.str(attrDBPoolState) {
	case "source":
		return "write"
	case "replica":
		return "read"
	default:
		return ""
	}
}

// queryRecord builds a query record, or reports false when a RejectQueries
// callback refused it. Like Laravel, the group is hashed from the SQL after
// the redaction callbacks ran.
func (t *Translator) queryRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) (Record, bool) {
	query := Query{
		SQL:            attrs.str(attrDBQuery),
		File:           attrs.str(AttrQueryFile),
		Line:           int(attrs.int(AttrQueryLine)),
		Duration:       spanDuration(span),
		Connection:     attrs.str(attrDBPoolName, "db.namespace"),
		ConnectionType: connectionType(attrs),
	}

	hooks := t.load()

	redacted, keep := filtered(hooks, hooks.rejectQueries, hooks.redactQueries, query)
	if !keep {
		return nil, false
	}

	query.SQL = redacted.SQL

	record := t.childHead(RecordQuery, span.StartTime(), QueryGroup(laravelDriver(attrs.str(attrDBSystem)), query.Connection, query.SQL), x)

	record["sql"] = MediumText(SanitizeUTF8(query.SQL))
	record["file"] = TinyText(query.File)
	record["line"] = query.Line
	record["duration"] = micros(query.Duration)
	record["connection"] = TinyText(query.Connection)
	record["connection_type"] = query.ConnectionType

	return record, true
}

func (t *Translator) outgoingRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) (Record, bool) {
	request := OutgoingRequest{
		Method:       attrs.str(attrHTTPMethod),
		URL:          RedactURL(attrs.str(attrURLFull), t.cfg.RedactQuery),
		Duration:     spanDuration(span),
		RequestSize:  attrs.int(attrHTTPReqSize),
		ResponseSize: attrs.int(attrHTTPRespSize),
		StatusCode:   int(attrs.int(attrHTTPStatus)),
	}

	hooks := t.load()

	redacted, keep := filtered(hooks, hooks.rejectOutgoingRequests, hooks.redactOutgoingRequests, request)
	if !keep {
		return nil, false
	}

	host := attrs.str(attrServerAddress)
	record := t.childHead(RecordOutgoingRequest, span.StartTime(), NameGroup(host), x)

	record["host"] = TinyText(host)
	record["method"] = request.Method
	record["url"] = TinyText(RedactURL(redacted.URL, t.cfg.RedactQuery))
	record["duration"] = micros(request.Duration)
	record["request_size"] = request.RequestSize
	record["response_size"] = request.ResponseSize
	record["status_code"] = int64(request.StatusCode)

	return record, true
}

func (t *Translator) queuedJobRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) (Record, bool) {
	job := QueuedJob{
		JobID:      attrs.str(AttrJobID),
		Name:       attrs.str(AttrJobName),
		Connection: attrs.str(AttrJobConnection),
		Queue:      attrs.str(AttrJobQueue),
		Duration:   spanDuration(span),
	}

	hooks := t.load()
	if _, keep := filtered(hooks, hooks.rejectQueuedJobs, nil, job); !keep {
		return nil, false
	}

	record := t.childHead(RecordQueuedJob, span.StartTime(), NameGroup(job.Name), x)

	record["job_id"] = job.JobID
	record["name"] = job.Name
	record["connection"] = job.Connection
	record["queue"] = job.Queue
	record["duration"] = micros(job.Duration)

	return record, true
}

var cacheTypes = []string{"hit", "miss", "write", "write-failure", "delete", "delete-failure"}

func (t *Translator) cacheEventRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) (Record, bool) {
	kind := enumOr(attrs.str(AttrCacheType), "", cacheTypes...)
	if kind == "" {
		return nil, false
	}

	event := CacheEvent{
		Store:    attrs.str(AttrCacheStore),
		Key:      attrs.str(AttrCacheKey),
		Type:     kind,
		Duration: spanDuration(span),
		TTL:      time.Duration(max(attrs.int(AttrCacheTTL), 0)) * time.Second,
	}

	hooks := t.load()
	if hooks.cacheKeyRejected(event.Key) {
		return nil, false
	}

	redacted, keep := filtered(hooks, hooks.rejectCacheEvents, hooks.redactCacheEvents, event)
	if !keep {
		return nil, false
	}

	event.Key = redacted.Key
	record := t.childHead(RecordCacheEvent, span.StartTime(), CacheGroup(event.Store, event.Key), x)

	record["store"] = event.Store
	record["key"] = TinyText(event.Key)
	record["type"] = event.Type
	record["duration"] = micros(event.Duration)
	record["ttl"] = int64(event.TTL / time.Second)

	return record, true
}

func (t *Translator) mailRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) (Record, bool) {
	mail := Mail{
		Mailer:      attrs.str(AttrMailMailer),
		Class:       attrs.str(AttrMailClass),
		Subject:     attrs.str(AttrMailSubject),
		To:          int(attrs.int(AttrMailTo)),
		Cc:          int(attrs.int(AttrMailCc)),
		Bcc:         int(attrs.int(AttrMailBcc)),
		Attachments: int(attrs.int(AttrMailAttachments)),
		Duration:    spanDuration(span),
		Failed:      spanFailed(span),
	}

	hooks := t.load()

	redacted, keep := filtered(hooks, hooks.rejectMail, hooks.redactMail, mail)
	if !keep {
		return nil, false
	}

	record := t.childHead(RecordMail, span.StartTime(), NameGroup(mail.Class), x)

	record["mailer"] = mail.Mailer
	record["class"] = mail.Class
	record["subject"] = TinyText(redacted.Subject)
	record["to"] = int64(mail.To)
	record["cc"] = int64(mail.Cc)
	record["bcc"] = int64(mail.Bcc)
	record["attachments"] = int64(mail.Attachments)
	record["duration"] = micros(mail.Duration)
	record["failed"] = mail.Failed

	return record, true
}

func (t *Translator) notificationRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) (Record, bool) {
	note := Notification{
		Channel:  attrs.str(AttrNotificationChan),
		Class:    attrs.str(AttrNotificationClass),
		Duration: spanDuration(span),
	}

	hooks := t.load()
	if _, keep := filtered(hooks, hooks.rejectNotifications, nil, note); !keep {
		return nil, false
	}

	record := t.childHead(RecordNotification, span.StartTime(), NameGroup(note.Class), x)

	record["channel"] = note.Channel
	record["class"] = note.Class
	record["duration"] = micros(note.Duration)
	record["failed"] = spanFailed(span)

	return record, true
}
