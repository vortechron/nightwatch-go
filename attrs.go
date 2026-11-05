package nightwatch

// Span attribute names the translator reads. Standard OpenTelemetry names are
// used where they exist. Everything Nightwatch-only lives under "nightwatch.".
const (
	// Standard OpenTelemetry semantic conventions.
	attrHTTPMethod     = "http.request.method"
	attrHTTPRoute      = "http.route"
	attrHTTPStatus     = "http.response.status_code"
	attrHTTPReqSize    = "http.request.body.size"
	attrHTTPRespSize   = "http.response.body.size"
	attrURLFull        = "url.full"
	attrURLPath        = "url.path"
	attrURLQuery       = "url.query"
	attrURLScheme      = "url.scheme"
	attrServerAddress  = "server.address"
	attrServerPort     = "server.port"
	attrClientAddress  = "client.address"
	attrDBSystem       = "db.system.name"
	attrDBQuery        = "db.query.text"
	attrDBOperation    = "db.operation.name"
	attrDBPoolName     = "db.client.connection.pool.name"
	attrDBPoolState    = "db.client.connection.pool.state"
	attrEnduserID      = "enduser.id"
	attrExceptionType  = "exception.type"
	attrExceptionMsg   = "exception.message"
	attrExceptionStack = "exception.stacktrace"
	eventException     = "exception"
)

// Nightwatch-only attributes. Instrumentation helpers set them; you can also
// set them by hand on your own spans.
const (
	// AttrRecord marks a span as a Nightwatch record: one of the Record*
	// constants (queued-job, job-attempt, command, scheduled-task,
	// cache-event, mail, notification).
	AttrRecord = "nightwatch.record"

	AttrRouteName    = "nightwatch.route.name"
	AttrRouteAction  = "nightwatch.route.action"
	AttrHeaders      = "nightwatch.request.headers"
	AttrPayload      = "nightwatch.request.payload"
	AttrPeakMemory   = "nightwatch.peak_memory"
	AttrStageBefore  = "nightwatch.stage.before_middleware"
	AttrStageAfter   = "nightwatch.stage.after_middleware"
	AttrQueryFile    = "nightwatch.query.file"
	AttrQueryLine    = "nightwatch.query.line"
	AttrQueryConnTyp = "nightwatch.query.connection_type"
	AttrUserName     = "nightwatch.user.name"
	AttrUserUsername = "nightwatch.user.username"

	AttrExceptionClass   = "nightwatch.exception.class"
	AttrExceptionHandled = "nightwatch.exception.handled"
	AttrExceptionCode    = "nightwatch.exception.code"

	AttrJobID         = "nightwatch.job.id"
	AttrJobName       = "nightwatch.job.name"
	AttrJobConnection = "nightwatch.job.connection"
	AttrJobQueue      = "nightwatch.job.queue"
	AttrJobAttempt    = "nightwatch.job.attempt"
	AttrJobStatus     = "nightwatch.job.status"

	AttrCommandName     = "nightwatch.command.name"
	AttrCommandClass    = "nightwatch.command.class"
	AttrCommandLine     = "nightwatch.command.line"
	AttrCommandExitCode = "nightwatch.command.exit_code"

	AttrTaskName          = "nightwatch.schedule.name"
	AttrTaskCron          = "nightwatch.schedule.cron"
	AttrTaskTimezone      = "nightwatch.schedule.timezone"
	AttrTaskRepeat        = "nightwatch.schedule.repeat_seconds"
	AttrTaskOverlap       = "nightwatch.schedule.without_overlapping"
	AttrTaskOneServer     = "nightwatch.schedule.on_one_server"
	AttrTaskBackground    = "nightwatch.schedule.run_in_background"
	AttrTaskMaintenance   = "nightwatch.schedule.even_in_maintenance_mode"
	AttrTaskStatus        = "nightwatch.schedule.status"
	AttrCacheStore        = "nightwatch.cache.store"
	AttrCacheKey          = "nightwatch.cache.key"
	AttrCacheType         = "nightwatch.cache.type"
	AttrCacheTTL          = "nightwatch.cache.ttl"
	AttrMailMailer        = "nightwatch.mail.mailer"
	AttrMailClass         = "nightwatch.mail.class"
	AttrMailSubject       = "nightwatch.mail.subject"
	AttrMailTo            = "nightwatch.mail.to"
	AttrMailCc            = "nightwatch.mail.cc"
	AttrMailBcc           = "nightwatch.mail.bcc"
	AttrMailAttachments   = "nightwatch.mail.attachments"
	AttrNotificationChan  = "nightwatch.notification.channel"
	AttrNotificationClass = "nightwatch.notification.class"
)

// Record types, as Omni names them in the "t" field.
const (
	RecordRequest         = "request"
	RecordQuery           = "query"
	RecordException       = "exception"
	RecordLog             = "log"
	RecordCommand         = "command"
	RecordQueuedJob       = "queued-job"
	RecordJobAttempt      = "job-attempt"
	RecordCacheEvent      = "cache-event"
	RecordScheduledTask   = "scheduled-task"
	RecordMail            = "mail"
	RecordNotification    = "notification"
	RecordOutgoingRequest = "outgoing-request"
	RecordUser            = "user"
)

// Execution sources, as Omni names them in "execution_source".
const (
	sourceRequest  = "request"
	sourceCommand  = "command"
	sourceJob      = "job"
	sourceSchedule = "schedule"
)
