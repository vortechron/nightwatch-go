package goravel_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/goravel/framework/contracts/binding"
	mocksfoundation "github.com/goravel/framework/mocks/foundation"

	nightwatch "github.com/vortechron/nightwatch-go"
	nwgoravel "github.com/vortechron/nightwatch-go/goravel"
)

// bootedQueryLocation boots the provider on an app with no services, on an
// SDK tracer provider, and returns the location a Goravel database span gets.
func bootedQueryLocation(t *testing.T, ignoreQueries bool) string {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = tp.Shutdown(context.Background()) })

	cfg := nightwatch.ConfigFromEnv(func(k string) string { return map[string]string{"NIGHTWATCH_TOKEN": "t"}[k] }, "h")
	cfg.IgnoreQueries, cfg.FlushInterval = ignoreQueries, time.Hour
	client := nightwatch.New(cfg)
	t.Cleanup(func() { _ = client.Shutdown(context.Background()) })

	app := mocksfoundation.NewApplication(t)
	app.EXPECT().Make(nwgoravel.BindingClient).Return(client, nil)
	app.EXPECT().MakeCache().Return(nil)
	app.EXPECT().MakeArtisan().Return(nil)
	app.EXPECT().MakeSchedule().Return(nil)
	app.EXPECT().MakeQueue().Return(nil)
	app.EXPECT().Fresh(binding.Cache, binding.Schedule, binding.Artisan, binding.Queue)
	nwgoravel.NightwatchServiceProvider{}.Boot(app)

	_, span := tp.Tracer(nwgoravel.GoravelDatabaseScope).Start(context.Background(), "db", trace.WithSpanKind(trace.SpanKindClient))
	span.End()

	for _, kv := range recorder.Ended()[0].Attributes() {
		if string(kv.Key) == nightwatch.AttrQueryFile {
			return kv.Value.AsString()
		}
	}

	return ""
}

func TestProvider_WithQueriesIgnoredTheQueryLocationIsNotRead(t *testing.T) {
	if file := bootedQueryLocation(t, false); !strings.HasSuffix(file, "querycaller_boot_test.go") {
		t.Fatalf("by default the provider records query locations, got %q", file)
	}

	if file := bootedQueryLocation(t, true); file != "" {
		t.Fatalf("NIGHTWATCH_IGNORE_QUERIES=true must skip the stack read, got %q", file)
	}
}
