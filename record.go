package nightwatch

// Record type constants matching the PHP 't' field values.
const (
	RecordTypeRequest         = "request"
	RecordTypeQuery           = "query"
	RecordTypeException       = "exception"
	RecordTypeLog             = "log"
	RecordTypeOutgoingRequest = "outgoing-request"
)

// CommonFields returns the fields shared by all record types.
// Each sensor adds its type-specific fields on top of these.
func CommonFields(state *ExecutionState) Record {
	return Record{
		"deploy":            state.Deploy,
		"server":            state.Server,
		"trace_id":          state.TraceID,
		"execution_source":  state.Source,
		"execution_id":      state.ExecutionID(),
		"execution_preview": state.ExecutionPreview(),
		"execution_stage":   state.CurrentStage(),
		"user":              state.UserID,
	}
}
