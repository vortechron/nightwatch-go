package omnirules

import (
	"encoding/json"
	"regexp"
	"strconv"
)

var (
	uuidPattern  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	groupPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

	cacheTypes  = setOf("hit", "miss", "write", "write-failure", "delete", "delete-failure")
	jobStatuses = setOf("processed", "released", "failed")
	taskStatus  = setOf("processed", "failed", "skipped")

	// unsignedFields are stored as UInt columns: integers, never negative.
	unsignedFields = setOf(
		"v", "duration", "status_code", "line", "request_size", "response_size", "bootstrap",
		"before_middleware", "action", "render", "after_middleware", "sending", "terminating",
		"exceptions", "logs", "queries", "lazy_loads", "jobs_queued", "mail", "notifications",
		"outgoing_requests", "files_read", "files_written", "cache_events", "hydrated_models",
		"peak_memory_usage", "ttl", "to", "cc", "bcc", "attachments", "attempt", "exit_code",
		"repeat_seconds",
	)
	stringFields = setOf(
		"sql", "file", "connection", "connection_type", "method", "url", "route_name", "route_domain",
		"route_path", "route_action", "ip", "user", "class", "message", "code", "name", "level",
		"host", "store", "key", "queue", "job_id", "execution_source", "execution_preview",
		"execution_stage", "deploy", "server", "exception_preview", "status", "channel", "mailer",
		"subject", "cron", "timezone", "command",
	)
)

func setOf(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}

	return set
}

// CheckStorage applies the rules that decide whether ClickHouse can insert a
// record Omni already accepted: UUID and enum formats, column types and
// nulls. Omni answers 503 when such an insert fails, so this returns a 503.
func CheckStorage(event Event, index int) *PayloadError {
	for _, check := range []func(Event) string{checkNulls, checkIDs, checkEnums, checkTypes} {
		if field := check(event); field != "" {
			return storageError(index, field)
		}
	}

	return nil
}

func storageError(index int, field string) *PayloadError {
	return newCodedPayloadError("Telemetry storage would reject the record", 503, "STORAGE_UNAVAILABLE", map[string]any{
		"record_index": index,
		"field":        field,
	})
}

func checkNulls(event Event) string {
	for key, value := range event {
		if value == nil {
			return key
		}
	}

	return ""
}

func checkIDs(event Event) string {
	eventType, _ := event["t"].(string)
	if eventType != "user" {
		if id, _ := event["trace_id"].(string); !uuidPattern.MatchString(id) {
			return "trace_id"
		}
	}

	for _, key := range []string{"attempt_id", "execution_id"} {
		if value, ok := event[key]; ok && !validOptionalUUID(value) {
			return key
		}
	}

	if value, ok := event["_group"]; ok {
		if group, isString := value.(string); !isString || (group != "" && !groupPattern.MatchString(group)) {
			return "_group"
		}
	}

	return ""
}

func validOptionalUUID(value any) bool {
	text, ok := value.(string)

	return ok && (text == "" || uuidPattern.MatchString(text))
}

func checkEnums(event Event) string {
	eventType, _ := event["t"].(string)
	enums := map[string]map[string]bool{
		"cache-event":    cacheTypes,
		"job-attempt":    jobStatuses,
		"scheduled-task": taskStatus,
	}
	field := "status"
	if eventType == "cache-event" {
		field = "type"
	}

	allowed, ok := enums[eventType]
	if !ok {
		return ""
	}

	if value, present := event[field]; present {
		if text, isString := value.(string); !isString || !allowed[text] {
			return field
		}
	}

	return ""
}

func checkTypes(event Event) string {
	for key, value := range event {
		switch {
		case key == "timestamp":
			if _, ok := value.(json.Number); !ok {
				return key
			}
		case unsignedFields[key]:
			if !unsignedInteger(value) {
				return key
			}
		case stringFields[key]:
			if _, ok := value.(string); !ok {
				return key
			}
		case key == "route_methods":
			if _, ok := value.([]any); !ok {
				return key
			}
		}
	}

	return ""
}

func unsignedInteger(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}

	_, err := strconv.ParseUint(number.String(), 10, 64)

	return err == nil
}
