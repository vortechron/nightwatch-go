package main

import (
	"bytes"
	"encoding/json"
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

// spyIngester wraps a real ingester and logs all records
type spyIngester struct {
	inner   nightwatch.Ingester
	records []nightwatch.Record
}

func (s *spyIngester) Write(record nightwatch.Record) error {
	s.records = append(s.records, record)
	prettyPrint(record)
	return s.inner.Write(record)
}
func (s *spyIngester) WriteNow(record nightwatch.Record) error {
	s.records = append(s.records, record)
	prettyPrint(record)
	return s.inner.WriteNow(record)
}
func (s *spyIngester) Flush()        { s.inner.Flush() }
func (s *spyIngester) Digest() error { return s.inner.Digest() }
func (s *spyIngester) Ping() error   { return s.inner.Ping() }

func prettyPrint(r nightwatch.Record) {
	t := r["t"]
	switch t {
	case "query":
		fmt.Printf("  [QUERY] sql=%q file=%v:%v duration=%vµs execution_preview=%q\n",
			r["sql"], r["file"], r["line"], r["duration"], r["execution_preview"])
	case "request":
		fmt.Printf("  [REQUEST] %v %v -> %v (%vµs) queries=%v\n",
			r["method"], r["route_path"], r["status_code"], r["duration"], r["queries"])
	case "exception":
		fmt.Printf("  [EXCEPTION] class=%v message=%q file=%v:%v handled=%v\n",
			r["class"], r["message"], r["file"], r["line"], r["handled"])
	default:
		j, _ := json.MarshalIndent(r, "  ", "  ")
		fmt.Printf("  [%v] %s\n", t, string(j))
	}
}

func main() {
	nw, err := nightwatch.New(
		nightwatch.WithToken("omni_X5M1ER9Fv8JNWM4Ef8NYkFs44Wwg8xxk7XofgXgV"),
		nightwatch.WithBaseURL("https://omni.vortechron.com"),
		nightwatch.WithDeployment("go-crud-debug"),
		nightwatch.WithServer("localhost"),
	)
	if err != nil {
		panic(err)
	}

	// Wrap the ingester with a spy to see all records
	spy := &spyIngester{inner: nw.Ingester()}
	nw.SetIngester(spy)

	db, _ := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	db.AutoMigrate(&Task{})
	gormhook.Register(db, nw, gormhook.WithConnectionName("sqlite"))

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(ginmw.Middleware(nw))

	r.POST("/tasks", func(c *gin.Context) {
		var input struct {
			Title       string `json:"title" binding:"required"`
			Description string `json:"description"`
		}
		c.ShouldBindJSON(&input)
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

	r.GET("/error-test", func(c *gin.Context) {
		nw.ReportError(c.Request.Context(), fmt.Errorf("intentional test error"))
		c.JSON(500, gin.H{"error": "test"})
	})

	go r.Run(":9091")
	time.Sleep(500 * time.Millisecond)

	fmt.Println("=== POST /tasks (creates INSERT query) ===")
	doReq("POST", "http://localhost:9091/tasks", `{"title":"Hello","description":"World"}`)
	time.Sleep(2 * time.Second)

	fmt.Println("\n=== GET /tasks (creates SELECT query) ===")
	doReq("GET", "http://localhost:9091/tasks", "")
	time.Sleep(2 * time.Second)

	fmt.Println("\n=== GET /tasks/1 (creates SELECT...WHERE query) ===")
	doReq("GET", "http://localhost:9091/tasks/1", "")
	time.Sleep(2 * time.Second)

	fmt.Println("\n=== GET /error-test (creates exception record) ===")
	doReq("GET", "http://localhost:9091/error-test", "")
	time.Sleep(2 * time.Second)

	fmt.Printf("\n=== Total records captured: %d ===\n", len(spy.records))
	for _, r := range spy.records {
		fmt.Printf("  type=%v trace=%v\n", r["t"], r["trace_id"])
	}

	fmt.Println("\nWaiting for final flush...")
	time.Sleep(5 * time.Second)
	fmt.Println("Done!")
}

func doReq(method, url, body string) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}
	req, _ := http.NewRequest(method, url, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	fmt.Printf("  Response: [%d] %s\n", resp.StatusCode, string(b))
}
