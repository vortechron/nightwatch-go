package nightwatch

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// Frame is one entry of Omni's exception "trace" list.
type Frame struct {
	// File is "path:line".
	File string `json:"file"`
	// Source is the function, for example "pkg.(*T).Method(args)".
	Source string `json:"source"`
	// Code is the source excerpt. Always null: the client does not read files.
	Code any `json:"code"`
}

var internalFramePrefixes = []string{
	"runtime/debug.",
	"runtime.gopanic",
	"runtime.panic",
	"runtime.sigpanic",
	"github.com/vortechron/nightwatch-go.",
	"github.com/vortechron/nightwatch-go/goravel.",
	"go.opentelemetry.io/",
}

// ParseStack turns the text of runtime/debug.Stack into Omni frames. Frames
// that belong to the runtime or to this client are skipped so the first frame
// is where the application raised the error.
func ParseStack(stack string) []Frame {
	lines := strings.Split(strings.ReplaceAll(stack, "\r\n", "\n"), "\n")
	frames := make([]Frame, 0, len(lines)/2)

	for i := 0; i+1 < len(lines); i++ {
		function, location := lines[i], lines[i+1]
		if strings.HasPrefix(function, "goroutine ") || strings.HasPrefix(function, "\t") || !strings.HasPrefix(location, "\t") {
			continue
		}

		i++
		if isInternalFrame(function) {
			continue
		}

		frames = append(frames, Frame{File: trimOffset(location), Source: strings.TrimSpace(function)})
	}

	return frames
}

func isInternalFrame(function string) bool {
	for _, prefix := range internalFramePrefixes {
		if strings.HasPrefix(function, prefix) {
			return true
		}
	}

	return false
}

// trimOffset turns "\t/path/file.go:42 +0x88" into "/path/file.go:42".
func trimOffset(location string) string {
	location = strings.TrimSpace(location)
	if space := strings.Index(location, " "); space >= 0 {
		location = location[:space]
	}

	return location
}

// FrameLocation returns the file and line of the first frame.
func FrameLocation(frames []Frame) (string, int) {
	if len(frames) == 0 {
		return "", 0
	}

	location := frames[0].File
	colon := strings.LastIndex(location, ":")
	if colon < 0 {
		return location, 0
	}

	line, err := strconv.Atoi(location[colon+1:])
	if err != nil {
		return location, 0
	}

	return location[:colon], line
}

// EncodeFrames renders frames as the JSON string Omni stores in "trace".
func EncodeFrames(frames []Frame) string {
	if len(frames) == 0 {
		return "[]"
	}

	return jsonString(frames)
}

const maxStackFrames = 64

// CaptureStack returns the Go call stack of the caller in the text format
// ParseStack reads: a function line, then a tab and "file:line". skip is the
// number of caller frames to leave out. It uses runtime.Callers, so the file
// and line are the real ones.
func CaptureStack(skip int) string {
	pcs := make([]uintptr, maxStackFrames)
	count := runtime.Callers(skip+2, pcs)
	frames := runtime.CallersFrames(pcs[:count])

	var out strings.Builder

	for {
		frame, more := frames.Next()
		fmt.Fprintf(&out, "%s(...)\n\t%s:%d\n", frame.Function, frame.File, frame.Line)

		if !more {
			break
		}
	}

	return out.String()
}
