package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	nightwatch "github.com/laravel/nightwatch-go"
	"github.com/laravel/nightwatch-go/gormhook"
	"github.com/laravel/nightwatch-go/middleware/ginmw"
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
	port := os.Getenv("PORT")
	if port == "" {
		port = "9093"
	}

	debug := os.Getenv("DEBUG") == "1"

	nw, err := nightwatch.New(
		nightwatch.WithToken("omni_X5M1ER9Fv8JNWM4Ef8NYkFs44Wwg8xxk7XofgXgV"),
		nightwatch.WithBaseURL("https://omni.vortechron.com"),
		nightwatch.WithDeployment("go-e2e-test"),
		nightwatch.WithServer("localhost"),
		nightwatch.WithDebug(debug),
	)
	if err != nil {
		log.Fatal(err)
	}

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	db.AutoMigrate(&Task{})

	if err := gormhook.Register(db, nw, gormhook.WithConnectionName("sqlite")); err != nil {
		log.Fatal(err)
	}

	// Async ping
	go func() {
		if err := nw.Ingester().Ping(); err != nil {
			fmt.Fprintf(os.Stderr, "Nightwatch ping failed: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "Nightwatch connected!")
		}
	}()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(ginmw.Middleware(nw))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	r.GET("/tasks", func(c *gin.Context) {
		var tasks []Task
		db.WithContext(c.Request.Context()).Find(&tasks)
		c.JSON(200, tasks)
	})

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

	r.GET("/tasks/:id", func(c *gin.Context) {
		var task Task
		if err := db.WithContext(c.Request.Context()).First(&task, c.Param("id")).Error; err != nil {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		c.JSON(200, task)
	})

	r.PUT("/tasks/:id", func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		var task Task
		if err := db.WithContext(c.Request.Context()).First(&task, id).Error; err != nil {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		var input struct {
			Title       *string `json:"title"`
			Description *string `json:"description"`
			Completed   *bool   `json:"completed"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
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
		db.WithContext(c.Request.Context()).Model(&task).Updates(updates)
		c.JSON(200, task)
	})

	r.DELETE("/tasks/:id", func(c *gin.Context) {
		result := db.WithContext(c.Request.Context()).Delete(&Task{}, c.Param("id"))
		if result.RowsAffected == 0 {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		c.JSON(200, gin.H{"message": "deleted"})
	})

	r.GET("/error-test", func(c *gin.Context) {
		nw.ReportError(c.Request.Context(), fmt.Errorf("intentional test error from Go"))
		c.JSON(500, gin.H{"error": "test error reported"})
	})

	fmt.Fprintf(os.Stderr, "Server on :%s (deploy=go-e2e-test)\n", port)
	r.Run(":" + port)
}
