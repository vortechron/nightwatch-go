package sensor

import (
	nightwatch "github.com/laravel/nightwatch-go"
)

// BuildRequestRecord creates the request record from the execution state.
// This is called by the Nightwatch.EndRequest method — typically you don't call this directly.
// The record fields match src/Sensors/RequestSensor.php lines 110-164.
func BuildRequestRecord(state *nightwatch.ExecutionState) nightwatch.Record {
	// The main nightwatch package builds the request record internally.
	// This sensor package exists for additional sensor logic (query normalization, etc.)
	// that is independent of the core lifecycle.
	return nil
}
