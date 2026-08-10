// Command megad serves the MEGA Downloader application.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
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
	healthcheck := flag.Bool("healthcheck", false, "check the local HTTP health endpoint and exit")
	healthcheckURL := flag.String("healthcheck-url", envOrDefault("MEGAD_HEALTHCHECK_URL", "http://127.0.0.1:8080/api/v1/health"), "health endpoint URL")
	listenAddress := flag.String("listen", envOrDefault("MEGAD_LISTEN", defaultListenAddress), "HTTP listen address")
	stateDir := flag.String("state-dir", envOrDefault("MEGAD_STATE_DIR", app.DefaultStateDir), "application state directory")
	databasePath := flag.String("database", os.Getenv("MEGAD_DATABASE"), "SQLite database path")
	secretKeyPath := flag.String("secret-key", os.Getenv("MEGAD_SECRET_KEY"), "application secret key path")
	megaAPIBaseURL := flag.String("mega-api-base", os.Getenv("MEGAD_MEGA_API_BASE_URL"), "MEGA API base URL (for compatibility fixtures and routed deployments)")
	secureCookies := flag.Bool("secure-cookies", envBool("MEGAD_SECURE_COOKIES"), "mark administrator session cookies Secure (required behind an HTTPS reverse proxy)")
	flag.Parse()
	if *healthcheck {
		if err := runHealthcheck(*healthcheckURL); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	application, err := app.Open(context.Background(), app.Config{
		StateDir:           *stateDir,
		DatabasePath:       *databasePath,
		SecretKeyPath:      *secretKeyPath,
		MegaAPIBaseURL:     *megaAPIBaseURL,
		DeferDownloadStart: true,
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
		DB:            application.DB,
		Secrets:       application.Secrets,
		Settings:      application.Settings,
		Auth:          application.Auth,
		Mega:          application.Mega,
		Downloads:     application.Downloads,
		Transports:    application.Transports,
		Events:        application.Events,
		Version:       build.Version,
		Commit:        build.Commit,
		BuildTime:     build.BuildTime,
		SecureCookies: *secureCookies,
		AllowedHosts:  allowedHosts(*listenAddress, os.Getenv("MEGAD_ALLOWED_HOSTS")),
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

	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		_ = application.Close()
		slog.Error("open HTTP listener", "error", err)
		os.Exit(1)
	}
	slog.Info("megad listening", "address", listener.Addr().String())
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()
	// Recovery and auto-resume begin only after the HTTP listener is accepting
	// health and administration requests. This keeps startup failures visible
	// before any persisted transfer starts writing again.
	if err := waitForLocalHealth(listener.Addr()); err != nil {
		_ = server.Close()
		_ = application.Close()
		slog.Error("verify HTTP listener health", "error", err)
		os.Exit(1)
	}
	if err := application.Downloads.Start(context.Background()); err != nil {
		_ = server.Close()
		_ = application.Close()
		slog.Error("start download manager", "error", err)
		os.Exit(1)
	}
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

func runHealthcheck(endpoint string) error {
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
	defer client.CloseIdleConnections()
	response, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("healthcheck request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned HTTP %d", response.StatusCode)
	}
	var value struct {
		Data struct {
			Status   string `json:"status"`
			Database string `json:"database"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&value); err != nil {
		return fmt.Errorf("decode healthcheck response: %w", err)
	}
	if value.Data.Status != "ok" || value.Data.Database != "ok" {
		return fmt.Errorf("healthcheck is not ready: status=%q database=%q", value.Data.Status, value.Data.Database)
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string) bool {
	switch os.Getenv(name) {
	case "1", "true", "TRUE", "yes", "YES", "on", "ON":
		return true
	default:
		return false
	}
}

func allowedHosts(listenAddress, configured string) []string {
	seen := make(map[string]struct{})
	add := func(host string) {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			seen[host] = struct{}{}
		}
	}
	for _, host := range strings.Split(configured, ",") {
		add(host)
	}
	host, port, err := net.SplitHostPort(listenAddress)
	if err == nil {
		if host != "" && host != "0.0.0.0" && host != "::" {
			add(net.JoinHostPort(host, port))
		}
		if host == "" || host == "0.0.0.0" || host == "::" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback() {
			add(net.JoinHostPort("localhost", port))
			add(net.JoinHostPort("127.0.0.1", port))
			add(net.JoinHostPort("::1", port))
		}
		if host == "" || host == "0.0.0.0" || host == "::" {
			if addresses, addressErr := net.InterfaceAddrs(); addressErr == nil {
				for _, address := range addresses {
					if ip, _, parseErr := net.ParseCIDR(address.String()); parseErr == nil {
						add(net.JoinHostPort(ip.String(), port))
					}
				}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for host := range seen {
		result = append(result, host)
	}
	return result
}

func waitForLocalHealth(address net.Addr) error {
	tcpAddress, ok := address.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("listener address is not TCP")
	}
	ip := tcpAddress.IP
	if ip == nil || ip.IsUnspecified() {
		if ip != nil && ip.To4() == nil {
			ip = net.IPv6loopback
		} else {
			ip = net.IPv4(127, 0, 0, 1)
		}
	}
	endpoint := "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(tcpAddress.Port)) + "/api/v1/health"
	client := &http.Client{Timeout: 250 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("health endpoint did not become ready")
}
