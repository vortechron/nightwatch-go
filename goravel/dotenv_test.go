package goravel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"

	goravelconfig "github.com/goravel/framework/config"
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	mocksfoundation "github.com/goravel/framework/mocks/foundation"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// In a Goravel app most settings live in .env, which Goravel's config reads but
// does not export to the process environment.
func clientFromApp(t *testing.T, app *mocksfoundation.Application) *nightwatch.Client {
	t.Helper()
	var factory func(contractsfoundation.Application) (any, error)
	app.EXPECT().Singleton(BindingClient, mock.Anything).Run(func(_ any, cb func(contractsfoundation.Application) (any, error)) { factory = cb })
	app.EXPECT().Make(BindingClient).Return(nil, os.ErrNotExist).Maybe()
	app.EXPECT().MakeConfig().Return(nil).Maybe() // Register finds no config to tune; only the factory is under test

	NightwatchServiceProvider{}.Register(app)

	built, err := factory(app)
	if err != nil {
		t.Fatal(err)
	}

	return built.(*nightwatch.Client)
}

func TestProvider_ReadsNightwatchSettingsFromDotEnvThroughGoravelConfig(t *testing.T) {
	for _, key := range []string{"NIGHTWATCH_TOKEN", "NIGHTWATCH_BASE_URL", "NIGHTWATCH_SERVER", "NIGHTWATCH_REQUEST_SAMPLE_RATE", "NIGHTWATCH_ENABLED"} {
		t.Setenv(key, "")
		_ = os.Unsetenv(key) // the values must come from the file, not the process
	}

	envFile := filepath.Join(t.TempDir(), ".env")
	content := "APP_KEY=01234567890123456789012345678901\n" +
		"NIGHTWATCH_TOKEN=dotenv-token\nNIGHTWATCH_BASE_URL=http://omni.dotenv.test/\nNIGHTWATCH_SERVER=dotenv-server\n" +
		"NIGHTWATCH_REQUEST_SAMPLE_RATE=0.25\nNIGHTWATCH_IGNORE_QUERIES=true\n"
	if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("NIGHTWATCH_TOKEN") != "" {
		t.Fatal("test setup: the token must not be in the process environment")
	}

	var factory func(contractsfoundation.Application) (any, error)
	app := mocksfoundation.NewApplication(t)
	app.EXPECT().Singleton(BindingClient, mock.Anything).Run(func(_ any, cb func(contractsfoundation.Application) (any, error)) { factory = cb })
	app.EXPECT().Make(BindingClient).Return(nil, os.ErrNotExist).Maybe()
	app.EXPECT().MakeConfig().Return(goravelconfig.NewApplication(envFile)).Maybe()

	NightwatchServiceProvider{}.Register(app)

	built, err := factory(app)
	if err != nil {
		t.Fatal(err)
	}
	cfg := built.(*nightwatch.Client).Config()

	if !cfg.Enabled || cfg.Token != "dotenv-token" {
		t.Fatalf("the .env token must enable the client: enabled=%v token=%q", cfg.Enabled, cfg.Token)
	}
	if cfg.BaseURL != "http://omni.dotenv.test" || cfg.Server != "dotenv-server" || cfg.RequestSampleRate != 0.25 || !cfg.IgnoreQueries {
		t.Fatalf("settings from .env: %+v", cfg)
	}
}

func TestProvider_FallsBackToTheProcessEnvironment(t *testing.T) {
	t.Setenv("NIGHTWATCH_TOKEN", "process-token")
	t.Setenv("NIGHTWATCH_SERVER", "process-server")

	// clientFromApp gives the provider no config at all: only the process
	// environment is left.
	app := mocksfoundation.NewApplication(t)
	client := clientFromApp(t, app)
	if cfg := client.Config(); !cfg.Enabled || cfg.Token != "process-token" || cfg.Server != "process-server" {
		t.Fatalf("process environment: %+v", cfg)
	}
}

func TestProvider_ConfigOverrideStillWinsOverEverything(t *testing.T) {
	t.Setenv("NIGHTWATCH_TOKEN", "process-token")
	var factory func(contractsfoundation.Application) (any, error)
	app := mocksfoundation.NewApplication(t)
	app.EXPECT().Singleton(BindingClient, mock.Anything).Run(func(_ any, cb func(contractsfoundation.Application) (any, error)) { factory = cb })
	app.EXPECT().Make(BindingClient).Return(nil, os.ErrNotExist).Maybe()

	NightwatchServiceProvider{Config: &nightwatch.Config{Enabled: false}}.Register(app)
	built, _ := factory(app)
	if built.(*nightwatch.Client).Enabled() {
		t.Fatal("an explicit Config must not be replaced by any environment")
	}
}
