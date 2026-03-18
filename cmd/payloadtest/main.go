package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	nightwatch "github.com/laravel/nightwatch-go"
	"github.com/laravel/nightwatch-go/gormhook"
	"github.com/laravel/nightwatch-go/middleware/ginmw"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type Task struct {
	ID    uint   `json:"id" gorm:"primaryKey"`
	Title string `json:"title"`
}

func main() {
	nw, _ := nightwatch.New(
		nightwatch.WithToken("omni_X5M1ER9Fv8JNWM4Ef8NYkFs44Wwg8xxk7XofgXgV"),
		nightwatch.WithBaseURL("https://omni.vortechron.com"),
		nightwatch.WithDeployment("go-payload-test"),
		nightwatch.WithServer("localhost"),
		nightwatch.WithDebug(true),
	)

	db, _ := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	db.AutoMigrate(&Task{})
	gormhook.Register(db, nw, gormhook.WithConnectionName("sqlite"))

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(ginmw.Middleware(nw))

	r.GET("/test", func(c *gin.Context) {
		var tasks []Task
		db.WithContext(c.Request.Context()).Find(&tasks)
		c.JSON(200, tasks)
	})

	go r.Run(":9092")
	time.Sleep(500 * time.Millisecond)

	fmt.Println("=== Sending single request to capture payload ===")
	resp, _ := http.Get("http://localhost:9092/test")
	resp.Body.Close()
	fmt.Printf("Response: %d\n", resp.StatusCode)

	// Wait for async ingestion
	time.Sleep(5 * time.Second)
	fmt.Println("\n=== Done ===")
}
