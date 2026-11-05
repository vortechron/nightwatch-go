// Command omni-capture is a fake Omni for local tests: it validates batches
// with Omni's decoder rules and writes accepted ones to a folder.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/vortechron/nightwatch-go/internal/capture"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:2601", "listen address")
	dir := flag.String("dir", "capture", "folder for batch-*.json files")
	token := flag.String("token", "", "accept only this bearer token (default: any)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	server, err := capture.New(*dir, *token, logger)
	if err != nil {
		logger.Error("omni-capture", "error", err)
		os.Exit(1)
	}

	httpServer := &http.Server{Addr: *addr, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		_ = httpServer.Shutdown(shutdownCtx)
	}()

	logger.Info("omni-capture listening", "addr", *addr, "dir", *dir)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("omni-capture", "error", err)
		os.Exit(1)
	}
}
