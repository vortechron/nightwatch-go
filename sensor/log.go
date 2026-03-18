package sensor

import (
	"encoding/json"
	"time"

	nightwatch "github.com/laravel/nightwatch-go"
)

// LogRecord builds a log telemetry record.
// Matches the schema from src/Sensors/LogSensor.php lines 30-47.
func LogRecord(
	state *nightwatch.ExecutionState,
	level string, // PSR-3 level: "debug", "info", "warning", "error"
	message string,
	contextAttrs map[string]any,
	extraAttrs map[string]any,
	timestamp time.Time,
) nightwatch.Record {
	state.IncrLogs()

	contextJSON := "{}"
	if contextAttrs != nil && len(contextAttrs) > 0 {
		if b, err := json.Marshal(contextAttrs); err == nil {
			contextJSON = string(b)
		}
	}

	extraJSON := "{}"
	if extraAttrs != nil && len(extraAttrs) > 0 {
		if b, err := json.Marshal(extraAttrs); err == nil {
			extraJSON = string(b)
		}
	}

	return nightwatch.Record{
		"v":                 1,
		"t":                 nightwatch.RecordTypeLog,
		"timestamp":         float64(timestamp.UnixMicro()) / 1_000_000,
		"deploy":            state.Deploy,
		"server":            state.Server,
		"trace_id":          state.TraceID,
		"execution_source":  state.Source,
		"execution_id":      state.ExecutionID(),
		"execution_preview": state.ExecutionPreview(),
		"execution_stage":   state.CurrentStage(),
		"user":              state.UserID,
		"level":             nightwatch.TinyText(level),
		"message":           nightwatch.Text(message),
		"context":           nightwatch.Text(contextJSON),
		"extra":             nightwatch.Text(extraJSON),
	}
}
