package main

import (
	"fmt"

	nightwatch "github.com/laravel/nightwatch-go"
)

func main() {
	nw, err := nightwatch.New(
		nightwatch.WithToken("omni_X5M1ER9Fv8JNWM4Ef8NYkFs44Wwg8xxk7XofgXgV"),
		nightwatch.WithBaseURL("https://omni.vortechron.com"),
		nightwatch.WithDeployment("go-crud-example"),
		nightwatch.WithServer("localhost"),
	)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Pinging Nightwatch backend...")
	if err := nw.Ingester().Ping(); err != nil {
		fmt.Println("Ping failed:", err)
	} else {
		fmt.Println("Ping OK - authentication successful!")
	}
}
