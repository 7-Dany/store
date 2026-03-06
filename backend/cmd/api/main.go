package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/7-Dany/store/backend/internal/config"
	"github.com/7-Dany/store/backend/internal/server"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env before reading any config. godotenv.Load is a no-op when the
	// file does not exist (e.g. in production where vars are injected directly),
	// so this is safe in all environments.
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("startup: config", "error", err)
		os.Exit(1)
	}

	// ctx is cancelled on SIGINT / SIGTERM; server.New propagates it to every
	// background goroutine (KV store cleanup, etc.).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv, cleanup, err := server.New(ctx, cfg)
	if err != nil {
		slog.Error("startup: server init", "error", err)
		os.Exit(1)
	}

	// Graceful shutdown: wait for the OS signal, give in-flight requests up to
	// 15 s to finish, then run cleanup to flush the mail queue and close the pool.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("shutdown: http server", "error", err)
		}
		cleanup()
	}()

	slog.Info("server starting", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server", "error", err)
		os.Exit(1)
	}
	slog.Info("server stopped")
}
