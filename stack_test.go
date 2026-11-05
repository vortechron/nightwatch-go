package nightwatch

import (
	"encoding/json"
	"strings"
	"testing"
)

const sampleStack = `goroutine 7 [running]:
runtime/debug.Stack()
	/usr/local/go/src/runtime/debug/stack.go:26 +0x5e
github.com/vortechron/nightwatch-go.RecordException({0x1, 0x2}, {0x3, 0x4})
	/work/nightwatch-go/helpers.go:40 +0x10
goravel/app/http/controllers.(*TaskController).Show(0x1400, {0x1, 0x2})
	/app/http/controllers/task.go:42 +0x88
net/http.HandlerFunc.ServeHTTP(0x1, {0x2, 0x3}, 0x4)
	/usr/local/go/src/net/http/server.go:2220 +0x29
`

func TestParseStack_SkipsInternalFramesAndFindsOrigin(t *testing.T) {
	frames := ParseStack(sampleStack)
	if len(frames) != 2 {
		t.Fatalf("want 2 frames, got %d: %+v", len(frames), frames)
	}
	first := frames[0]
	if first.File != "/app/http/controllers/task.go:42" || !strings.HasPrefix(first.Source, "goravel/app/http/controllers.(*TaskController).Show(") {
		t.Fatalf("first frame: %+v", first)
	}
	file, line := FrameLocation(frames)
	if file != "/app/http/controllers/task.go" || line != 42 {
		t.Fatalf("location %q %d", file, line)
	}
}

func TestEncodeFrames_ShapeForOmni(t *testing.T) {
	encoded := EncodeFrames(ParseStack(sampleStack))
	var back []map[string]any
	if err := json.Unmarshal([]byte(encoded), &back); err != nil {
		t.Fatal(err)
	}
	if _, ok := back[0]["code"]; !ok || back[0]["code"] != nil {
		t.Fatalf("code must be present and null: %v", back[0])
	}
	if EncodeFrames(nil) != "[]" {
		t.Fatal("no frames must encode as []")
	}
}

func TestFrameLocation_Empty(t *testing.T) {
	if f, l := FrameLocation(nil); f != "" || l != 0 {
		t.Fatalf("%q %d", f, l)
	}
}
