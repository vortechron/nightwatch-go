package nightwatch

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// endpoint is where batches go and the token they carry.
type endpoint struct {
	url     string
	token   string
	expires time.Time
}

type authResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
	IngestURL string `json:"ingest_url"`
}

// resolveEndpoint returns the ingest URL and token. An explicit IngestURL wins.
// Otherwise it asks POST {base}/api/agent-auth, and falls back to
// {base}/api/omni/ingest with the app token when that fails.
func (s *Sender) resolveEndpoint(ctx context.Context) endpoint {
	if s.cfg.IngestURL != "" {
		return endpoint{url: s.cfg.IngestURL, token: s.cfg.Token}
	}

	if s.target.url != "" && s.now().Before(s.target.expires) {
		return s.target
	}

	if found, ok := s.authenticate(ctx); ok {
		s.target = found

		return found
	}

	s.target = endpoint{
		url:     s.cfg.BaseURL + fallbackPath,
		token:   s.cfg.Token,
		expires: s.now().Add(fallbackRetry),
	}

	return s.target
}

func (s *Sender) authenticate(ctx context.Context) (endpoint, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.BaseURL+authPath, bytes.NewReader([]byte("{}")))
	if err != nil {
		return endpoint{}, false
	}

	req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Nightwatch-Server", s.cfg.Server)

	resp, err := s.client.Do(req)
	if err != nil {
		return endpoint{}, false
	}

	defer resp.Body.Close()

	var parsed authResponse
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&parsed) != nil || parsed.IngestURL == "" {
		return endpoint{}, false
	}

	token := parsed.Token
	if token == "" {
		token = s.cfg.Token
	}

	lifetime := time.Duration(parsed.ExpiresIn) * time.Second
	if lifetime <= time.Minute {
		lifetime = time.Minute
	}

	return endpoint{url: parsed.IngestURL, token: token, expires: s.now().Add(lifetime - time.Minute)}, true
}
