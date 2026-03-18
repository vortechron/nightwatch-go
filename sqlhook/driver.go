package sqlhook

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"runtime"
	"strings"
	"time"

	nightwatch "github.com/laravel/nightwatch-go"
	"github.com/laravel/nightwatch-go/sensor"
)

// OpenDB opens a database connection with Nightwatch query instrumentation.
// It wraps the underlying driver to capture query execution telemetry.
//
// Usage:
//
//	nw, _ := nightwatch.New(nightwatch.WithToken("..."))
//	db := sqlhook.OpenDB(nw, "mysql", "user:pass@/dbname", sqlhook.WithConnectionName("mysql-primary"))
func OpenDB(nw *nightwatch.Nightwatch, driverName, dsn string, opts ...DBOption) (*sql.DB, error) {
	cfg := &dbConfig{
		connectionName: driverName,
		connectionType: "",
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Register a wrapped driver
	wrappedName := fmt.Sprintf("nightwatch_%s_%d", driverName, time.Now().UnixNano())

	// Get the original driver
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	originalDriver := db.Driver()
	db.Close()

	wrapped := &instrumentedDriver{
		driver:         originalDriver,
		nw:             nw,
		connectionName: cfg.connectionName,
		connectionType: cfg.connectionType,
	}

	sql.Register(wrappedName, wrapped)
	return sql.Open(wrappedName, dsn)
}

type dbConfig struct {
	connectionName string
	connectionType string // "read", "write", or ""
}

type DBOption func(*dbConfig)

func WithConnectionName(name string) DBOption {
	return func(c *dbConfig) { c.connectionName = name }
}

func WithConnectionType(connType string) DBOption {
	return func(c *dbConfig) { c.connectionType = connType }
}

// instrumentedDriver wraps a database/sql driver to capture query telemetry.
type instrumentedDriver struct {
	driver         driver.Driver
	nw             *nightwatch.Nightwatch
	connectionName string
	connectionType string
}

func (d *instrumentedDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &instrumentedConn{
		Conn:           conn,
		nw:             d.nw,
		connectionName: d.connectionName,
		connectionType: d.connectionType,
	}, nil
}

// instrumentedConn wraps a database/sql connection.
type instrumentedConn struct {
	driver.Conn
	nw             *nightwatch.Nightwatch
	connectionName string
	connectionType string
}

func (c *instrumentedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !c.nw.Config().Enabled || c.nw.Config().IgnoreQueries {
		if qc, ok := c.Conn.(driver.QueryerContext); ok {
			return qc.QueryContext(ctx, query, args)
		}
		return nil, fmt.Errorf("driver does not support QueryerContext")
	}

	start := time.Now()

	var rows driver.Rows
	var err error
	if qc, ok := c.Conn.(driver.QueryerContext); ok {
		rows, err = qc.QueryContext(ctx, query, args)
	} else {
		return nil, fmt.Errorf("driver does not support QueryerContext")
	}

	c.recordQuery(ctx, query, time.Since(start))
	return rows, err
}

func (c *instrumentedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if !c.nw.Config().Enabled || c.nw.Config().IgnoreQueries {
		if ec, ok := c.Conn.(driver.ExecerContext); ok {
			return ec.ExecContext(ctx, query, args)
		}
		return nil, fmt.Errorf("driver does not support ExecerContext")
	}

	start := time.Now()

	var result driver.Result
	var err error
	if ec, ok := c.Conn.(driver.ExecerContext); ok {
		result, err = ec.ExecContext(ctx, query, args)
	} else {
		return nil, fmt.Errorf("driver does not support ExecerContext")
	}

	c.recordQuery(ctx, query, time.Since(start))
	return result, err
}

func (c *instrumentedConn) recordQuery(ctx context.Context, query string, duration time.Duration) {
	state := nightwatch.StateFromContext(ctx)
	if state == nil {
		return
	}

	file, line := callerFile(5) // skip through the sql/driver stack

	record := sensor.QueryRecord(state, query, c.connectionName, c.connectionType, file, line, duration)
	_ = c.nw.Ingester().Write(record)
}

// callerFile walks up the call stack to find the first frame outside of
// database/sql, the nightwatch packages, and the driver packages.
func callerFile(skip int) (string, int) {
	var pcs [20]uintptr
	n := runtime.Callers(skip, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])

	for {
		frame, more := frames.Next()
		// Skip internal frames
		if strings.Contains(frame.Function, "database/sql") ||
			strings.Contains(frame.Function, "nightwatch") ||
			strings.Contains(frame.Function, "sqlhook") {
			if !more {
				break
			}
			continue
		}
		return frame.File, frame.Line
	}

	return "unknown", 0
}
