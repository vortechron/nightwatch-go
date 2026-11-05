package omnirules

// Table names mirror app/Services/Ingestion/TelemetryEventSchema.php in the
// Omni Laravel application. Both sides must agree, so keep them in step.
var tables = map[string]string{
	"request":          "requests",
	"query":            "queries",
	"exception":        "exceptions",
	"log":              "logs",
	"command":          "commands",
	"queued-job":       "queued_jobs",
	"job-attempt":      "job_attempts",
	"cache-event":      "cache_events",
	"scheduled-task":   "scheduled_tasks",
	"mail":             "mail",
	"notification":     "notifications",
	"outgoing-request": "outgoing_requests",
	"user":             "users",
}

var booleanFields = []string{
	"without_overlapping",
	"on_one_server",
	"run_in_background",
	"even_in_maintenance_mode",
	"handled",
	"failed",
}

var jsonFields = []string{"context", "extra", "trace", "payload"}

// TableFor returns the ClickHouse table an event type is written to.
func TableFor(eventType string) (string, bool) {
	table, ok := tables[eventType]

	return table, ok
}

// Supports reports whether the event type is one Omni stores.
func Supports(eventType string) bool {
	_, ok := tables[eventType]

	return ok
}

// BooleanFields lists the fields ClickHouse stores as UInt8 flags.
func BooleanFields() []string {
	return booleanFields
}

// JSONFields lists the fields ClickHouse stores as encoded JSON strings.
func JSONFields() []string {
	return jsonFields
}

// Tables lists every known ClickHouse telemetry table. Used as an allowlist
// when a table name is interpolated into SQL.
func Tables() map[string]string {
	return tables
}
