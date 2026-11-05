package goravel

import (
	"context"
	"time"

	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/foundation"
)

const shutdownTimeout = 10 * time.Second

// ServiceProvider is the one call that wires Nightwatch into Goravel.
//
// List it after the framework providers (cache, queue, schedule, mail, auth,
// telemetry). Register points telemetry at Nightwatch. Boot starts the sender
// and wraps the cache, scheduler, console commands and queue job registry.
type ServiceProvider struct{}

// Register selects the Nightwatch exporters for traces and logs.
func (ServiceProvider) Register(app foundation.Application) {
	if !Client().Enabled() {
		return
	}

	if cfg := app.MakeConfig(); cfg != nil {
		ConfigureTelemetry(cfg)
	}
}

// Boot starts the client and wraps the framework services.
func (ServiceProvider) Boot(app foundation.Application) {
	if !Client().Enabled() {
		return
	}

	Client().Start()
	Wrap(app)
}

// Runners returns the shutdown hook that sends what is still buffered. It runs
// after Goravel's telemetry runner (priority 100), which hands the last spans
// to the exporters.
func (ServiceProvider) Runners(foundation.Application) []foundation.Runner {
	return []foundation.Runner{&shutdownRunner{}}
}

type shutdownRunner struct{}

func (r *shutdownRunner) Signature() string { return "nightwatch" }

func (r *shutdownRunner) ShouldRun() bool { return Client().Enabled() }

func (r *shutdownRunner) Run() error { return nil }

func (r *shutdownRunner) ShutdownPriority() int { return 200 }

func (r *shutdownRunner) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	return Client().Shutdown(ctx) //nolint:wrapcheck // plain shutdown error
}

// Wrap replaces the cache, schedule, artisan and queue bindings with
// instrumented versions. Resolving each binding first makes sure the original
// exists; Fresh then drops the cached instance so the next facade call gets
// the wrapper.
func Wrap(app foundation.Application) {
	if cache := app.MakeCache(); cache != nil {
		app.Instance(binding.Cache, WrapCache(cache))
	}

	if schedule := app.MakeSchedule(); schedule != nil {
		app.Instance(binding.Schedule, WrapSchedule(schedule))
	}

	if artisan := app.MakeArtisan(); artisan != nil {
		app.Instance(binding.Artisan, WrapArtisan(artisan))
	}

	if queue := app.MakeQueue(); queue != nil {
		app.Instance(binding.Queue, WrapQueue(queue))
	}

	app.Fresh(binding.Cache, binding.Schedule, binding.Artisan, binding.Queue)
}
