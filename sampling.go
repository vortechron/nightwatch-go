package nightwatch

import (
	"context"
	"maps"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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

// vendorCommands is Laravel's Nightwatch::defaultVendorCommands().
var vendorCommands = []string{
	"auth:clear-resets", "config:cache", "horizon:snapshot", "horizon:status", "horizon:supervisor",
	"inertia:start-ssr", "invoke-serialized-closure", "model:prune", "nightwatch:agent", "nightwatch:status",
	"octane:status", "queue:monitor", "reverb:start", "schedule:list",
}

// DefaultVendorCommands lists the framework and package commands Laravel does
// not sample by default. nightwatch-go records them unless
// CaptureDefaultVendorCommands(false) is called, so upgrading changes nothing.
func DefaultVendorCommands() []string {
	return slices.Clone(vendorCommands)
}

// CaptureDefaultVendorCommands turns the DefaultVendorCommands rule off (true,
// the default here) or on (false, Laravel's default). With the rule on, those
// commands, and scheduled tasks that run them, have a sample rate of 0.
func (h *Hooks) CaptureDefaultVendorCommands(capture bool) {
	h.update(func(s *hookSet) { s.skipVendorCommands = !capture })
}

// SampleScheduledTask sets the sample rate of the scheduled task with this
// name (Laravel's Console\Sample::rate on a task). It beats
// NIGHTWATCH_SCHEDULED_TASK_SAMPLE_RATE; Sample inside a run still wins.
func (h *Hooks) SampleScheduledTask(name string, rate float64) {
	if rate < 0 || rate > 1 {
		rate = 0
	}

	h.update(func(s *hookSet) {
		rates := maps.Clone(s.taskRates)
		if rates == nil {
			rates = map[string]float64{}
		}

		rates[name] = rate
		s.taskRates = rates
	})
}

// vendorCommand reports whether a command line starts with a vendor command
// that is not sampled.
func (s *hookSet) vendorCommand(line string) bool {
	name, _, _ := strings.Cut(strings.TrimSpace(line), " ")

	return s.skipVendorCommands && slices.Contains(vendorCommands, name)
}

// configuredRate is the rate of an execution before Sample: the vendor rule,
// a task's own rate, or the configured rate of its kind.
func (t *Translator) configuredRate(root sdktrace.ReadOnlySpan, kind string) float64 {
	attrs := newAttrSet(root.Attributes())
	hooks := t.load()

	switch kind {
	case RecordCommand:
		if hooks.vendorCommand(attrs.str(AttrCommandName)) {
			return 0
		}

		return t.cfg.CommandSampleRate
	case RecordScheduledTask:
		if hooks.vendorCommand(attrs.str(AttrTaskCommand)) {
			return 0
		}

		if rate, set := hooks.taskRates[attrs.str(AttrTaskName)]; set {
			return rate
		}

		return t.cfg.ScheduledTaskSampleRate
	case RecordJobAttempt:
		if root.Parent().IsValid() {
			return t.cfg.RequestSampleRate
		}

		return t.cfg.CommandSampleRate
	default:
		return t.cfg.RequestSampleRate
	}
}
