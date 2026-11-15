package nightwatch_test

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The program the build test compiles. The app sits in /go/src/app, where the
// official golang image builds it; the library is a separate module.
const (
	locatedApp = `package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"example.com/dep"
	nightwatch "github.com/vortechron/nightwatch-go"
)

type sink struct{ records []nightwatch.Record }

func (s *sink) Enqueue(records []nightwatch.Record) { s.records = append(s.records, records...) }

func main() {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(nightwatch.QueryCallerProcessor(dep.Scope)), sdktrace.WithSpanProcessor(recorder))
	tracer := tp.Tracer(dep.Scope)

	query(tracer)
	dep.Query(tracer) // LOCATION dep
	dep.QueryInBackground(tracer)

	for _, span := range recorder.Ended() {
		var file string
		var line int64
		for _, kv := range span.Attributes() {
			switch kv.Key {
			case nightwatch.AttrQueryFile:
				file = kv.Value.AsString()
			case nightwatch.AttrQueryLine:
				line = kv.Value.AsInt64()
			}
		}
		fmt.Printf("%s %s:%d\n", span.Name(), file, line)
	}

	fmt.Println("code", exceptionHasCode())
}

func query(tracer trace.Tracer) {
	_, span := tracer.Start(context.Background(), "app", trace.WithSpanKind(trace.SpanKindClient)) // LOCATION app
	span.End()
}

// exceptionHasCode reports whether the first frame of an exception raised in
// the app carries its source lines.
func exceptionHasCode() bool {
	cfg := nightwatch.ConfigFromEnv(func(k string) string { return map[string]string{"NIGHTWATCH_TOKEN": "t"}[k] }, "h")
	cfg.RootGrace = 0
	out := &sink{}
	translator := nightwatch.NewTranslator(cfg, out)
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(translator.SpanExporter()))
	otel.SetTracerProvider(tp)

	ctx, span := tp.Tracer("http").Start(context.Background(), "GET /", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", "GET"), attribute.String("url.path", "/")))
	nightwatch.ReportError(ctx, errors.New("boom"))
	span.End()

	for _, record := range out.records {
		if record.Type() == "exception" {
			var frames []map[string]any
			_ = json.Unmarshal([]byte(record["trace"].(string)), &frames)

			return len(frames) > 0 && frames[0]["code"] != nil
		}
	}

	return false
}
`
	locatedDep = `package dep

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

const Scope = "example.com/db"

func Query(tracer trace.Tracer) {
	_, span := tracer.Start(context.Background(), "dep", trace.WithSpanKind(trace.SpanKindClient))
	span.End()
}

func QueryInBackground(tracer trace.Tracer) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, span := tracer.Start(context.Background(), "background", trace.WithSpanKind(trace.SpanKindClient)) // LOCATION background
		span.End()
	}()
	<-done
}
`
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// markerLine is the line of source that carries "// LOCATION <name>".
func markerLine(t *testing.T, source, name string) int {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(source))
	for line := 1; scanner.Scan(); line++ {
		if strings.HasSuffix(scanner.Text(), "// LOCATION "+name) {
			return line
		}
	}
	t.Fatalf("no marker %s", name)

	return 0
}

// buildLocatedApp writes the app and its library under root, builds it and
// returns what it prints.
func buildLocatedApp(t *testing.T, root string, trimpath bool) string {
	t.Helper()
	repo, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}

	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	otelVersion := regexp.MustCompile(`go\.opentelemetry\.io/otel/trace (v\S+)`).FindSubmatch(goMod)[1]
	goSum, err := os.ReadFile("go.sum")
	if err != nil {
		t.Fatal(err)
	}

	app, dep := filepath.Join(root, "go", "src", "app"), filepath.Join(root, "dep")
	writeFile(t, filepath.Join(dep, "go.mod"), fmt.Sprintf("module example.com/dep\n\ngo 1.26\n\nrequire go.opentelemetry.io/otel/trace %s\n", otelVersion))
	writeFile(t, filepath.Join(dep, "dep.go"), locatedDep)
	writeFile(t, filepath.Join(app, "go.mod"), fmt.Sprintf("module example.com/app\n\ngo 1.26\n\nrequire (\n\texample.com/dep v0.0.0\n\tgithub.com/vortechron/nightwatch-go v0.0.0\n)\n\nreplace example.com/dep => %s\n\nreplace github.com/vortechron/nightwatch-go => %s\n", dep, repo))
	writeFile(t, filepath.Join(app, "go.sum"), string(goSum))
	writeFile(t, filepath.Join(app, "main.go"), locatedApp)

	binary := filepath.Join(root, "located")
	args := []string{"build", "-mod=mod", "-o", binary}
	if trimpath {
		args = append(args, "-trimpath")
	}

	build := exec.CommandContext(t.Context(), "go", append(args, ".")...)
	build.Dir = app
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}

	output, err := exec.CommandContext(t.Context(), binary).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	return string(output)
}

func TestQueryCallerProcessor_FindsTheApplicationByItsModuleInDockerAndTrimpathBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}

	for _, trimpath := range []bool{false, true} {
		t.Run(fmt.Sprintf("trimpath=%v", trimpath), func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir()) // macOS: /var is /private/var
			if err != nil {
				t.Fatal(err)
			}
			output := buildLocatedApp(t, root, trimpath)

			appFile, depFile := filepath.Join(root, "go", "src", "app", "main.go"), filepath.Join(root, "dep", "dep.go")
			if trimpath {
				appFile, depFile = "example.com/app/main.go", "example.com/dep@v0.0.0/dep.go"
			}

			want := []string{
				fmt.Sprintf("app %s:%d", appFile, markerLine(t, locatedApp, "app")),
				// A library that runs the query for the app is not the location.
				fmt.Sprintf("dep %s:%d", appFile, markerLine(t, locatedApp, "dep")),
				// With no app code on the stack, the library is the fallback.
				fmt.Sprintf("background %s:%d", depFile, markerLine(t, locatedDep, "background")),
			}
			for _, line := range want {
				if !strings.Contains(output, line+"\n") {
					t.Errorf("want %q in:\n%s", line, output)
				}
			}

			// The source lines of an app frame are read where the file exists.
			if wantCode := fmt.Sprintf("code %v\n", !trimpath); !strings.Contains(output, wantCode) {
				t.Errorf("want %q in:\n%s", wantCode, output)
			}
		})
	}
}
