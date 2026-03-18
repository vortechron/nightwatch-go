package sensor

import (
	"regexp"
	"strings"
	"time"

	nightwatch "github.com/laravel/nightwatch-go"
)

var (
	// Matches IN (?, ?, ?) or IN (1, 2, 3) patterns for SQL normalization
	inClauseRegex = regexp.MustCompile(`(?i)in \([\d?\s,]+\)`)
	// Matches VALUES (?, ?), (?, ?) patterns for INSERT normalization
	valuesRegex = regexp.MustCompile(`(?i)values [(?,\s)]+`)
)

// QueryRecord builds a query telemetry record.
// Matches the schema from src/Sensors/QuerySensor.php lines 60-79.
func QueryRecord(state *nightwatch.ExecutionState, sql, connection, connectionType, file string, line int, duration time.Duration) nightwatch.Record {
	state.IncrQueries()

	durationMicros := int(duration.Microseconds())
	timestamp := float64(time.Now().Add(-duration).UnixMicro()) / 1_000_000

	return nightwatch.Record{
		"v":                 1,
		"t":                 nightwatch.RecordTypeQuery,
		"timestamp":         timestamp,
		"deploy":            state.Deploy,
		"server":            state.Server,
		"_group":            queryGroupHash(connection, sql),
		"trace_id":          state.TraceID,
		"execution_source":  state.Source,
		"execution_id":      state.ExecutionID(),
		"execution_preview": state.ExecutionPreview(),
		"execution_stage":   state.CurrentStage(),
		"user":              state.UserID,
		"sql":               nightwatch.MediumText(sql),
		"file":              nightwatch.TinyText(file),
		"line":              line,
		"duration":          durationMicros,
		"connection":        nightwatch.TinyText(connection),
		"connection_type":   connectionType,
	}
}

// queryGroupHash normalizes the SQL and computes the group hash.
// Matches PHP QuerySensor::hash() logic.
func queryGroupHash(connection, sql string) string {
	normalized := inClauseRegex.ReplaceAllString(sql, "in (...?)")

	if strings.Contains(strings.ToLower(normalized), "insert") {
		normalized = valuesRegex.ReplaceAllString(normalized, "values ...")
	}

	return nightwatch.GroupHashf("%s,%s", connection, normalized)
}
