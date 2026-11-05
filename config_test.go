package nightwatch

import (
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	cfg := ConfigFromEnv(env(map[string]string{"NIGHTWATCH_TOKEN": "tok"}), "host-1")
	if !cfg.Enabled || cfg.Token != "tok" {
		t.Fatalf("enabled/token: %+v", cfg)
	}
	if cfg.Server != "host-1" || cfg.BaseURL != "https://nightwatch.laravel.com" {
		t.Fatalf("server/base: %q %q", cfg.Server, cfg.BaseURL)
	}
	if cfg.RequestSampleRate != 1 || cfg.ExceptionSampleRate != 1 || cfg.CommandSampleRate != 1 || cfg.ScheduledTaskSampleRate != 1 {
		t.Fatalf("rates: %+v", cfg)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("log level %q", cfg.LogLevel)
	}
	if cfg.BatchSize != 500 || cfg.FlushInterval != 5*time.Second || cfg.TraceMaxWait != 10*time.Second {
		t.Fatalf("batching: %+v", cfg)
	}
	want := []string{"Authorization", "Cookie", "Proxy-Authorization", "X-XSRF-TOKEN"}
	if len(cfg.RedactHeaders) != 4 || cfg.RedactHeaders[3] != want[3] {
		t.Fatalf("redact: %v", cfg.RedactHeaders)
	}
}

func TestConfigFromEnv_Overrides(t *testing.T) {
	cfg := ConfigFromEnv(env(map[string]string{
		"NIGHTWATCH_ENABLED":                  "false",
		"NIGHTWATCH_TOKEN":                    "t",
		"NIGHTWATCH_BASE_URL":                 "http://127.0.0.1:2601/",
		"NIGHTWATCH_DEPLOY":                   "d1",
		"NIGHTWATCH_SERVER":                   "web",
		"NIGHTWATCH_REQUEST_SAMPLE_RATE":      "0.25",
		"NIGHTWATCH_EXCEPTION_SAMPLE_RATE":    "bad",
		"NIGHTWATCH_IGNORE_QUERIES":           "true",
		"NIGHTWATCH_IGNORE_MAIL":              "1",
		"NIGHTWATCH_IGNORE_CACHE_EVENTS":      "true",
		"NIGHTWATCH_IGNORE_NOTIFICATIONS":     "true",
		"NIGHTWATCH_IGNORE_OUTGOING_REQUESTS": "true",
		"LOG_LEVEL":                           "info",
		"NIGHTWATCH_REDACT_HEADERS":           "X-A, X-B",
	}), "ignored")
	if cfg.Enabled {
		t.Fatal("should be disabled")
	}
	if cfg.BaseURL != "http://127.0.0.1:2601" || cfg.Deploy != "d1" || cfg.Server != "web" {
		t.Fatalf("%+v", cfg)
	}
	if cfg.RequestSampleRate != 0.25 || cfg.ExceptionSampleRate != 1 {
		t.Fatalf("rates %v %v", cfg.RequestSampleRate, cfg.ExceptionSampleRate)
	}
	if !cfg.IgnoreQueries || !cfg.IgnoreMail || !cfg.IgnoreCacheEvents || !cfg.IgnoreNotifications || !cfg.IgnoreOutgoingRequests {
		t.Fatalf("ignores %+v", cfg)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("level %q", cfg.LogLevel)
	}
	if len(cfg.RedactHeaders) != 2 || cfg.RedactHeaders[1] != "X-B" {
		t.Fatalf("redact %v", cfg.RedactHeaders)
	}
}

func TestConfigFromEnv_NoTokenDisables(t *testing.T) {
	if ConfigFromEnv(env(nil), "h").Enabled {
		t.Fatal("no token should disable")
	}
}

func TestLogLevelAllowed(t *testing.T) {
	cfg := Config{LogLevel: "warning"}
	for level, want := range map[string]bool{"debug": false, "info": false, "warning": true, "error": true, "emergency": true, "weird": true} {
		if got := cfg.LogAllowed(level); got != want {
			t.Errorf("%s: got %v want %v", level, got, want)
		}
	}
}
