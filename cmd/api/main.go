package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"kanpic/internal/ai"
	"kanpic/internal/apikey"
	"kanpic/internal/auth"
	"kanpic/internal/automation"
	"kanpic/internal/database"
	"kanpic/internal/httpapi"
	"kanpic/internal/mail"
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
	bootstrap := auth.BootstrapCredentials{
		ID:       strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_ID")),
		Password: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
	}
	if (bootstrap.ID == "") != (bootstrap.Password == "") {
		logger.Error("BOOTSTRAP_ADMIN_ID and BOOTSTRAP_ADMIN_PASSWORD must be configured together")
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
	authService := auth.New(pool, settingRepository, bootstrap)
	aiService := ai.NewService(pool, settingRepository, repository, logger)
	automationService := automation.NewService(pool, settingRepository, repository, logger)
	mailService := mail.NewService(pool, settingRepository, httpapi.NewMailDirectory(repository), logger)
	handler := httpapi.NewPlatformWithServices(repository, settingRepository, keyRepository, authService, logStore, aiService, automationService, logger, httpapi.WithMail(mailService))
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	address := ":" + port
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 130 * time.Second, IdleTimeout: 60 * time.Second}
	runtimeContext, stopRuntime := context.WithCancel(context.Background())
	defer stopRuntime()
	var runtimeWorkers sync.WaitGroup
	runtimeWorkers.Add(2)
	go func() {
		defer runtimeWorkers.Done()
		automationService.RunScheduler(runtimeContext)
	}()
	// Applies ai.history_retention_days without an operator having to prune.
	go func() {
		defer runtimeWorkers.Done()
		aiService.RunRetention(runtimeContext)
	}()

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
	stopRuntime()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
	runtimeStopped := make(chan struct{})
	go func() {
		runtimeWorkers.Wait()
		close(runtimeStopped)
	}()
	select {
	case <-runtimeStopped:
	case <-ctx.Done():
		logger.Error("background workers did not stop before shutdown deadline", "error", ctx.Err())
	}
}
