package config

import (
	"goravel/app/facades"
)

func init() {
	config := facades.Config()
	config.Add("mail", map[string]any{
		"host": config.Env("MAIL_HOST", "127.0.0.1"),
		"port": config.Env("MAIL_PORT", 1025),
		"from": map[string]any{
			"address": config.Env("MAIL_FROM_ADDRESS", "hello@example.test"),
			"name":    config.Env("MAIL_FROM_NAME", "Nightwatch Example"),
		},
		"username": config.Env("MAIL_USERNAME", ""),
		"password": config.Env("MAIL_PASSWORD", ""),
	})
}
