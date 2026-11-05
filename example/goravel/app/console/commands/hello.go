package commands

import (
	"goravel/app/facades"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
)

type Hello struct{}

func (c *Hello) Signature() string   { return "hello" }
func (c *Hello) Description() string { return "Runs one query and logs a line" }
func (c *Hello) Extend() command.Extend {
	return command.Extend{Category: "example"}
}

func (c *Hello) Handle(ctx console.Context) error {
	var rows []map[string]any
	if err := facades.Orm().WithContext(ctx).Query().Raw("select * from tasks").Scan(&rows); err != nil {
		return err
	}

	facades.Log().WithContext(ctx).Info("hello from the command")

	return nil
}
