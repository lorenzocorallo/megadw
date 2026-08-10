// Command megad serves the MEGA Downloader application.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lorenzocorallo/megadw/internal/api"
	"github.com/lorenzocorallo/megadw/internal/app"
	"github.com/lorenzocorallo/megadw/internal/buildinfo"
	"github.com/lorenzocorallo/megadw/internal/logging"
	"github.com/lorenzocorallo/megadw/internal/webui"
)

const defaultListenAddress = "127.0.0.1:8080"

func main() {
	slog.SetDefault(logging.NewTextLogger(os.Stdout))
	listenAddress := flag.String("listen", envOrDefault("MEGAD_LISTEN", defaultListenAddress), "HTTP listen address")
	stateDir := flag.String("state-dir", envOrDefault("MEGAD_STATE_DIR", app.DefaultStateDir), "application state directory")
	databasePath := flag.String("database", os.Getenv("MEGAD_DATABASE"), "SQLite database path")
	secretKeyPath := flag.String("secret-key", os.Getenv("MEGAD_SECRET_KEY"), "application secret key path")
	megaAPIBaseURL := flag.String("mega-api-base", os.Getenv("MEGAD_MEGA_API_BASE_URL"), "MEGA API base URL (for compatibility fixtures and routed deployments)")
	flag.Parse()

	application, err := app.Open(context.Background(), app.Config{
		StateDir:       *stateDir,
		DatabasePath:   *databasePath,
		SecretKeyPath:  *secretKeyPath,
		MegaAPIBaseURL: *megaAPIBaseURL,
	})
	if err != nil {
		slog.Error("initialize application", "error", err)
		os.Exit(1)
	}
	build := buildinfo.Current()

	staticHandler, err := webui.Handler()
	if err != nil {
		_ = application.Close()
		slog.Error("load embedded web UI", "error", err)
		os.Exit(1)
	}
	apiHandler := api.New(api.Config{
		DB:         application.DB,
		Secrets:    application.Secrets,
		Settings:   application.Settings,
		Auth:       application.Auth,
		Mega:       application.Mega,
		Downloads:  application.Downloads,
		Transports: application.Transports,
		Events:     application.Events,
		Version:    build.Version,
		Commit:     build.Commit,
		BuildTime:  build.BuildTime,
	})
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", apiHandler)
	mux.Handle("/", staticHandler)

	server := &http.Server{
		Addr:              *listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		// SSE responses are intentionally long-lived. Handler-level bounded
		// request work and IdleTimeout still protect ordinary API calls.
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	slog.Info("megad listening", "address", server.Addr)
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	select {
	case <-signalContext.Done():
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Second)
		serverShutdown := make(chan error, 1)
		go func() { serverShutdown <- server.Shutdown(shutdownContext) }()
		applicationErr := application.CloseContext(shutdownContext)
		serverErr := <-serverShutdown
		if serverErr != nil && !errors.Is(serverErr, context.DeadlineExceeded) {
			slog.Error("HTTP server shutdown", "error", serverErr)
		}
		if applicationErr != nil {
			slog.Error("application shutdown", "error", applicationErr)
		}
		cancelShutdown()
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server stopped", "error", err)
		}
		_ = application.Close()
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
