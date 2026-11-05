package config

import (
	"goravel/app/facades"

	"github.com/goravel/framework/contracts/database/driver"
	sqlitefacades "github.com/goravel/sqlite/facades"
)

func init() {
	config := facades.Config()
	config.Add("database", map[string]any{
		"default": "sqlite",
		"connections": map[string]any{
			"sqlite": map[string]any{
				"database": config.Env("DB_DATABASE", "storage/example.sqlite"),
				"prefix":   "",
				"singular": false,
				"via": func() (driver.Driver, error) {
					return sqlitefacades.Sqlite("sqlite")
				},
			},
		},
		"pool": map[string]any{
			"max_idle_conns":    10,
			"max_open_conns":    100,
			"conn_max_idletime": 3600,
			"conn_max_lifetime": 3600,
		},
		"slow_threshold": 200,
		"migrations":     map[string]any{"table": "migrations"},
		// Redis: always DB 15 for this example.
		"redis": map[string]any{
			"default": map[string]any{
				"host":     config.Env("REDIS_HOST", "127.0.0.1"),
				"password": config.Env("REDIS_PASSWORD", ""),
				"port":     config.Env("REDIS_PORT", 6379),
				"database": config.Env("REDIS_DB", 15),
			},
		},
	})
}
