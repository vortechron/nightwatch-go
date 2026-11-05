package nightwatch

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

const (
	sourceContext   = 5
	maxSourceFrames = 10
)

// codeLine is one source line of a frame.
type codeLine struct {
	number int
	text   string
}

// codeLines marshals as the JSON object {"lineNo": "text", ...} that Omni
// shows, keeping the lines in file order.
type codeLines []codeLine

// MarshalJSON writes the lines in order, which a Go map would not.
func (c codeLines) MarshalJSON() ([]byte, error) {
	var out strings.Builder

	out.WriteByte('{')

	for i, line := range c {
		if i > 0 {
			out.WriteByte(',')
		}

		out.WriteString(jsonString(strconv.Itoa(line.number)))
		out.WriteByte(':')
		out.WriteString(jsonString(line.text))
	}

	out.WriteByte('}')

	return []byte(out.String()), nil
}

// withSource returns the frames with source lines added to the first ten
// application frames whose file can be read. It copies; the input is not
// changed. When the option is off the frames come back with code null.
func (t *Translator) withSource(frames []Frame) []Frame {
	if !t.cfg.CaptureExceptionSourceCode {
		return frames
	}

	out := make([]Frame, len(frames))
	copy(out, frames)

	captured := 0

	for i := range out {
		if captured >= maxSourceFrames {
			break
		}

		file, line := splitLocation(out[i].File)
		if lines := readSource(file, line); lines != nil {
			out[i].Code = lines
			captured++
		}
	}

	return out
}

func splitLocation(location string) (string, int) {
	frames := []Frame{{File: location}}

	return FrameLocation(frames)
}

// libraryPaths mark files of dependencies and of the Go installation (the
// standard library lives under <GOROOT>/src, usually ".../go/src/",
// ".../libexec/src/" or a downloaded ".../toolchain@.../src/").
var libraryPaths = []string{"/pkg/mod/", "/vendor/", "/go/src/", "/libexec/src/", "/toolchain@"}

// isApplicationFile skips the Go standard library and dependencies.
func isApplicationFile(file string) bool {
	if file == "" {
		return false
	}

	for _, marker := range libraryPaths {
		if strings.Contains(file, marker) {
			return false
		}
	}

	return true
}

// readSource returns the lines around line (five each side), or nil when the
// file is not an application file or cannot be read.
func readSource(file string, line int) codeLines {
	if line <= 0 || !isApplicationFile(file) {
		return nil
	}

	handle, err := os.Open(file) //nolint:gosec // the path comes from our own stack frames
	if err != nil {
		return nil
	}
	defer handle.Close()

	first, last := max(line-sourceContext, 1), line+sourceContext
	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lines codeLines

	for number := 1; scanner.Scan() && number <= last; number++ {
		if number >= first {
			lines = append(lines, codeLine{number: number, text: SanitizeUTF8(scanner.Text())})
		}
	}

	if len(lines) == 0 {
		return nil
	}

	return lines
}
