package nightwatch

import (
	"context"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Client ties the pieces together: the translator that builds records from
// OpenTelemetry data and the sender that ships them to Omni.
type Client struct {
	cfg        Config
	sender     *Sender
	translator *Translator

	stop     chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// Stats reports the health of the sender.
type Stats struct {
	Pending int
	Dropped int64
}

// New builds a client. Nothing runs until Start is called.
func New(cfg Config) *Client {
	sender := NewSender(cfg)

	return &Client{
		cfg:        cfg,
		sender:     sender,
		translator: NewTranslator(cfg, sender),
		stop:       make(chan struct{}),
	}
}

// NewFromEnv builds a client from the NIGHTWATCH_* environment variables.
func NewFromEnv() *Client {
	hostname, _ := os.Hostname()

	return New(ConfigFromEnv(os.Getenv, hostname))
}

// Config returns the settings the client runs with.
func (c *Client) Config() Config { return c.cfg }

// Enabled reports whether the client sends anything.
func (c *Client) Enabled() bool { return c.cfg.Enabled }

// Start launches the sender and the sweeper.
func (c *Client) Start() {
	if !c.cfg.Enabled {
		return
	}

	c.sender.Start()
	c.wg.Add(1)

	go c.sweep()
}

func (c *Client) sweep() {
	defer c.wg.Done()

	interval := max(c.cfg.TraceMaxWait/4, 50*time.Millisecond)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.translator.Sweep()
		}
	}
}

// SpanExporter is the OpenTelemetry span exporter to register.
func (c *Client) SpanExporter() sdktrace.SpanExporter { return c.translator.SpanExporter() }

// LogExporter is the OpenTelemetry log exporter to register.
func (c *Client) LogExporter() log.Exporter { return c.translator.LogExporter() }

// Translator exposes the translator, for tests and custom wiring.
func (c *Client) Translator() *Translator { return c.translator }

// Stats returns the sender counters.
func (c *Client) Stats() Stats {
	return Stats{Pending: c.sender.Pending(), Dropped: c.sender.Dropped()}
}

// Flush moves everything the translator still holds to the sender and sends
// it.
func (c *Client) Flush(ctx context.Context) {
	c.translator.Drain()
	c.sender.Flush(ctx)
}

// Shutdown stops the sweeper, hands over what the translator holds and sends
// the rest. It is safe to call twice.
func (c *Client) Shutdown(ctx context.Context) error {
	c.stopOnce.Do(func() { close(c.stop) })
	c.wg.Wait()
	c.translator.Drain()

	return c.sender.Shutdown(ctx)
}
