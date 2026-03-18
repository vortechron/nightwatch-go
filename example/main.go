package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	nightwatch "github.com/laravel/nightwatch-go"
	"github.com/laravel/nightwatch-go/gormhook"
	"github.com/laravel/nightwatch-go/middleware/ginmw"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Task is the GORM model.
type Task struct {
	ID          uint   `json:"id" gorm:"primaryKey"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Completed   bool   `json:"completed"`
}

var db *gorm.DB

func main() {
	// 1. Initialize Nightwatch
	nw, err := nightwatch.New(
		nightwatch.WithToken("omni_X5M1ER9Fv8JNWM4Ef8NYkFs44Wwg8xxk7XofgXgV"),
		nightwatch.WithBaseURL("https://omni.vortechron.com"),
		nightwatch.WithDeployment("go-crud-example"),
		nightwatch.WithServer("localhost"),
	)
	if err != nil {
		log.Fatalf("Failed to init Nightwatch: %v", err)
	}

	// 2. Initialize GORM with SQLite
	db, err = gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	// Auto-migrate the schema
	db.AutoMigrate(&Task{})

	// 3. Register Nightwatch GORM hook — captures all query telemetry
	if err := gormhook.Register(db, nw, gormhook.WithConnectionName("sqlite")); err != nil {
		log.Fatalf("Failed to register GORM hook: %v", err)
	}

	// 4. Async connectivity check
	go func() {
		if err := nw.Ingester().Ping(); err != nil {
			fmt.Printf("Nightwatch ping failed: %v\n", err)
		} else {
			fmt.Println("Nightwatch connected!")
		}
	}()

	// 5. Set up Gin
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(ginmw.Middleware(nw))

	// CRUD routes
	r.GET("/tasks", listTasks)
	r.POST("/tasks", createTask)
	r.GET("/tasks/:id", getTask)
	r.PUT("/tasks/:id", updateTask)
	r.DELETE("/tasks/:id", deleteTask)
	r.GET("/health", healthCheck)
	r.GET("/error-test", errorTest(nw))

	fmt.Println("Server on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}

func listTasks(c *gin.Context) {
	var tasks []Task
	// WithContext propagates the nightwatch execution state to GORM,
	// so query records are linked to this HTTP request's trace.
	if err := db.WithContext(c.Request.Context()).Find(&tasks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, tasks)
}

func createTask(c *gin.Context) {
	var input struct {
		Title       string `json:"title" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	task := Task{Title: input.Title, Description: input.Description}
	if err := db.WithContext(c.Request.Context()).Create(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, task)
}

func getTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var task Task
	if err := db.WithContext(c.Request.Context()).First(&task, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, task)
}

func updateTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var task Task
	if err := db.WithContext(c.Request.Context()).First(&task, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	var input struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Completed   *bool   `json:"completed"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]any{}
	if input.Title != nil {
		updates["title"] = *input.Title
	}
	if input.Description != nil {
		updates["description"] = *input.Description
	}
	if input.Completed != nil {
		updates["completed"] = *input.Completed
	}

	if err := db.WithContext(c.Request.Context()).Model(&task).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, task)
}

func deleteTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	result := db.WithContext(c.Request.Context()).Delete(&Task{}, id)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "task deleted"})
}

func healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func errorTest(nw *nightwatch.Nightwatch) gin.HandlerFunc {
	return func(c *gin.Context) {
		nw.ReportError(c.Request.Context(), fmt.Errorf("test error from Go CRUD app"))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "test error reported"})
	}
}
