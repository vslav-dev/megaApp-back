package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"megaapp-back/internal/config"
	"megaapp-back/internal/httpx"
	logplatform "megaapp-back/internal/platform/log"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	logger := logplatform.New(cfg.LogLevel)
	ctx := context.Background()

	app, err := httpx.NewApp(ctx, cfg, logger)
	if err != nil {
		logger.Error("app_init_failed", "error", err)
		return 1
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErrCh := make(chan error, 1)
	go func() {
		logger.Info("app_starting", "addr", cfg.HTTPAddress(), "env", cfg.AppEnv)
		serveErrCh <- app.Start()
	}()

	var serveErr error
	select {
	case <-sigCtx.Done():
	case serveErr = <-serveErrCh:
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := app.Shutdown(shutdownCtx); err != nil {
		logger.Error("app_shutdown_failed", "error", err)
	}

	if serveErr == nil {
		serveErr = <-serveErrCh
	}

	if serveErr != nil {
		logger.Error("app_stopped_with_error", "error", serveErr)
		return 1
	}
	logger.Info("app_stopped")
	return 0
}
