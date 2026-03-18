package sensor

import (
	"time"

	nightwatch "github.com/laravel/nightwatch-go"
)

// OutgoingRequestRecord builds an outgoing HTTP request telemetry record.
// Matches the schema from src/Sensors/OutgoingRequestSensor.php lines 48-68.
func OutgoingRequestRecord(
	state *nightwatch.ExecutionState,
	method, url, host string,
	statusCode int,
	requestSize, responseSize int,
	startTime time.Time,
	duration time.Duration,
) nightwatch.Record {
	state.IncrOutgoingRequests()

	return nightwatch.Record{
		"v":                 1,
		"t":                 nightwatch.RecordTypeOutgoingRequest,
		"timestamp":         float64(startTime.UnixMicro()) / 1_000_000,
		"deploy":            state.Deploy,
		"server":            state.Server,
		"_group":            nightwatch.GroupHash(host),
		"trace_id":          state.TraceID,
		"execution_source":  state.Source,
		"execution_id":      state.ExecutionID(),
		"execution_preview": state.ExecutionPreview(),
		"execution_stage":   state.CurrentStage(),
		"user":              state.UserID,
		"host":              nightwatch.TinyText(host),
		"method":            nightwatch.TinyText(method),
		"url":               nightwatch.Text(url),
		"duration":          int(duration.Microseconds()),
		"request_size":      requestSize,
		"response_size":     responseSize,
		"status_code":       statusCode,
	}
}
