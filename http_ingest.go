package nightwatch

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// HTTPIngest sends telemetry directly to the Nightwatch backend via HTTP.
// It handles authentication (token exchange) and gzip-compressed ingestion,
// eliminating the need for a separate PHP agent process.
//
// This replicates the flow from agent/src/agent.php:
//  1. POST /api/agent-auth with the refresh token → get temp Bearer token + ingest URL
//  2. POST to ingest URL with gzipped JSON + Bearer token
type HTTPIngest struct {
	baseURL      string
	refreshToken string
	server       string
	buffer       *RecordsBuffer
	client       *http.Client

	mu          sync.Mutex
	authToken   string
	ingestURL   string
	authExpiry  time.Time
	digestOnFull bool
	debug        bool
}

// NewHTTPIngest creates a new HTTP-based ingest that talks directly to the backend.
func NewHTTPIngest(baseURL, refreshToken, server string, buffer *RecordsBuffer) *HTTPIngest {
	return &HTTPIngest{
		baseURL:      strings.TrimRight(baseURL, "/"),
		refreshToken: refreshToken,
		server:       server,
		buffer:       buffer,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		digestOnFull: true,
	}
}

// Write adds a record to the buffer. Auto-digests when buffer is full.
func (h *HTTPIngest) Write(record Record) error {
	h.buffer.Write(record)

	if h.digestOnFull && h.buffer.Full {
		return h.Digest()
	}
	return nil
}

// WriteNow immediately transmits a single record.
func (h *HTTPIngest) WriteNow(record Record) error {
	return h.sendRecords([]Record{record})
}

// Flush clears the buffer without transmitting.
func (h *HTTPIngest) Flush() {
	h.buffer.Flush()
}

// Digest pulls all buffered records and sends them to the backend.
func (h *HTTPIngest) Digest() error {
	h.buffer.mu.Lock()
	if len(h.buffer.records) == 0 {
		h.buffer.mu.Unlock()
		return nil
	}

	records := h.buffer.records
	h.buffer.records = make([]Record, 0, h.buffer.maxLen)
	h.buffer.Full = false
	h.buffer.mu.Unlock()

	return h.sendRecords(records)
}

// SetDigestOnFull controls whether the buffer auto-digests when full.
func (h *HTTPIngest) SetDigestOnFull(enabled bool) {
	h.digestOnFull = enabled
}

// Ping verifies connectivity by authenticating with the backend.
func (h *HTTPIngest) Ping() error {
	_, _, err := h.authenticate()
	return err
}

// SetDebug enables debug logging of payloads and responses.
func (h *HTTPIngest) SetDebug(debug bool) {
	h.debug = debug
}

// ingestPayload matches the format the PHP agent sends to the backend:
// {"records": [{...}, {...}, ...]}
type ingestPayload struct {
	Records []Record `json:"records"`
}

// sendRecords wraps records in {"records": [...]} and sends to the backend.
func (h *HTTPIngest) sendRecords(records []Record) error {
	payload := ingestPayload{Records: records}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("nightwatch: failed to marshal records: %w", err)
	}
	return h.send(data)
}

func (h *HTTPIngest) send(jsonData []byte) error {
	if h.debug {
		var pretty bytes.Buffer
		json.Indent(&pretty, jsonData, "", "  ")
		fmt.Fprintf(os.Stderr, "nightwatch: sending payload (%d bytes):\n%s\n", len(jsonData), pretty.String())
	}
	// Ensure we have valid auth
	token, ingestURL, err := h.getAuth()
	if err != nil {
		return fmt.Errorf("nightwatch: authentication failed: %w", err)
	}

	// Gzip the payload (matches agent behavior)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(jsonData); err != nil {
		return fmt.Errorf("nightwatch: gzip failed: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("nightwatch: gzip close failed: %w", err)
	}

	req, err := http.NewRequest("POST", ingestURL, &buf)
	if err != nil {
		return fmt.Errorf("nightwatch: failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Nightwatch-Server", h.server)
	req.Header.Set("User-Agent", "NightwatchGoAgent/0.1.0")

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("nightwatch: ingest request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("nightwatch: ingest failed with status %d: %s", resp.StatusCode, string(body))
	}

	if h.debug {
		fmt.Fprintf(os.Stderr, "nightwatch: ingest response [%d]: %s\n", resp.StatusCode, string(body))
	}

	return nil
}

func (h *HTTPIngest) getAuth() (string, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Return cached auth if still valid
	if h.authToken != "" && h.ingestURL != "" && time.Now().Before(h.authExpiry) {
		return h.authToken, h.ingestURL, nil
	}

	// Refresh authentication
	token, url, err := h.authenticate()
	if err != nil {
		return "", "", err
	}

	return token, url, nil
}

// authenticate exchanges the refresh token for a temporary ingest token.
// Matches: POST /api/agent-auth with Bearer {refreshToken}
// Response: { "token": "...", "expires_in": 3600, "ingest_url": "...", "refresh_in": 1800 }
func (h *HTTPIngest) authenticate() (string, string, error) {
	url := h.baseURL + "/api/agent-auth"

	req, err := http.NewRequest("POST", url, strings.NewReader("{}"))
	if err != nil {
		return "", "", fmt.Errorf("failed to create auth request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+h.refreshToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Nightwatch-Server", h.server)
	req.Header.Set("User-Agent", "NightwatchGoAgent/0.1.0")

	resp, err := h.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("auth request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read auth response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("auth failed with status %d: %s", resp.StatusCode, string(body))
	}

	var authResp struct {
		Token     string `json:"token"`
		ExpiresIn int    `json:"expires_in"`
		IngestURL string `json:"ingest_url"`
		RefreshIn int    `json:"refresh_in"`
	}

	if err := json.Unmarshal(body, &authResp); err != nil {
		return "", "", fmt.Errorf("failed to parse auth response: %w", err)
	}

	if authResp.Token == "" || authResp.IngestURL == "" {
		return "", "", fmt.Errorf("invalid auth response: missing token or ingest_url: %s", string(body))
	}

	// Cache the auth details
	h.authToken = authResp.Token
	h.ingestURL = authResp.IngestURL
	h.authExpiry = time.Now().Add(time.Duration(authResp.ExpiresIn) * time.Second)

	return authResp.Token, authResp.IngestURL, nil
}
