package config

import (
	"goravel/app/facades"

	"github.com/goravel/framework/contracts/queue"
	redisfacades "github.com/goravel/redis/facades"
)

func init() {
	config := facades.Config()
	config.Add("queue", map[string]any{
		"default": "redis",
		"connections": map[string]any{
			"sync": map[string]any{"driver": "sync"},
			"redis": map[string]any{
				"driver":     "custom",
				"connection": "default",
				"queue":      config.Env("QUEUE_NAME", "default"),
				"concurrent": 1,
				"via": func() (queue.Driver, error) {
					return redisfacades.Queue("redis")
				},
			},
		},
	})
}
