package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/lorenzocorallo/megadw/internal/auth"
	"github.com/lorenzocorallo/megadw/internal/download"
	"github.com/lorenzocorallo/megadw/internal/events"
	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/network"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
)

const (
	DefaultStateDir = "/var/lib/megadw"
	DefaultDatabase = "megadw.sqlite3"
	DefaultSecret   = "secret.key"
)

type Config struct {
	StateDir           string
	DatabasePath       string
	SecretKeyPath      string
	MegaAPIBaseURL     string
	HTTPClient         *http.Client
	Version            string
	DeferDownloadStart bool
}

type App struct {
	DB         *store.DB
	Secrets    *store.SecretStore
	Settings   *settings.Service
	Auth       *auth.Manager
	Mega       *mega.Client
	Downloads  *download.Manager
	Transports *network.TransportPool
	Events     *events.Bus
	closeOnce  sync.Once
	closeErr   error
}

func Open(ctx context.Context, config Config) (*App, error) {
	if config.StateDir == "" {
		config.StateDir = DefaultStateDir
	}
	if config.DatabasePath == "" {
		config.DatabasePath = filepath.Join(config.StateDir, DefaultDatabase)
	}
	if config.SecretKeyPath == "" {
		config.SecretKeyPath = filepath.Join(config.StateDir, DefaultSecret)
	}
	database, err := store.Open(ctx, config.DatabasePath)
	if err != nil {
		return nil, err
	}
	closeWithError := func(err error) (*App, error) {
		_ = database.Close()
		return nil, err
	}
	secrets, err := store.OpenSecretStore(config.SecretKeyPath)
	if err != nil {
		return closeWithError(err)
	}
	settingsService, err := settings.NewService(database)
	if err != nil {
		return closeWithError(fmt.Errorf("initialize settings: %w", err))
	}
	manager := auth.NewManager(database)
	current, settingsErr := settingsService.Get(ctx)
	if settingsErr != nil {
		return closeWithError(fmt.Errorf("read network settings: %w", settingsErr))
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = network.NewHTTPClient(network.TransportConfig{
			ConnectTimeout:        time.Duration(current.Network.ConnectTimeoutSeconds) * time.Second,
			ResponseHeaderTimeout: time.Duration(current.Network.ResponseHeaderTimeoutSeconds) * time.Second,
			MaxConnectionsPerHost: current.Downloads.MaxGlobalWorkers,
		})
	}
	client := mega.NewClient(httpClient, config.MegaAPIBaseURL)
	transports := network.NewTransportPool(network.TransportConfig{ConnectTimeout: time.Duration(current.Network.ConnectTimeoutSeconds) * time.Second, ResponseHeaderTimeout: time.Duration(current.Network.ResponseHeaderTimeoutSeconds) * time.Second, MaxConnectionsPerHost: current.Downloads.MaxGlobalWorkers})
	eventBus := events.NewBus()
	downloadManager, err := download.NewManager(download.Config{DB: database, Secrets: secrets, Mega: client, Settings: settingsService, TransportPool: transports, Events: eventBus, NormalRetryLimit: current.Downloads.NormalRetryLimit})
	if err != nil {
		eventBus.Close()
		return closeWithError(fmt.Errorf("initialize download manager: %w", err))
	}
	if !config.DeferDownloadStart {
		if err := downloadManager.Start(ctx); err != nil {
			_ = downloadManager.Close()
			eventBus.Close()
			return closeWithError(fmt.Errorf("start download manager: %w", err))
		}
	}
	return &App{DB: database, Secrets: secrets, Settings: settingsService, Auth: manager, Mega: client, Downloads: downloadManager, Transports: transports, Events: eventBus}, nil
}

func (a *App) Close() error {
	return a.CloseContext(context.Background())
}

// CloseContext shuts down owned goroutines and resources in dependency order.
// The transfer manager is stopped before SQLite is closed so its final
// checkpoint transaction cannot race a closing database. A release shutdown
// passes the process-wide 20-second deadline here; if that deadline expires,
// SQLite is deliberately left open until the process supervisor reclaims it.
func (a *App) CloseContext(ctx context.Context) error {
	if a == nil || a.DB == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.closeOnce.Do(func() {
		var errs []error
		managerClosed := true
		if a.Downloads != nil {
			if err := a.Downloads.CloseContext(ctx); err != nil {
				managerClosed = false
				errs = append(errs, fmt.Errorf("close download manager: %w", err))
			}
		}
		if a.Events != nil {
			a.Events.Close()
		}
		if a.Mega != nil {
			a.Mega.CloseIdleConnections()
		}
		if a.Transports != nil {
			a.Transports.Close()
		}
		if managerClosed {
			if err := a.DB.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close database: %w", err))
			}
		} else {
			// A timed-out manager may still be finishing its final checkpoint.
			// Leave SQLite open rather than trading a bounded process exit for a
			// concurrent close/write race. The process supervisor will reclaim it
			// after the shutdown deadline.
			errs = append(errs, errors.New("database left open because download manager did not finish"))
		}
		a.closeErr = errors.Join(errs...)
	})
	return a.closeErr
}
