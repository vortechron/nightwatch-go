package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	nightwatch "github.com/laravel/nightwatch-go"
	"github.com/laravel/nightwatch-go/gormhook"
	"github.com/laravel/nightwatch-go/middleware/ginmw"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type Task struct {
	ID          uint   `json:"id" gorm:"primaryKey"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Completed   bool   `json:"completed"`
}

func main() {
	// Initialize Nightwatch
	nw, err := nightwatch.New(
		nightwatch.WithToken("omni_X5M1ER9Fv8JNWM4Ef8NYkFs44Wwg8xxk7XofgXgV"),
		nightwatch.WithBaseURL("https://omni.vortechron.com"),
		nightwatch.WithDeployment("go-crud-e2e-test"),
		nightwatch.WithServer("localhost"),
	)
	if err != nil {
		panic(err)
	}

	// Test auth
	fmt.Println("=== Testing Authentication ===")
	if err := nw.Ingester().Ping(); err != nil {
		fmt.Printf("FAIL: %v\n", err)
		return
	}
	fmt.Println("OK: Authentication successful")

	// Initialize GORM + SQLite
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		panic(err)
	}
	db.AutoMigrate(&Task{})

	// Register GORM hook
	if err := gormhook.Register(db, nw, gormhook.WithConnectionName("sqlite")); err != nil {
		panic(err)
	}

	// Set up Gin
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(ginmw.Middleware(nw))

	r.POST("/tasks", func(c *gin.Context) {
		var input struct {
			Title       string `json:"title" binding:"required"`
			Description string `json:"description"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		task := Task{Title: input.Title, Description: input.Description}
		db.WithContext(c.Request.Context()).Create(&task)
		c.JSON(201, task)
	})

	r.GET("/tasks", func(c *gin.Context) {
		var tasks []Task
		db.WithContext(c.Request.Context()).Find(&tasks)
		c.JSON(200, tasks)
	})

	r.GET("/tasks/:id", func(c *gin.Context) {
		var task Task
		if err := db.WithContext(c.Request.Context()).First(&task, c.Param("id")).Error; err != nil {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		c.JSON(200, task)
	})

	// Start server in background
	go r.Run(":9090")
	time.Sleep(1 * time.Second)

	fmt.Println("\n=== Running CRUD Operations ===")

	// Create task
	fmt.Println("\n--- POST /tasks ---")
	resp := doRequest("POST", "http://localhost:9090/tasks",
		`{"title":"Test task from Go","description":"GORM + SQLite query tracking"}`)
	fmt.Println(resp)

	// Create another
	fmt.Println("\n--- POST /tasks ---")
	resp = doRequest("POST", "http://localhost:9090/tasks",
		`{"title":"Another task","description":"More data"}`)
	fmt.Println(resp)

	// List tasks (triggers SELECT)
	fmt.Println("\n--- GET /tasks ---")
	resp = doRequest("GET", "http://localhost:9090/tasks", "")
	fmt.Println(resp)

	// Get single task (triggers SELECT ... WHERE)
	fmt.Println("\n--- GET /tasks/1 ---")
	resp = doRequest("GET", "http://localhost:9090/tasks/1", "")
	fmt.Println(resp)

	// Wait for async ingestion to complete
	fmt.Println("\n=== Waiting for ingestion (8s)... ===")
	time.Sleep(8 * time.Second)

	fmt.Println("\n=== DONE ===")
	fmt.Println("Check your Omni dashboard at https://omni.vortechron.com")
	fmt.Println("You should see:")
	fmt.Println("  - 4 HTTP request records (2x POST, 1x GET /tasks, 1x GET /tasks/1)")
	fmt.Println("  - Multiple query records (INSERT INTO tasks, SELECT * FROM tasks, etc.)")
	fmt.Println("  - Each query linked to its parent request via trace_id")
	fmt.Println("  - File/line pointing to example code, not GORM internals")
}

func doRequest(method, url, body string) string {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return fmt.Sprintf("ERROR creating request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Sprintf("[%d] %s", resp.StatusCode, string(respBody))
}
