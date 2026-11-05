// Package capture is a fake Omni for local tests. It checks every batch with
// Omni's own decoder rules and saves accepted batches to disk.
package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vortechron/nightwatch-go/internal/omnirules"
)

const (
	keyError    = "error"
	keyReceived = "received"
	keyStatus   = "status"
)

// Server answers like Omni: POST /api/agent-auth, POST /api/omni/ingest and
// GET /api/omni/ping.
type Server struct {
	dir   string
	token string
	log   *slog.Logger

	mu       sync.Mutex
	seen     map[string]bool
	accepted int
	rejected []Rejection
}

// Rejection is a batch the decoder refused.
type Rejection struct {
	Status  int
	Message string
	Code    string
	Context map[string]any
}

// New creates a server that writes accepted batches to dir. When token is not
// empty only that bearer token is accepted.
func New(dir, token string, logger *slog.Logger) (*Server, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating capture dir: %w", err)
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &Server{dir: dir, token: token, log: logger, seen: map[string]bool{}}, nil
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/agent-auth", s.auth)
	mux.HandleFunc("POST /api/omni/agent-auth", s.auth)
	mux.HandleFunc("POST /api/omni/ingest", s.ingest)
	mux.HandleFunc("GET /api/omni/ping", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"pong": true})
	})

	return mux
}

// Accepted is the number of batches written to disk.
func (s *Server) Accepted() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.accepted
}

// Rejected lists the batches that failed validation.
func (s *Server) Rejected() []Rejection {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]Rejection(nil), s.rejected...)
}

func (s *Server) authorised(w http.ResponseWriter, r *http.Request) (string, bool) {
	token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{keyError: "Unauthorized"})

		return "", false
	}

	if s.token != "" && token != s.token {
		writeJSON(w, http.StatusUnauthorized, map[string]any{keyError: "Invalid token"})

		return "", false
	}

	return token, true
}

func (s *Server) auth(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authorised(w, r)
	if !ok {
		return
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"expires_in": 86400,
		"refresh_in": 82800,
		"ingest_url": scheme + "://" + r.Host + "/api/omni/ingest",
	})
}

func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorised(w, r); !ok {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, int64(omnirules.DefaultLimits.MaxBodyBytes)+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{keyError: "Invalid JSON payload"})

		return
	}

	batch, perr := omnirules.Validate(body, r.Header.Get("Content-Encoding"))
	if perr != nil {
		s.reject(w, perr)

		return
	}

	if len(batch.Events) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{keyStatus: "ok", keyReceived: 0})

		return
	}

	if key := r.Header.Get("Idempotency-Key"); s.duplicate(key) {
		writeJSON(w, http.StatusOK, map[string]any{keyStatus: "ok", keyReceived: len(batch.Events), "duplicate": true})

		return
	}

	if err := s.save(batch); err != nil {
		s.log.Error("capture: cannot save batch", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{keyError: "capture failed", "code": "STORAGE_UNAVAILABLE"})

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{keyStatus: "ok", keyReceived: len(batch.Events)})
}

func (s *Server) duplicate(key string) bool {
	if key == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.seen[key] {
		return true
	}

	s.seen[key] = true

	return false
}

func (s *Server) reject(w http.ResponseWriter, perr *omnirules.PayloadError) {
	s.mu.Lock()
	s.rejected = append(s.rejected, Rejection{Status: perr.Status, Message: perr.Message, Code: perr.Code, Context: perr.Context})
	s.mu.Unlock()

	s.log.Warn("capture: batch rejected", "status", perr.Status, "message", perr.Message, "code", perr.Code, "context", perr.Context)

	payload := map[string]any{keyError: perr.Message}
	if perr.Code != "" {
		payload["code"] = perr.Code
	}

	for key, value := range perr.Context {
		payload[key] = value
	}

	writeJSON(w, perr.Status, payload)
}

func (s *Server) save(batch omnirules.Batch) error {
	encoded, err := json.Marshal(map[string]any{"records": batch.Events})
	if err != nil {
		return fmt.Errorf("encoding batch: %w", err)
	}

	name := fmt.Sprintf("batch-%d.json", time.Now().UnixNano())
	if err := os.WriteFile(filepath.Join(s.dir, name), encoded, 0o600); err != nil {
		return fmt.Errorf("writing batch: %w", err)
	}

	s.mu.Lock()
	s.accepted++
	s.mu.Unlock()

	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	var buffer bytes.Buffer
	_ = json.NewEncoder(&buffer).Encode(payload)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buffer.Bytes())
}
