package app

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/lorenzocorallo/megadw/internal/auth"
	"github.com/lorenzocorallo/megadw/internal/download"
	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
)

const (
	DefaultStateDir = "/var/lib/megad"
	DefaultDatabase = "megad.sqlite3"
	DefaultSecret   = "secret.key"
)

type Config struct {
	StateDir       string
	DatabasePath   string
	SecretKeyPath  string
	MegaAPIBaseURL string
	HTTPClient     *http.Client
	Version        string
}

type App struct {
	DB        *store.DB
	Secrets   *store.SecretStore
	Settings  *settings.Service
	Auth      *auth.Manager
	Mega      *mega.Client
	Downloads *download.Manager
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
	client := mega.NewClient(config.HTTPClient, config.MegaAPIBaseURL)
	downloadManager, err := download.NewManager(download.Config{DB: database, Secrets: secrets, Mega: client, Settings: settingsService})
	if err != nil {
		return closeWithError(fmt.Errorf("initialize download manager: %w", err))
	}
	if err := downloadManager.Start(ctx); err != nil {
		_ = downloadManager.Close()
		return closeWithError(fmt.Errorf("start download manager: %w", err))
	}
	return &App{DB: database, Secrets: secrets, Settings: settingsService, Auth: manager, Mega: client, Downloads: downloadManager}, nil
}

func (a *App) Close() error {
	if a == nil || a.DB == nil {
		return nil
	}
	if a.Downloads != nil {
		_ = a.Downloads.Close()
	}
	return a.DB.Close()
}
