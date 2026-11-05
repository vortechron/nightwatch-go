package bootstrap

import (
	"goravel/app/console/commands"
	"goravel/app/facades"
	"goravel/app/jobs"
	"goravel/config"
	"goravel/routes"

	contractsconsole "github.com/goravel/framework/contracts/console"
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/contracts/foundation/configuration"
	contractshttp "github.com/goravel/framework/contracts/http"
	contractsqueue "github.com/goravel/framework/contracts/queue"
	contractsschedule "github.com/goravel/framework/contracts/schedule"
	"github.com/goravel/framework/foundation"
	telemetryhttp "github.com/goravel/framework/telemetry/instrumentation/http"

	nightwatch "github.com/vortechron/nightwatch-go"
	nwgoravel "github.com/vortechron/nightwatch-go/goravel"
)

func Boot() contractsfoundation.Application {
	return foundation.Setup().
		WithMiddleware(func(m configuration.Middleware) {
			// Goravel's telemetry middleware opens the request span; the
			// Nightwatch middleware adds path, headers, sizes and the user.
			m.Append(telemetryhttp.Telemetry(), nwgoravel.LazyMiddleware(facades.App, nwgoravel.WithUserResolver(resolveUser)))
		}).
		WithCommands(func() []contractsconsole.Command {
			return []contractsconsole.Command{&commands.Hello{}}
		}).
		WithJobs(func() []contractsqueue.Job {
			return []contractsqueue.Job{&jobs.SendWelcome{}}
		}).
		WithSchedule(func() []contractsschedule.Event {
			return []contractsschedule.Event{
				facades.Schedule().Call(routes.Tick).EverySecond().Name("tick"),
			}
		}).
		WithRouting(routes.Web).
		WithProviders(Providers).
		WithConfig(config.Boot).
		WithCallback(routes.Prepare).
		Create()
}

// resolveUser gives the signed-in user a name for Omni's user page. A real app
// would load the user from the database here.
func resolveUser(_ contractshttp.Context, id string) (nightwatch.User, bool) {
	return nightwatch.User{ID: id, Name: "Example User", Username: "user" + id}, true
}
