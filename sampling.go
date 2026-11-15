package nightwatch

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
)

// AttrSampleRate on an execution root span replaces the configured sample
// rate for that execution. Sample sets it.
const AttrSampleRate = "nightwatch.sample.rate"

// Sample sets the sample rate of the execution ctx belongs to (Laravel's
// Nightwatch::sample). The last call wins, and it beats the configured rate,
// so a middleware can keep every admin request or a job can choose its own
// rate:
//
//	nightwatch.Sample(ctx, 1)   // always keep this request
//	nightwatch.Sample(ctx, 0.1) // keep one in ten
//
// A rate below 0 or above 1 means 0, as in Laravel. The decision is still made
// from the trace id, so it is the same on every server. An unhandled exception
// samples the execution again with the exception rate. Outside an execution
// (a plain context) Sample does nothing.
func Sample(ctx context.Context, rate float64) {
	exec := executionOf(ctx)
	if exec == nil {
		return
	}

	if rate < 0 || rate > 1 {
		rate = 0
	}

	exec.root.SetAttributes(attribute.Float64(AttrSampleRate, rate))
}

// DontSample drops the execution ctx belongs to (Laravel's
// Nightwatch::dontSample): Sample(ctx, 0).
func DontSample(ctx context.Context) {
	Sample(ctx, 0)
}
