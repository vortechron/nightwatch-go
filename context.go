package nightwatch

import "context"

type contextKey struct{}

// WithState stores the ExecutionState in the context.
func WithState(ctx context.Context, state *ExecutionState) context.Context {
	return context.WithValue(ctx, contextKey{}, state)
}

// StateFromContext retrieves the ExecutionState from the context.
// Returns nil if no state is present.
func StateFromContext(ctx context.Context) *ExecutionState {
	state, _ := ctx.Value(contextKey{}).(*ExecutionState)
	return state
}
