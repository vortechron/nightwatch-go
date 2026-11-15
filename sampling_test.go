package nightwatch

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func TestSample_DontSampleDropsTheRequestAndSampleKeepsIt(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RequestSampleRate = 0; c.ExceptionSampleRate = 0 })

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		Sample(ctx, 1)
		h.query(ctx, "select 'kept'")
	})

	if len(h.sink.ofType(RecordRequest)) != 1 || len(h.sink.ofType(RecordQuery)) != 1 {
		t.Fatalf("Sample(ctx, 1) must keep the request whatever the global rate: %v", h.sink.all())
	}

	h2 := newHarness(t, nil)
	h2.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		DontSample(ctx)
		h2.query(ctx, "select 'dropped'")
	})

	if records := h2.sink.all(); len(records) != 0 {
		t.Fatalf("DontSample must drop the execution: %v", records)
	}
}

func TestSample_AnUnhandledExceptionStillRerollsWithTheExceptionRate(t *testing.T) {
	h := newHarness(t, nil)

	h.request(context.Background(), 500, func(ctx context.Context, span trace.Span) {
		DontSample(MarkActive(ctx))
		addException(span, "boom")
	})

	if len(h.sink.ofType(RecordRequest)) != 1 || len(h.sink.ofType(RecordException)) != 1 {
		t.Fatalf("like Laravel, an unhandled exception samples again with NIGHTWATCH_EXCEPTION_SAMPLE_RATE: %v", h.sink.all())
	}
}

func TestSample_RatesOutsideZeroToOneMeanZeroAndAPlainContextIsANoOp(t *testing.T) {
	h := newHarness(t, nil)

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		Sample(ctx, 1) // not marked active: no execution to change
		Sample(MarkActive(ctx), 1.5)
	})

	if len(h.sink.all()) != 0 {
		t.Fatalf("a rate above 1 is 0, as in Laravel: %v", h.sink.all())
	}
}

func TestSample_DefaultVendorCommandsAreKeptUnlessCaptureIsTurnedOff(t *testing.T) {
	run := func(capture *bool) (commands, tasks int) {
		h := newHarness(t, nil)
		if capture != nil {
			h.tr.CaptureDefaultVendorCommands(*capture)
		}
		_, cmd := h.execution(context.Background(), RecordCommand, attribute.String(AttrCommandName, "schedule:list"))
		cmd.End()
		_, own := h.execution(context.Background(), RecordCommand, attribute.String(AttrCommandName, "app:report"))
		own.End()
		_, task := h.execution(context.Background(), RecordScheduledTask, attribute.String(AttrTaskName, "prune"),
			attribute.String(AttrTaskCommand, "model:prune --model=Post"))
		task.End()

		return len(h.sink.ofType(RecordCommand)), len(h.sink.ofType(RecordScheduledTask))
	}

	if commands, tasks := run(nil); commands != 2 || tasks != 1 {
		t.Fatalf("by default vendor commands are recorded (no change on upgrade): %d commands, %d tasks", commands, tasks)
	}

	off := false
	if commands, tasks := run(&off); commands != 1 || tasks != 0 {
		t.Fatalf("Laravel's vendor list must be skipped: %d commands, %d tasks", commands, tasks)
	}

	if got := DefaultVendorCommands(); len(got) != 14 || got[0] != "auth:clear-resets" {
		t.Fatalf("DefaultVendorCommands() = %v", got)
	}
}

func TestSample_ScheduledTaskRateAndSampleInsideTheTaskWins(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.SampleScheduledTask("reports", 0)

	_, task := h.execution(context.Background(), RecordScheduledTask, attribute.String(AttrTaskName, "reports"))
	task.End()

	ctx, again := h.execution(context.Background(), RecordScheduledTask, attribute.String(AttrTaskName, "reports"))
	Sample(MarkActive(ctx), 1)
	again.End()

	if got := len(h.sink.ofType(RecordScheduledTask)); got != 1 {
		t.Fatalf("one run dropped by its task rate, one kept by Sample inside it; got %d", got)
	}
}
