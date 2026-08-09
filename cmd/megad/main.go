// Command megad serves the MEGA Downloader application.
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/lorenzocorallo/megadw/internal/webui"
)

const defaultListenAddress = "127.0.0.1:8080"

func main() {
	listenAddress := flag.String("listen", defaultListenAddress, "HTTP listen address")
	flag.Parse()

	handler, err := webui.Handler()
	if err != nil {
		slog.Error("load embedded web UI", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              *listenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	slog.Info("megad listening", "address", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("HTTP server stopped", "error", err)
		os.Exit(1)
	}
}
