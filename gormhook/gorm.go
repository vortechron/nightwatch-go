package gormhook

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	nightwatch "github.com/laravel/nightwatch-go"
	"github.com/laravel/nightwatch-go/sensor"
	"gorm.io/gorm"
)

type startTimeKey struct{}
type callerInfoKey struct{}

type callerInfo struct {
	File string
	Line int
}

// Config holds GORM hook configuration.
type Config struct {
	ConnectionName string // e.g. "sqlite", "mysql", "postgres"
	ConnectionType string // "read", "write", or "" (unknown)
	BasePath       string // project root path to strip from file paths
}

// Option configures the GORM hook.
type Option func(*Config)

// WithConnectionName sets the connection name reported in query records.
func WithConnectionName(name string) Option {
	return func(c *Config) { c.ConnectionName = name }
}

// WithConnectionType sets the connection type ("read", "write", or "").
func WithConnectionType(connType string) Option {
	return func(c *Config) { c.ConnectionType = connType }
}

// WithBasePath sets the project root to strip from file paths.
func WithBasePath(path string) Option {
	return func(c *Config) { c.BasePath = path }
}

// Register attaches Nightwatch query instrumentation to a GORM database instance.
// It registers before/after callbacks on all GORM operations to capture SQL,
// duration, and caller file/line.
//
// IMPORTANT: For queries to be linked to HTTP requests, pass the request context
// to GORM via db.WithContext(c.Request.Context()).
//
// Usage:
//
//	db, _ := gorm.Open(sqlite.Open("app.db"), &gorm.Config{})
//	gormhook.Register(db, nw, gormhook.WithConnectionName("sqlite"))
func Register(db *gorm.DB, nw *nightwatch.Nightwatch, opts ...Option) error {
	cfg := &Config{
		ConnectionName: "default",
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Auto-detect base path
	if cfg.BasePath == "" {
		if wd, err := os.Getwd(); err == nil {
			cfg.BasePath = wd + string(filepath.Separator)
		}
	} else if !strings.HasSuffix(cfg.BasePath, string(filepath.Separator)) {
		cfg.BasePath += string(filepath.Separator)
	}

	before := makeBefore(nw)
	after := makeAfter(nw, cfg)

	// Register on all GORM callback types.
	// Each operation (Create, Query, Update, Delete, Row, Raw) gets a before/after pair.
	type regPair struct {
		beforeName, afterName, hookPoint string
	}
	ops := []regPair{
		{"nightwatch:before_create", "nightwatch:after_create", "create"},
		{"nightwatch:before_query", "nightwatch:after_query", "query"},
		{"nightwatch:before_update", "nightwatch:after_update", "update"},
		{"nightwatch:before_delete", "nightwatch:after_delete", "delete"},
		{"nightwatch:before_row", "nightwatch:after_row", "row"},
		{"nightwatch:before_raw", "nightwatch:after_raw", "raw"},
	}

	for _, op := range ops {
		var err error
		switch op.hookPoint {
		case "create":
			err = db.Callback().Create().Before("gorm:create").Register(op.beforeName, before)
			if err == nil {
				err = db.Callback().Create().After("gorm:create").Register(op.afterName, after)
			}
		case "query":
			err = db.Callback().Query().Before("gorm:query").Register(op.beforeName, before)
			if err == nil {
				err = db.Callback().Query().After("gorm:query").Register(op.afterName, after)
			}
		case "update":
			err = db.Callback().Update().Before("gorm:update").Register(op.beforeName, before)
			if err == nil {
				err = db.Callback().Update().After("gorm:update").Register(op.afterName, after)
			}
		case "delete":
			err = db.Callback().Delete().Before("gorm:delete").Register(op.beforeName, before)
			if err == nil {
				err = db.Callback().Delete().After("gorm:delete").Register(op.afterName, after)
			}
		case "row":
			err = db.Callback().Row().Before("gorm:row").Register(op.beforeName, before)
			if err == nil {
				err = db.Callback().Row().After("gorm:row").Register(op.afterName, after)
			}
		case "raw":
			err = db.Callback().Raw().Before("gorm:raw").Register(op.beforeName, before)
			if err == nil {
				err = db.Callback().Raw().After("gorm:raw").Register(op.afterName, after)
			}
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func makeBefore(nw *nightwatch.Nightwatch) func(*gorm.DB) {
	return func(db *gorm.DB) {
		if !nw.Config().Enabled || nw.Config().IgnoreQueries {
			return
		}

		ctx := db.Statement.Context

		// Store start time
		ctx = context.WithValue(ctx, startTimeKey{}, time.Now())

		// Capture caller info NOW — the call stack still contains the user's code.
		// We walk the stack and find the first frame that is NOT in GORM,
		// nightwatch, database/sql, or runtime internals.
		// This replicates PHP's Location::forQueryTrace() which skips vendor frames.
		file, line := findUserCaller()
		ctx = context.WithValue(ctx, callerInfoKey{}, callerInfo{File: file, Line: line})

		db.Statement.Context = ctx
	}
}

func makeAfter(nw *nightwatch.Nightwatch, cfg *Config) func(*gorm.DB) {
	return func(db *gorm.DB) {
		if !nw.Config().Enabled || nw.Config().IgnoreQueries {
			return
		}

		ctx := db.Statement.Context

		// Retrieve start time
		startVal := ctx.Value(startTimeKey{})
		if startVal == nil {
			return
		}
		start := startVal.(time.Time)
		duration := time.Since(start)

		// Get nightwatch execution state
		state := nightwatch.StateFromContext(ctx)
		if state == nil {
			return // No active request — skip (e.g. migrations at startup)
		}

		// Get the executed SQL
		sql := db.Statement.SQL.String()
		if sql == "" {
			return
		}

		// Retrieve caller info
		file := "unknown"
		line := 0
		if ci := ctx.Value(callerInfoKey{}); ci != nil {
			info := ci.(callerInfo)
			file = info.File
			line = info.Line
		}

		// Normalize file path: strip base path prefix
		// Matches PHP's Location::normalizeFile() which strips the base path
		if cfg.BasePath != "" && strings.HasPrefix(file, cfg.BasePath) {
			file = file[len(cfg.BasePath):]
		}

		// Build query record (handles SQL normalization for _group hash)
		record := sensor.QueryRecord(
			state,
			sql,
			cfg.ConnectionName,
			cfg.ConnectionType,
			file,
			line,
			duration,
		)

		_ = nw.Ingester().Write(record)
	}
}

// findUserCaller walks the call stack to find the first frame in user application
// code, skipping GORM, nightwatch, database/sql, and runtime frames.
func findUserCaller() (string, int) {
	var pcs [50]uintptr
	n := runtime.Callers(3, pcs[:]) // skip: Callers, findUserCaller, makeBefore closure
	if n == 0 {
		return "unknown", 0
	}

	frames := runtime.CallersFrames(pcs[:n])

	for {
		frame, more := frames.Next()

		if !isInternalFrame(frame.Function) {
			return frame.File, frame.Line
		}

		if !more {
			break
		}
	}

	return "unknown", 0
}

// isInternalFrame returns true if the frame belongs to a library/framework
// rather than user application code.
func isInternalFrame(fn string) bool {
	return strings.HasPrefix(fn, "runtime.") ||
		strings.HasPrefix(fn, "reflect.") ||
		strings.HasPrefix(fn, "gorm.io/") ||
		strings.Contains(fn, "nightwatch") ||
		strings.HasPrefix(fn, "database/sql") ||
		strings.HasPrefix(fn, "github.com/gin-gonic/") ||
		strings.HasPrefix(fn, "net/http") ||
		strings.HasPrefix(fn, "github.com/mattn/go-sqlite3")
}
