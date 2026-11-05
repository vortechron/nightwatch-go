package config

import (
	"goravel/app/facades"

	"github.com/goravel/framework/support/carbon"
)

func Boot() {}

func init() {
	config := facades.Config()
	config.Add("app", map[string]any{
		"name":            config.Env("APP_NAME", "nightwatch-example"),
		"env":             config.Env("APP_ENV", "local"),
		"debug":           config.Env("APP_DEBUG", false),
		"timezone":        carbon.UTC,
		"locale":          "en",
		"fallback_locale": "en",
		"key":             config.Env("APP_KEY", "01234567890123456789012345678901"),
	})
}
