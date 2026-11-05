package main

import (
	"goravel/app/facades"
	"goravel/bootstrap"
	"os"

	contractsqueue "github.com/goravel/framework/contracts/queue"
)

func main() {
	app := bootstrap.Boot()

	// Run a queue worker next to the web server.
	if queueName := os.Getenv("QUEUE_NAME"); queueName != "" {
		go func() {
			_ = facades.Queue().Worker(contractsqueue.Args{Connection: "redis", Queue: queueName, Concurrent: 1}).Run()
		}()
	}

	app.Start()
}
