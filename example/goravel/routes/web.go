package routes

import (
	"goravel/app/facades"
	"goravel/app/http/controllers"
)

// Prepare creates the demo table and one row.
func Prepare() {
	query := facades.Orm().Query()
	_, _ = query.Exec("create table if not exists tasks (id integer primary key, title text)")
	_, _ = query.Exec("insert or ignore into tasks (id, title) values (7, 'write the report')")
}

func Web() {
	tasks := controllers.NewTaskController()
	route := facades.Route()

	route.Get("/api/tasks/{id}", tasks.Show)
	route.Get("/api/boom", tasks.Boom)
	route.Get("/api/handled", tasks.Handled)
	route.Post("/api/welcome", tasks.Welcome)
	route.Post("/api/mail", tasks.Mail)
	route.Get("/api/outgoing", tasks.Outgoing)
	route.Post("/api/login", tasks.Login)
	route.Get("/api/me", tasks.Me)
	route.Post("/api/notify", tasks.Notify)
}

// Tick is the scheduled task.
func Tick() {
	facades.Log().Info("tick")
}
