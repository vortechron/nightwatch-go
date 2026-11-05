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
		return t.queuedJobRecord(span, attrs, x), true
	case RecordCacheEvent:
		return t.cacheEventRecord(span, attrs, x)
	case RecordMail:
		return t.mailRecord(span, attrs, x), true
	case RecordNotification:
		return t.notificationRecord(span, attrs, x), true
	}

	if span.SpanKind() != trace.SpanKindClient {
		return nil, false
	}

	switch {
	case attrs.has(attrDBQuery):
		return t.queryRecord(span, attrs, x), true
	case attrs.has(attrHTTPMethod):
		return t.outgoingRecord(span, attrs, x), true
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

func (t *Translator) queryRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) Record {
	sql := attrs.str(attrDBQuery)
	connection := attrs.str(attrDBPoolName, "db.namespace")
	record := t.childHead(RecordQuery, span.StartTime(), QueryGroup(laravelDriver(attrs.str(attrDBSystem)), connection, sql), x)

	record["sql"] = MediumText(SanitizeUTF8(sql))
	record["file"] = TinyText(attrs.str(AttrQueryFile))
	record["line"] = attrs.int(AttrQueryLine)
	record["duration"] = micros(spanDuration(span))
	record["connection"] = TinyText(connection)
	record["connection_type"] = connectionType(attrs)

	return record
}

func (t *Translator) outgoingRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) Record {
	host := attrs.str(attrServerAddress)
	record := t.childHead(RecordOutgoingRequest, span.StartTime(), NameGroup(host), x)

	record["host"] = TinyText(host)
	record["method"] = attrs.str(attrHTTPMethod)
	record["url"] = TinyText(attrs.str(attrURLFull))
	record["duration"] = micros(spanDuration(span))
	record["request_size"] = attrs.int(attrHTTPReqSize)
	record["response_size"] = attrs.int(attrHTTPRespSize)
	record["status_code"] = attrs.int(attrHTTPStatus)

	return record
}

func (t *Translator) queuedJobRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) Record {
	name := attrs.str(AttrJobName)
	record := t.childHead(RecordQueuedJob, span.StartTime(), NameGroup(name), x)

	record["job_id"] = attrs.str(AttrJobID)
	record["name"] = name
	record["connection"] = attrs.str(AttrJobConnection)
	record["queue"] = attrs.str(AttrJobQueue)
	record["duration"] = micros(spanDuration(span))

	return record
}

var cacheTypes = []string{"hit", "miss", "write", "write-failure", "delete", "delete-failure"}

func (t *Translator) cacheEventRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) (Record, bool) {
	kind := enumOr(attrs.str(AttrCacheType), "", cacheTypes...)
	if kind == "" {
		return nil, false
	}

	store, key := attrs.str(AttrCacheStore), attrs.str(AttrCacheKey)
	record := t.childHead(RecordCacheEvent, span.StartTime(), CacheGroup(store, key), x)

	record["store"] = store
	record["key"] = TinyText(key)
	record["type"] = kind
	record["duration"] = micros(spanDuration(span))
	record["ttl"] = max(attrs.int(AttrCacheTTL), 0)

	return record, true
}

func (t *Translator) mailRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) Record {
	class := attrs.str(AttrMailClass)
	record := t.childHead(RecordMail, span.StartTime(), NameGroup(class), x)

	record["mailer"] = attrs.str(AttrMailMailer)
	record["class"] = class
	record["subject"] = TinyText(attrs.str(AttrMailSubject))
	record["to"] = attrs.int(AttrMailTo)
	record["cc"] = attrs.int(AttrMailCc)
	record["bcc"] = attrs.int(AttrMailBcc)
	record["attachments"] = attrs.int(AttrMailAttachments)
	record["duration"] = micros(spanDuration(span))
	record["failed"] = spanFailed(span)

	return record
}

func (t *Translator) notificationRecord(span sdktrace.ReadOnlySpan, attrs attrSet, x execInfo) Record {
	class := attrs.str(AttrNotificationClass)
	record := t.childHead(RecordNotification, span.StartTime(), NameGroup(class), x)

	record["channel"] = attrs.str(AttrNotificationChan)
	record["class"] = class
	record["duration"] = micros(spanDuration(span))
	record["failed"] = spanFailed(span)

	return record
}
