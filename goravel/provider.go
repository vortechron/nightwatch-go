package goravel

import (
	"context"
	"time"

	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/foundation"

	nightwatch "github.com/vortechron/nightwatch-go"
)

const shutdownTimeout = 10 * time.Second

// NightwatchServiceProvider is the one call that wires Nightwatch into Goravel.
//
// List it after the framework providers (cache, queue, schedule, mail, auth,
// telemetry). Register binds the client in the container and points telemetry
// at Nightwatch. Boot starts the sender and wraps the cache, scheduler,
// console commands and queue job registry.
//
// Register panics with ErrExporterConflict when the app already configured a
// different telemetry exporter, because failing at boot is clearer than
// silently sending nothing.
type NightwatchServiceProvider struct {
	// Config overrides the environment. Nil reads the NIGHTWATCH_* variables.
	Config *nightwatch.Config
}

// Register binds the client and selects the Nightwatch exporters.
func (p NightwatchServiceProvider) Register(app foundation.Application) {
	app.Singleton(BindingClient, func(foundation.Application) (any, error) {
		if p.Config != nil {
			return nightwatch.New(*p.Config), nil
		}

		return nightwatch.NewFromEnv(), nil
	})

	client := ClientFrom(app)
	if client == nil || !client.Enabled() {
		return
	}

	if cfg := app.MakeConfig(); cfg != nil {
		if err := ConfigureTelemetry(cfg, client); err != nil {
			panic(err)
		}
	}
}

// Boot starts the client and wraps the framework services.
func (NightwatchServiceProvider) Boot(app foundation.Application) {
	client := ClientFrom(app)
	if client == nil || !client.Enabled() {
		// Disabled: nothing is traced and the cache, schedule and artisan are
		// left alone. Only the job registry still removes the envelope that a
		// tracing dispatcher may have added, so a job never sees it.
		if queue := app.MakeQueue(); queue != nil {
			app.Instance(binding.Queue, TolerateEnvelopes(queue))
			app.Fresh(binding.Queue)
		}

		return
	}

	client.Start()
	Wrap(app, client)
}

// Runners returns the shutdown hook that sends what is still buffered. It runs
// after Goravel's telemetry runner (priority 100), which hands the last spans
// to the exporters.
func (NightwatchServiceProvider) Runners(app foundation.Application) []foundation.Runner {
	return []foundation.Runner{&shutdownRunner{client: ClientFrom(app)}}
}

type shutdownRunner struct{ client *nightwatch.Client }

func (r *shutdownRunner) Signature() string { return "nightwatch" }

func (r *shutdownRunner) ShouldRun() bool { return r.client != nil && r.client.Enabled() }

func (r *shutdownRunner) Run() error { return nil }

func (r *shutdownRunner) ShutdownPriority() int { return 200 }

func (r *shutdownRunner) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	return r.client.Shutdown(ctx) //nolint:wrapcheck // plain shutdown error
}

// Wrap replaces the cache, artisan, schedule and queue bindings with
// instrumented versions. Resolving each binding first makes sure the original
// exists; Fresh then drops the cached instance so the next facade call gets
// the wrapper. Artisan is wrapped before schedule, which uses it to run
// scheduled commands.
func Wrap(app foundation.Application, client *nightwatch.Client) {
	if cache := app.MakeCache(); cache != nil {
		app.Instance(binding.Cache, WrapCache(cache))
	}

	artisan := app.MakeArtisan()
	if artisan != nil {
		artisan = WrapArtisan(artisan, flusher(client))
		app.Instance(binding.Artisan, artisan)
	}

	if schedule := app.MakeSchedule(); schedule != nil {
		app.Instance(binding.Schedule, WrapSchedule(schedule, artisan))
	}

	if queue := app.MakeQueue(); queue != nil {
		app.Instance(binding.Queue, WrapQueue(queue))
	}

	app.Fresh(binding.Cache, binding.Schedule, binding.Artisan, binding.Queue)
}
