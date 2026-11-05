package bootstrap

import (
	"github.com/goravel/framework/auth"
	"github.com/goravel/framework/cache"
	"github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/database"
	"github.com/goravel/framework/event"
	"github.com/goravel/framework/http"
	"github.com/goravel/framework/log"
	"github.com/goravel/framework/mail"
	"github.com/goravel/framework/queue"
	"github.com/goravel/framework/route"
	"github.com/goravel/framework/schedule"
	"github.com/goravel/framework/telemetry"
	"github.com/goravel/gin"
	"github.com/goravel/redis"
	"github.com/goravel/sqlite"

	nwgoravel "github.com/vortechron/nightwatch-go/goravel"
)

func Providers() []foundation.ServiceProvider {
	return []foundation.ServiceProvider{
		&log.ServiceProvider{},
		&cache.ServiceProvider{},
		&http.ServiceProvider{},
		&route.ServiceProvider{},
		&gin.ServiceProvider{},
		&database.ServiceProvider{},
		&sqlite.ServiceProvider{},
		&auth.ServiceProvider{},
		&queue.ServiceProvider{},
		&redis.ServiceProvider{},
		&event.ServiceProvider{},
		&mail.ServiceProvider{},
		&schedule.ServiceProvider{},
		&telemetry.ServiceProvider{},
		// Last: it points telemetry at Nightwatch and wraps the services above.
		&nwgoravel.ServiceProvider{},
	}
}
