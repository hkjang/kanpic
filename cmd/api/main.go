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

	"kanpic/internal/apikey"
	"kanpic/internal/auth"
	"kanpic/internal/database"
	"kanpic/internal/httpapi"
	"kanpic/internal/observability"
	"kanpic/internal/settings"
	"kanpic/internal/workbook"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		logger.Error("POSTGRES_DSN is required")
		os.Exit(2)
	}
	startupContext, startupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	pool, err := database.Open(startupContext, dsn)
	startupCancel()
	if err != nil {
		logger.Error("database startup failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	logger, closeLogger := observability.NewLogger(pool, os.Stdout)
	defer closeLogger()
	repository := workbook.NewPostgresRepository(pool)
	settingRepository := settings.New(pool)
	setupContext, setupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := settingRepository.EnsureDefaults(setupContext); err != nil {
		setupCancel()
		logger.Error("default settings failed", "error", err)
		os.Exit(1)
	}
	setupCancel()
	logStore := observability.NewStore(pool)
	keyRepository := apikey.New(pool)
	authService := auth.New(pool, settingRepository)
	handler := httpapi.NewPlatform(repository, settingRepository, keyRepository, authService, logStore, logger)
	address := ":8080"
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}

	go func() {
		logger.Info("kanpic API started", "address", address, "storage", "postgres")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("API stopped", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
