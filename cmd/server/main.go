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

	"github.com/joho/godotenv"

	"whatsapp-api/internal/config"
	"whatsapp-api/internal/httpserver"
	"whatsapp-api/internal/store"
	"whatsapp-api/internal/webhook"
	"whatsapp-api/internal/whatsapp"
)

func main() {
	_ = godotenv.Load()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("konfigurasi tidak valid", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	data, err := store.Open(ctx, cfg.MySQLDSN)
	if err != nil {
		logger.Error("database tidak siap", "error", err)
		os.Exit(1)
	}
	defer data.Close()

	waManager, err := whatsapp.New(ctx, cfg.SessionDSN, cfg.LogLevel, data, logger)
	if err != nil {
		logger.Error("WhatsApp manager tidak siap", "error", err)
		os.Exit(1)
	}
	defer waManager.Close()

	worker := webhook.New(data, cfg.WebhookTimeout, cfg.WebhookSecret, cfg.WebhookMaxAttempts, logger)
	go worker.Run(ctx)

	server := &http.Server{
		Addr: cfg.Addr, Handler: httpserver.New(cfg, data, waManager, worker, logger),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 45 * time.Second, IdleTimeout: 90 * time.Second,
	}
	go func() {
		logger.Info("gateway berjalan", "address", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server berhenti", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown server", "error", err)
	}
	logger.Info("gateway berhenti")
}
