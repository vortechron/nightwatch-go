package nightwatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// withSampler swaps the harness tracer provider for one with this sampler and
// makes it the global one the helpers use.
func withSampler(t *testing.T, h *harness, sampler sdktrace.Sampler) {
	t.Helper()
	h.tp = sdktrace.NewTracerProvider(sdktrace.WithSampler(sampler), sdktrace.WithSyncer(h.tr.SpanExporter()))
	h.tracer = h.tp.Tracer("test")
	t.Cleanup(func() { _ = h.tp.Shutdown(context.Background()) })
	useHarness(t, h)
}

// lateTrace is a sampled remote parent whose trace id is above every rate
// below 1, so Sample(ctx, 0.1) drops it.
func lateTrace(ctx context.Context) context.Context {
	traceID, _ := trace.TraceIDFromHex("ffffffffffffffff0000000000000001")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")

	return trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled, Remote: true,
	}))
}

var sampledOutQueues = map[string]func(ctx context.Context) JobEnvelope{
	"Ignore": func(ctx context.Context) (env JobEnvelope) {
		Ignore(ctx, func(ctx context.Context) { env = dispatch(ctx) })

		return env
	},
	"Pause":       func(ctx context.Context) JobEnvelope { return dispatch(Pause(ctx)) },
	"DontSample":  func(ctx context.Context) JobEnvelope { DontSample(ctx); return dispatch(ctx) },
	"Sample(0.1)": func(ctx context.Context) JobEnvelope { Sample(ctx, 0.1); return dispatch(ctx) },
}

var samplers = map[string]sdktrace.Sampler{
	"parent_based": sdktrace.ParentBased(sdktrace.AlwaysSample()),
	"always_on":    sdktrace.AlwaysSample(),
}

// runSampledOutJob queues a job the way queue does, runs one attempt that
// returns err, and reports what the attempt passes on to its outgoing calls.
func runSampledOutJob(t *testing.T, h *harness, queue func(context.Context) JobEnvelope, err error) (outgoing string, nested JobEnvelope) {
	t.Helper()

	var env JobEnvelope
	h.request(lateTrace(context.Background()), 200, func(ctx context.Context, _ trace.Span) {
		env = queue(MarkActive(ctx))
	})
	h.sink.records = nil

	ctx, end := StartJobAttempt(context.Background(), env, JobInfo{Name: "jobs.Sync"}, 1)
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	nested = dispatch(ctx)
	end(err)

	return carrier.Get("traceparent"), nested
}

func TestSample_AFailedJobQueuedFromASampledOutExecutionIsKeptByTheExceptionRate(t *testing.T) {
	for samplerName, sampler := range samplers {
		for queueName, queue := range sampledOutQueues {
			t.Run(samplerName+"/"+queueName, func(t *testing.T) {
				h := newHarness(t, nil)
				withSampler(t, h, sampler)

				outgoing, nested := runSampledOutJob(t, h, queue, errors.New("job failed"))

				if attempts, exceptions := h.sink.ofType(RecordJobAttempt), h.sink.ofType(RecordException); len(attempts) != 1 || len(exceptions) != 1 {
					t.Fatalf("like Laravel, a failed job is sampled again at the exception rate: %d attempts, %d exceptions", len(attempts), len(exceptions))
				}

				if !strings.HasSuffix(outgoing, "-01") {
					t.Fatalf("the attempt is recorded, so its outgoing calls pass a sampled traceparent: %q", outgoing)
				}

				if !strings.HasSuffix(nested.TraceParent, "-00") {
					t.Fatalf("a job queued from the sampled-out attempt is sampled out too: %q", nested.TraceParent)
				}
			})
		}
	}
}

func TestSample_AJobQueuedFromASampledOutExecutionIsDroppedWhenItSucceedsOrTheExceptionRateIsZero(t *testing.T) {
	for samplerName, sampler := range samplers {
		t.Run(samplerName, func(t *testing.T) {
			h := newHarness(t, nil)
			withSampler(t, h, sampler)
			runSampledOutJob(t, h, sampledOutQueues["DontSample"], nil)

			if records := h.sink.all(); len(records) != 0 {
				t.Fatalf("a job that succeeds keeps the rate 0 of its dispatcher: %v", records)
			}

			never := newHarness(t, func(c *Config) { c.ExceptionSampleRate = 0 })
			withSampler(t, never, sampler)
			runSampledOutJob(t, never, sampledOutQueues["Ignore"], errors.New("job failed"))

			if records := never.sink.all(); len(records) != 0 {
				t.Fatalf("with NIGHTWATCH_EXCEPTION_SAMPLE_RATE=0 the failed job stays dropped: %v", records)
			}
		})
	}
}

func TestSample_SampleInsideAJobQueuedFromASampledOutExecutionStillWins(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)

	var env JobEnvelope
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		DontSample(ctx)
		env = dispatch(ctx)
	})
	h.sink.records = nil

	ctx, end := StartJobAttempt(context.Background(), env, JobInfo{Name: "jobs.Sync"}, 1)
	Sample(ctx, 1)
	end(nil)

	if got := h.sink.ofType(RecordJobAttempt); len(got) != 1 {
		t.Fatalf("Sample(ctx, 1) in the job keeps the attempt: %v", h.sink.all())
	}
}
