package nightwatch

import "net/http"

// ResponseWriter wraps http.ResponseWriter to capture the status code and bytes written.
type ResponseWriter struct {
	http.ResponseWriter
	StatusCode   int
	BytesWritten int
	wroteHeader  bool
}

// NewResponseWriter wraps an existing http.ResponseWriter.
func NewResponseWriter(w http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{
		ResponseWriter: w,
		StatusCode:     http.StatusOK, // default if WriteHeader is never called
	}
}

func (rw *ResponseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.StatusCode = code
		rw.wroteHeader = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *ResponseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.BytesWritten += n
	return n, err
}

// Unwrap returns the original http.ResponseWriter, for compatibility with
// http.ResponseController and middleware that checks for specific interfaces.
func (rw *ResponseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}
