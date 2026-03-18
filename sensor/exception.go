package sensor

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"

	nightwatch "github.com/laravel/nightwatch-go"
)

// traceFrame represents a single frame in the exception trace.
// Matches the PHP trace serialization format.
type traceFrame struct {
	File   string  `json:"file"`
	Source string  `json:"source"`
	Code   *string `json:"code"` // always null in Go version (no source code capture)
}

// ExceptionRecord builds an exception telemetry record.
// Matches the schema from src/Sensors/ExceptionSensor.php lines 89-111.
func ExceptionRecord(
	state *nightwatch.ExecutionState,
	err error,
	handled bool,
	skip int, // number of stack frames to skip
) nightwatch.Record {
	state.IncrExceptions()

	if !handled {
		state.ExceptionPreview = err.Error()
	}

	typeName := fmt.Sprintf("%T", err)
	file, line := callerInfo(skip + 1)
	trace := captureTrace(skip + 1)
	traceJSON, _ := json.Marshal(trace)

	now := float64(time.Now().UnixMicro()) / 1_000_000

	return nightwatch.Record{
		"v":                 3,
		"t":                 nightwatch.RecordTypeException,
		"timestamp":         now,
		"deploy":            state.Deploy,
		"server":            state.Server,
		"_group":            nightwatch.GroupHashf("%s,,%s,%d", typeName, file, line),
		"trace_id":          state.TraceID,
		"execution_source":  state.Source,
		"execution_id":      state.ExecutionID(),
		"execution_preview": state.ExecutionPreview(),
		"execution_stage":   state.CurrentStage(),
		"user":              state.UserID,
		"class":             nightwatch.Text(typeName),
		"file":              nightwatch.Text(file),
		"line":              line,
		"message":           nightwatch.Text(err.Error()),
		"code":              "",
		"trace":             nightwatch.MediumText(string(traceJSON)),
		"handled":           handled,
		"go_version":        runtime.Version(),
	}
}

func callerInfo(skip int) (string, int) {
	_, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return "unknown", 0
	}
	return file, line
}

func captureTrace(skip int) []traceFrame {
	var pcs [50]uintptr
	n := runtime.Callers(skip+2, pcs[:])
	if n == 0 {
		return nil
	}

	frames := runtime.CallersFrames(pcs[:n])
	var trace []traceFrame

	for {
		frame, more := frames.Next()
		// Skip runtime and testing frames
		if strings.HasPrefix(frame.Function, "runtime.") ||
			strings.HasPrefix(frame.Function, "testing.") {
			if !more {
				break
			}
			continue
		}

		trace = append(trace, traceFrame{
			File:   fmt.Sprintf("%s:%d", frame.File, frame.Line),
			Source: frame.Function + "()",
			Code:   nil,
		})

		if !more {
			break
		}
	}

	return trace
}
