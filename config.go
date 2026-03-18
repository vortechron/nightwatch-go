package nightwatch

import (
	"os"
	"strconv"
	"time"
)

const (
	defaultAddress           = "127.0.0.1:2407"
	defaultConnectionTimeout = 500 * time.Millisecond
	defaultReadWriteTimeout  = 500 * time.Millisecond
	defaultEventBuffer       = 500
	defaultSampleRate        = 1.0
)

// Config holds all configuration for the Nightwatch instance.
type Config struct {
	Enabled    bool
	Token      string
	Deployment string
	Server     string

	// HTTP direct mode (set BaseURL to enable, bypasses local agent)
	BaseURL string // e.g., "https://omni.vortechron.com"

	// TCP agent mode (default, requires local agent on port 2407)
	Address           string
	ConnectionTimeout time.Duration
	ReadWriteTimeout  time.Duration
	EventBuffer       int

	// Sampling
	RequestSampleRate   float64
	ExceptionSampleRate float64

	// Filtering
	IgnoreQueries          bool
	IgnoreOutgoingRequests bool
	LogLevel               string // "debug", "info", "warning", "error"

	// Redaction
	RedactHeaders       []string
	RedactPayloadFields []string

	// Payload capture
	CapturePayload bool

	// Debug mode
	Debug bool
}

// Option is a functional option for configuring Nightwatch.
type Option func(*Config)

func defaultConfig() *Config {
	hostname, _ := os.Hostname()

	cfg := &Config{
		Enabled:             true,
		Server:              hostname,
		Address:             defaultAddress,
		ConnectionTimeout:   defaultConnectionTimeout,
		ReadWriteTimeout:    defaultReadWriteTimeout,
		EventBuffer:         defaultEventBuffer,
		RequestSampleRate:   defaultSampleRate,
		ExceptionSampleRate: defaultSampleRate,
		LogLevel:            "debug",
		RedactHeaders:       []string{"authorization", "cookie", "set-cookie"},
		RedactPayloadFields: []string{"_token", "password", "password_confirmation"},
	}

	// Apply environment variable overrides
	if v := os.Getenv("NIGHTWATCH_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("NIGHTWATCH_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("NIGHTWATCH_DEPLOY"); v != "" {
		cfg.Deployment = v
	}
	if v := os.Getenv("NIGHTWATCH_SERVER"); v != "" {
		cfg.Server = v
	}
	if v := os.Getenv("NIGHTWATCH_INGEST_URI"); v != "" {
		cfg.Address = v
	}
	if v := os.Getenv("NIGHTWATCH_REQUEST_SAMPLE_RATE"); v != "" {
		if rate, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.RequestSampleRate = rate
		}
	}
	if v := os.Getenv("NIGHTWATCH_EXCEPTION_SAMPLE_RATE"); v != "" {
		if rate, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.ExceptionSampleRate = rate
		}
	}

	return cfg
}

func WithEnabled(enabled bool) Option {
	return func(c *Config) { c.Enabled = enabled }
}

func WithToken(token string) Option {
	return func(c *Config) { c.Token = token }
}

func WithDeployment(deploy string) Option {
	return func(c *Config) { c.Deployment = deploy }
}

func WithServer(server string) Option {
	return func(c *Config) { c.Server = server }
}

func WithAddress(addr string) Option {
	return func(c *Config) { c.Address = addr }
}

func WithConnectionTimeout(d time.Duration) Option {
	return func(c *Config) { c.ConnectionTimeout = d }
}

func WithReadWriteTimeout(d time.Duration) Option {
	return func(c *Config) { c.ReadWriteTimeout = d }
}

func WithBufferSize(n int) Option {
	return func(c *Config) { c.EventBuffer = n }
}

func WithRequestSampleRate(rate float64) Option {
	return func(c *Config) { c.RequestSampleRate = rate }
}

func WithExceptionSampleRate(rate float64) Option {
	return func(c *Config) { c.ExceptionSampleRate = rate }
}

func WithIgnoreQueries(ignore bool) Option {
	return func(c *Config) { c.IgnoreQueries = ignore }
}

func WithIgnoreOutgoingRequests(ignore bool) Option {
	return func(c *Config) { c.IgnoreOutgoingRequests = ignore }
}

func WithLogLevel(level string) Option {
	return func(c *Config) { c.LogLevel = level }
}

func WithRedactHeaders(headers ...string) Option {
	return func(c *Config) { c.RedactHeaders = headers }
}

func WithRedactPayloadFields(fields ...string) Option {
	return func(c *Config) { c.RedactPayloadFields = fields }
}

func WithCapturePayload(capture bool) Option {
	return func(c *Config) { c.CapturePayload = capture }
}

func WithBaseURL(baseURL string) Option {
	return func(c *Config) { c.BaseURL = baseURL }
}

func WithDebug(debug bool) Option {
	return func(c *Config) { c.Debug = debug }
}
