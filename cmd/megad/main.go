// Command megad serves the MEGA Downloader application.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lorenzocorallo/megadw/internal/api"
	"github.com/lorenzocorallo/megadw/internal/app"
	"github.com/lorenzocorallo/megadw/internal/webui"
)

const defaultListenAddress = "127.0.0.1:8080"

func main() {
	listenAddress := flag.String("listen", defaultListenAddress, "HTTP listen address")
	stateDir := flag.String("state-dir", app.DefaultStateDir, "application state directory")
	databasePath := flag.String("database", "", "SQLite database path")
	secretKeyPath := flag.String("secret-key", "", "application secret key path")
	flag.Parse()

	application, err := app.Open(context.Background(), app.Config{StateDir: *stateDir, DatabasePath: *databasePath, SecretKeyPath: *secretKeyPath})
	if err != nil {
		slog.Error("initialize application", "error", err)
		os.Exit(1)
	}
	defer application.Close()

	staticHandler, err := webui.Handler()
	if err != nil {
		slog.Error("load embedded web UI", "error", err)
		os.Exit(1)
	}
	apiHandler := api.New(api.Config{DB: application.DB, Secrets: application.Secrets, Settings: application.Settings, Auth: application.Auth, Mega: application.Mega, Downloads: application.Downloads, Transports: application.Transports})
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", apiHandler)
	mux.Handle("/", staticHandler)

	server := &http.Server{
		Addr:              *listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
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
		if err := server.Shutdown(shutdownContext); err != nil {
			slog.Error("HTTP server shutdown", "error", err)
		}
		cancelShutdown()
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server stopped", "error", err)
		}
	}
}
