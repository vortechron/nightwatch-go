package capture

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const uid = "0b9e3f6a-6d0c-4d6e-9d77-1d2a5c0f8a11"

func newServer(t *testing.T, token string) (*Server, *httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(dir, token, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	return s, srv, dir
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()

	return b.Bytes()
}

func post(t *testing.T, url, token, key string, body []byte, encoding string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if encoding != "" {
		req.Header.Set("Content-Encoding", encoding)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)

	return resp.StatusCode, out
}

func TestIngest_AcceptsAndSavesBatch(t *testing.T) {
	s, srv, dir := newServer(t, "")
	body := gz(t, `{"records":[{"t":"log","trace_id":"`+uid+`","level":"info","message":"hi","context":"{}","duration":12}]}`)
	status, out := post(t, srv.URL+"/api/omni/ingest", "tok", "k1", body, "gzip")
	if status != 200 || out["received"] != float64(1) {
		t.Fatalf("%d %v", status, out)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "batch-*.json"))
	if len(files) != 1 || s.Accepted() != 1 {
		t.Fatalf("files %v", files)
	}
	saved, _ := os.ReadFile(files[0])
	if !strings.Contains(string(saved), `"duration":12`) || !strings.HasPrefix(string(saved), `{"records":[`) {
		t.Fatalf("saved: %s", saved)
	}
}

func TestIngest_DuplicateKeyNotSavedTwice(t *testing.T) {
	_, srv, dir := newServer(t, "")
	body := []byte(`{"records":[{"t":"log","trace_id":"` + uid + `"}]}`)
	post(t, srv.URL+"/api/omni/ingest", "tok", "same", body, "")
	_, out := post(t, srv.URL+"/api/omni/ingest", "tok", "same", body, "")
	if out["duplicate"] != true {
		t.Fatalf("%v", out)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "batch-*.json")); len(files) != 1 {
		t.Fatalf("files %d", len(files))
	}
}

func TestIngest_RejectsLikeOmni(t *testing.T) {
	s, srv, dir := newServer(t, "")
	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"unknown type", `{"records":[{"t":"nope"}]}`, 422, "UNSUPPORTED_EVENT_TYPE"},
		{"null boolean", `{"records":[{"t":"exception","handled":null}]}`, 422, ""},
		{"bad uuid", `{"records":[{"t":"request","trace_id":"x"}]}`, 503, "STORAGE_UNAVAILABLE"},
		{"bad json", `nope`, 400, ""},
	}
	for _, tt := range tests {
		status, out := post(t, srv.URL+"/api/omni/ingest", "tok", "", []byte(tt.body), "")
		if status != tt.status {
			t.Errorf("%s: status %d %v", tt.name, status, out)
		}
		if tt.code != "" && out["code"] != tt.code {
			t.Errorf("%s: code %v", tt.name, out["code"])
		}
	}
	if len(s.Rejected()) != 4 {
		t.Fatalf("rejected %d", len(s.Rejected()))
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "batch-*.json")); len(files) != 0 {
		t.Fatal("rejected batches must not be saved")
	}
}

func TestAuthAndToken(t *testing.T) {
	_, srv, _ := newServer(t, "secret")
	if status, _ := post(t, srv.URL+"/api/omni/ingest", "", "", []byte(`{}`), ""); status != 401 {
		t.Fatalf("no token: %d", status)
	}
	if status, _ := post(t, srv.URL+"/api/omni/ingest", "wrong", "", []byte(`{}`), ""); status != 401 {
		t.Fatalf("wrong token: %d", status)
	}
	status, out := post(t, srv.URL+"/api/agent-auth", "secret", "", []byte(`{}`), "")
	if status != 200 || out["ingest_url"] != srv.URL+"/api/omni/ingest" || out["token"] != "secret" {
		t.Fatalf("auth: %d %v", status, out)
	}
	if status, out = post(t, srv.URL+"/api/omni/ingest", "secret", "", []byte(`{"records":[]}`), ""); status != 200 || out["received"] != float64(0) {
		t.Fatalf("empty: %d %v", status, out)
	}
	pingReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/omni/ping", nil)
	resp, err := http.DefaultClient.Do(pingReq)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("ping: %v", err)
	}
	resp.Body.Close()
}
