package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lorenzocorallo/megadw/internal/auth"
	"github.com/lorenzocorallo/megadw/internal/buildinfo"
	"github.com/lorenzocorallo/megadw/internal/download"
	"github.com/lorenzocorallo/megadw/internal/events"
	"github.com/lorenzocorallo/megadw/internal/fsroot"
	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/network"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
)

const (
	maxJSONBody     = 256 << 10
	maxResolveBody  = 256 << 10
	maxSetupBody    = 256 << 10
	maxSettingsBody = 256 << 10
	sseWriteTimeout = 10 * time.Second
)

type Config struct {
	DB            *store.DB
	Secrets       *store.SecretStore
	Settings      *settings.Service
	Auth          *auth.Manager
	Mega          *mega.Client
	Downloads     *download.Manager
	Transports    *network.TransportPool
	Events        *events.Bus
	Version       string
	Commit        string
	BuildTime     string
	SecureCookies bool
	AllowedHosts  []string
	Now           func() time.Time
}

type Server struct {
	config       Config
	auth         *auth.Manager
	settings     *settings.Service
	mega         *mega.Client
	downloads    *download.Manager
	transports   *network.TransportPool
	events       *events.Bus
	allowedHosts map[string]struct{}
}

func New(config Config) http.Handler {
	return NewServer(config).Handler()
}

func NewServer(config Config) *Server {
	if config.Auth == nil && config.DB != nil {
		config.Auth = auth.NewManager(config.DB)
	}
	if config.Auth != nil {
		config.Auth.SecureCookies = config.SecureCookies
		if config.Now != nil {
			config.Auth.Now = config.Now
		}
	}
	if config.Version == "" {
		config.Version = buildinfo.Current().Version
	}
	if config.Commit == "" {
		config.Commit = buildinfo.Current().Commit
	}
	if config.BuildTime == "" {
		config.BuildTime = buildinfo.Current().BuildTime
	}
	if config.Transports == nil {
		config.Transports = network.NewTransportPool(network.TransportConfig{ConnectTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, MaxConnectionsPerHost: 8})
	}
	if config.Events == nil {
		config.Events = events.NewBus()
	}
	allowedHosts := make(map[string]struct{}, len(config.AllowedHosts))
	for _, host := range config.AllowedHosts {
		if normalized := normalizeHost(host); normalized != "" {
			allowedHosts[normalized] = struct{}{}
		}
	}
	return &Server{config: config, auth: config.Auth, settings: config.Settings, mega: config.Mega, downloads: config.Downloads, transports: config.Transports, events: config.Events, allowedHosts: allowedHosts}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/version", s.handleVersion)
	mux.HandleFunc("GET /api/v1/dashboard", s.withAuth(s.handleDashboard))
	mux.HandleFunc("GET /api/v1/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/v1/auth/setup", s.handleSetup)
	// Keep a short first-run alias for clients that discover setup before the
	// authenticated auth namespace is available.
	mux.HandleFunc("POST /api/v1/setup", s.handleSetup)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/me", s.handleMe)

	mux.HandleFunc("GET /api/v1/settings", s.withAuth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/v1/settings", s.withAuth(s.handlePutSettings))

	mux.HandleFunc("POST /api/v1/downloads/resolve", s.withAuth(s.handleResolve))
	mux.HandleFunc("POST /api/v1/downloads", s.withAuth(s.handleCreateDownload))
	mux.HandleFunc("GET /api/v1/downloads", s.withAuth(s.handleListDownloads))
	mux.HandleFunc("GET /api/v1/downloads/{id}", s.withAuth(s.handleGetDownload))
	mux.HandleFunc("GET /api/v1/downloads/{id}/events", s.withAuth(s.handleDownloadEvents))
	mux.HandleFunc("POST /api/v1/downloads/{id}/pause", s.withAuth(s.handlePauseDownload))
	mux.HandleFunc("POST /api/v1/downloads/{id}/resume", s.withAuth(s.handleResumeDownload))
	mux.HandleFunc("POST /api/v1/downloads/{id}/retry", s.withAuth(s.handleRetryDownload))
	mux.HandleFunc("POST /api/v1/downloads/{id}/cancel", s.withAuth(s.handleCancelDownload))
	mux.HandleFunc("DELETE /api/v1/downloads/{id}", s.withAuth(s.handleDeleteDownload))
	mux.HandleFunc("POST /api/v1/queue/pause", s.withAuth(s.handlePauseQueue))
	mux.HandleFunc("POST /api/v1/queue/resume", s.withAuth(s.handleResumeQueue))
	mux.HandleFunc("GET /api/v1/accounts", s.withAuth(s.handleListAccounts))
	mux.HandleFunc("POST /api/v1/accounts", s.withAuth(s.handleCreateAccount))
	mux.HandleFunc("POST /api/v1/accounts/{id}/test", s.withAuth(s.handleTestAccount))
	mux.HandleFunc("PUT /api/v1/accounts/{id}", s.withAuth(s.handleUpdateAccount))
	mux.HandleFunc("DELETE /api/v1/accounts/{id}", s.withAuth(s.handleDeleteAccount))
	mux.HandleFunc("GET /api/v1/proxies", s.withAuth(s.handleListProxies))
	mux.HandleFunc("POST /api/v1/proxies", s.withAuth(s.handleCreateProxy))
	mux.HandleFunc("POST /api/v1/proxies/{id}/test", s.withAuth(s.handleTestProxy))
	mux.HandleFunc("PUT /api/v1/proxies/{id}", s.withAuth(s.handleUpdateProxy))
	mux.HandleFunc("DELETE /api/v1/proxies/{id}", s.withAuth(s.handleDeleteProxy))
	mux.HandleFunc("GET /api/v1/events", s.withAuth(s.handleEvents))

	return sameOrigin(s.allowedHosts, mux)
}

func (s *Server) withAuth(handler func(http.ResponseWriter, *http.Request, auth.Principal)) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if s.auth == nil {
			writeError(writer, http.StatusInternalServerError, "auth_unavailable", "authentication is unavailable", nil)
			return
		}
		principal, ok := s.auth.Principal(request.Context(), request)
		if !ok {
			writeError(writer, http.StatusUnauthorized, "auth_required", "authentication is required", nil)
			return
		}
		handler(writer, request, principal)
	}
}

func (s *Server) handleHealth(writer http.ResponseWriter, request *http.Request) {
	database := "ok"
	if s.config.DB == nil || s.config.DB.PingContext(request.Context()) != nil {
		database = "error"
	}
	status := "ok"
	if database != "ok" {
		status = "degraded"
	}
	writeJSON(writer, http.StatusOK, map[string]any{"status": status, "version": s.config.Version, "database": database, "downloadManager": "ok"})
}

func (s *Server) handleVersion(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{
		"version":   s.config.Version,
		"commit":    s.config.Commit,
		"buildTime": s.config.BuildTime,
	})
}

func (s *Server) handleDashboard(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	if s.config.DB == nil {
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "download storage is unavailable", nil)
		return
	}
	jobs, err := s.config.DB.ListDownloadJobs(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not read dashboard state", nil)
		return
	}
	var active, queued, waiting int
	var speed float64
	var sessionBytes int64
	for index := range jobs {
		job := &jobs[index]
		s.decorateDownload(job)
		switch download.JobState(job.State) {
		case download.JobReady, download.JobQueued:
			queued++
		case download.JobResolving, download.JobDownloading, download.JobFinalizing:
			active++
		case download.JobWaitingQuota:
			waiting++
		}
		speed += job.SpeedBytesPerSecond
		if s.downloads != nil {
			sessionBytes += s.downloads.Speed(job.ID).TotalBytes
		}
	}
	freeBytes := int64(0)
	if s.settings != nil {
		if value, settingsErr := s.settings.Get(request.Context()); settingsErr == nil {
			freeBytes = diskFreeBytes(value.Paths.CompleteRoot)
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"activeJobs":                 active,
		"queuedJobs":                 queued,
		"waitingQuotaJobs":           waiting,
		"currentSpeedBytesPerSecond": speed,
		"bytesDownloadedThisSession": sessionBytes,
		"diskFreeBytes":              freeBytes,
	})
}

func diskFreeBytes(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}

func (s *Server) handleAuthStatus(writer http.ResponseWriter, request *http.Request) {
	setupRequired := true
	if s.config.DB != nil {
		users, err := s.config.DB.HasUsers(request.Context())
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "database_error", "could not read authentication state", nil)
			return
		}
		setupRequired = !users
	}
	principal, authenticated := auth.Principal{}, false
	if s.auth != nil {
		principal, authenticated = s.auth.Principal(request.Context(), request)
	}
	data := map[string]any{"setupRequired": setupRequired, "authenticated": authenticated}
	if authenticated {
		data["user"] = principal
	}
	writeJSON(writer, http.StatusOK, data)
}

type credentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleSetup(writer http.ResponseWriter, request *http.Request) {
	if s.auth == nil || s.config.DB == nil {
		writeError(writer, http.StatusInternalServerError, "auth_unavailable", "authentication is unavailable", nil)
		return
	}
	users, err := s.config.DB.HasUsers(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not read authentication state", nil)
		return
	}
	if users {
		writeError(writer, http.StatusConflict, "setup_complete", "administrator setup has already been completed", nil)
		return
	}
	var input credentialsRequest
	if !decodeJSON(writer, request, &input, maxSetupBody) {
		return
	}
	principal, token, err := s.auth.Setup(request.Context(), input.Username, input.Password)
	if err != nil {
		if errors.Is(err, store.ErrAdminExists) {
			writeError(writer, http.StatusConflict, "setup_complete", "administrator setup has already been completed", nil)
			return
		}
		writeError(writer, http.StatusBadRequest, "setup_invalid", err.Error(), nil)
		return
	}
	s.auth.SetCookie(writer, request, token)
	writeJSON(writer, http.StatusCreated, principal)
}

func (s *Server) handleLogin(writer http.ResponseWriter, request *http.Request) {
	if s.auth == nil {
		writeError(writer, http.StatusInternalServerError, "auth_unavailable", "authentication is unavailable", nil)
		return
	}
	var input credentialsRequest
	if !decodeJSON(writer, request, &input, maxJSONBody) {
		return
	}
	principal, token, err := s.auth.Login(request.Context(), input.Username, input.Password)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_credentials", "invalid username or password", nil)
		return
	}
	s.auth.SetCookie(writer, request, token)
	writeJSON(writer, http.StatusOK, principal)
}

func (s *Server) handleLogout(writer http.ResponseWriter, request *http.Request) {
	if s.auth != nil {
		if err := s.auth.Logout(request.Context(), request); err != nil {
			writeError(writer, http.StatusInternalServerError, "database_error", "could not end session", nil)
			return
		}
		s.auth.ClearCookie(writer, request)
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"loggedOut": true})
}

func (s *Server) handleMe(writer http.ResponseWriter, request *http.Request) {
	if s.auth == nil {
		writeError(writer, http.StatusInternalServerError, "auth_unavailable", "authentication is unavailable", nil)
		return
	}
	principal, ok := s.auth.Principal(request.Context(), request)
	if !ok {
		writeError(writer, http.StatusUnauthorized, "auth_required", "authentication is required", nil)
		return
	}
	writeJSON(writer, http.StatusOK, principal)
}

func (s *Server) handleGetSettings(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	if s.settings == nil {
		writeError(writer, http.StatusInternalServerError, "settings_unavailable", "settings are unavailable", nil)
		return
	}
	value, err := s.settings.Get(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "settings_error", "could not read settings", nil)
		return
	}
	writeJSON(writer, http.StatusOK, value)
}

func (s *Server) handlePutSettings(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	if s.settings == nil {
		writeError(writer, http.StatusInternalServerError, "settings_unavailable", "settings are unavailable", nil)
		return
	}
	var value settings.Settings
	if !decodeJSON(writer, request, &value, maxSettingsBody) {
		return
	}
	if err := s.settings.Update(request.Context(), value); err != nil {
		writeError(writer, http.StatusBadRequest, "settings_invalid", err.Error(), nil)
		return
	}
	s.events.Publish(events.Event{Name: events.SettingsUpdated, Timestamp: time.Now().UTC(), Data: map[string]any{"settings": value}})
	writeJSON(writer, http.StatusOK, value)
}

type resolveRequest struct {
	URL       string `json:"url"`
	AccountID string `json:"accountId"`
}

type resolvedFileResponse struct {
	NodeID       string `json:"nodeId"`
	RelativePath string `json:"relativePath"`
	Size         int64  `json:"size"`
}

type resolvedResponse struct {
	Kind        mega.LinkKind          `json:"kind"`
	DisplayName string                 `json:"displayName"`
	TotalBytes  int64                  `json:"totalBytes"`
	FileCount   int                    `json:"fileCount"`
	Files       []resolvedFileResponse `json:"files"`
}

func (s *Server) resolve(ctx context.Context, input resolveRequest) (mega.PublicLink, mega.ResolvedJob, resolvedResponse, error) {
	if strings.TrimSpace(input.URL) == "" {
		return mega.PublicLink{}, mega.ResolvedJob{}, resolvedResponse{}, fmt.Errorf("URL is required")
	}
	link, err := mega.ParseLink(input.URL)
	if err != nil {
		return mega.PublicLink{}, mega.ResolvedJob{}, resolvedResponse{}, err
	}
	if s.mega == nil {
		return mega.PublicLink{}, mega.ResolvedJob{}, resolvedResponse{}, fmt.Errorf("MEGA resolver is unavailable")
	}
	client, err := s.clientForAccount(ctx, input.AccountID)
	if err != nil {
		return mega.PublicLink{}, mega.ResolvedJob{}, resolvedResponse{}, err
	}
	job, err := client.ResolveLink(ctx, input.URL, input.AccountID)
	if err != nil {
		return mega.PublicLink{}, mega.ResolvedJob{}, resolvedResponse{}, err
	}
	files := make([]resolvedFileResponse, 0, len(job.Files))
	for _, file := range job.Files {
		safePath, err := fsroot.SanitizeRelativePath(file.RelativePath)
		if err != nil {
			return mega.PublicLink{}, mega.ResolvedJob{}, resolvedResponse{}, fmt.Errorf("remote path %q: %w", file.RelativePath, err)
		}
		files = append(files, resolvedFileResponse{NodeID: file.NodeID, RelativePath: safePath, Size: file.Size})
	}
	return link, job, resolvedResponse{Kind: job.Kind, DisplayName: job.DisplayName, TotalBytes: job.TotalBytes, FileCount: len(files), Files: files}, nil
}

func (s *Server) handleResolve(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	var input resolveRequest
	if !decodeJSON(writer, request, &input, maxResolveBody) {
		return
	}
	_, _, response, err := s.resolve(request.Context(), input)
	if err != nil {
		writeError(writer, http.StatusBadRequest, errorCode(err), err.Error(), nil)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (s *Server) clientForAccount(ctx context.Context, accountID string) (*mega.Client, error) {
	if accountID == "" {
		return s.mega, nil
	}
	if s.config.DB == nil || s.config.Secrets == nil {
		return nil, fmt.Errorf("account storage is unavailable")
	}
	account, err := s.config.DB.GetMegaAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	credentialCipher, sessionCipher, err := s.config.DB.MegaAccountSecrets(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if len(sessionCipher) > 0 {
		session, err := s.config.Secrets.Decrypt(sessionCipher)
		if err == nil && len(session) > 0 {
			return s.mega.WithSession(string(session)), nil
		}
	}
	credential, err := s.config.Secrets.Decrypt(credentialCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt account credential: %w", err)
	}
	session, err := s.mega.LoginAccount(account.Email, string(credential))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "multi-factor") {
			return nil, fmt.Errorf("MFA-enabled accounts are not supported in this version")
		}
		return nil, fmt.Errorf("MEGA account login failed")
	}
	sessionCipher, err = s.config.Secrets.Encrypt([]byte(session))
	if err != nil {
		return nil, err
	}
	if err := s.config.DB.UpdateMegaAccount(ctx, accountID, account.Label, account.Email, nil, sessionCipher, "active", account.DefaultForDownloads, time.Now()); err != nil {
		return nil, err
	}
	return s.mega.WithSession(session), nil
}

type accountRequest struct {
	Label               string `json:"label"`
	Email               string `json:"email"`
	Password            string `json:"password"`
	DefaultForDownloads *bool  `json:"defaultForDownloads"`
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	accounts, err := s.config.DB.ListMegaAccounts(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "could not list accounts", nil)
		return
	}
	writeJSON(w, 200, accounts)
}
func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	var in accountRequest
	if !decodeJSON(w, r, &in, maxJSONBody) {
		return
	}
	if strings.TrimSpace(in.Label) == "" || strings.TrimSpace(in.Email) == "" || in.Password == "" {
		writeError(w, 400, "account_invalid", "label, email, and password are required", nil)
		return
	}
	session, err := s.mega.LoginAccount(in.Email, in.Password)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "multi-factor") {
			writeError(w, 400, "account_mfa_unsupported", "MFA-enabled accounts are not supported in this version", nil)
		} else {
			writeError(w, 400, "account_login_failed", "MEGA account login failed", nil)
		}
		return
	}
	credential, err := s.config.Secrets.Encrypt([]byte(in.Password))
	if err != nil {
		writeError(w, 500, "encryption_error", "could not protect account credential", nil)
		return
	}
	sessionCipher, err := s.config.Secrets.Encrypt([]byte(session))
	if err != nil {
		writeError(w, 500, "encryption_error", "could not protect account session", nil)
		return
	}
	def := in.DefaultForDownloads != nil && *in.DefaultForDownloads
	record, err := s.config.DB.InsertMegaAccount(r.Context(), store.MegaAccountInput{Label: in.Label, Email: in.Email, CredentialCiphertext: credential, SessionCiphertext: sessionCipher, Status: "active", DefaultForDownloads: def}, time.Now())
	if err != nil {
		writeError(w, 500, "database_error", "could not save account", nil)
		return
	}
	s.events.Publish(events.Event{Name: events.AccountUpdated, Timestamp: time.Now().UTC(), Data: map[string]any{"id": record.ID}})
	writeJSON(w, http.StatusCreated, record)
}
func (s *Server) handleTestAccount(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	id := r.PathValue("id")
	account, err := s.config.DB.GetMegaAccount(r.Context(), id)
	if err != nil {
		writeError(w, 404, "account_not_found", "account was not found", nil)
		return
	}
	credential, _, err := s.config.DB.MegaAccountSecrets(r.Context(), id)
	if err != nil {
		writeError(w, 500, "database_error", "could not read account", nil)
		return
	}
	password, err := s.config.Secrets.Decrypt(credential)
	if err != nil {
		writeError(w, 500, "encryption_error", "could not decrypt account credential", nil)
		return
	}
	if _, err = s.mega.LoginAccount(account.Email, string(password)); err != nil {
		_ = s.config.DB.MarkMegaAccountChecked(r.Context(), id, "error", time.Now())
		writeError(w, 400, "account_login_failed", "MEGA account login failed", nil)
		return
	}
	_ = s.config.DB.MarkMegaAccountChecked(r.Context(), id, "active", time.Now())
	writeJSON(w, 200, map[string]string{"status": "active"})
}
func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	id := r.PathValue("id")
	account, err := s.config.DB.GetMegaAccount(r.Context(), id)
	if err != nil {
		writeError(w, 404, "account_not_found", "account was not found", nil)
		return
	}
	var in accountRequest
	if !decodeJSON(w, r, &in, maxJSONBody) {
		return
	}
	if in.Label == "" {
		in.Label = account.Label
	}
	if in.Email == "" {
		in.Email = account.Email
	}
	def := account.DefaultForDownloads
	if in.DefaultForDownloads != nil {
		def = *in.DefaultForDownloads
	}
	var credential, session []byte
	if in.Password != "" {
		sessionID, loginErr := s.mega.LoginAccount(in.Email, in.Password)
		if loginErr != nil {
			writeError(w, 400, "account_login_failed", "MEGA account login failed", nil)
			return
		}
		credential, err = s.config.Secrets.Encrypt([]byte(in.Password))
		if err != nil {
			writeError(w, 500, "encryption_error", "could not protect account credential", nil)
			return
		}
		session, err = s.config.Secrets.Encrypt([]byte(sessionID))
		if err != nil {
			writeError(w, 500, "encryption_error", "could not protect account session", nil)
			return
		}
	}
	if err := s.config.DB.UpdateMegaAccount(r.Context(), id, in.Label, in.Email, credential, session, account.Status, def, time.Now()); err != nil {
		writeError(w, 400, "account_update_failed", err.Error(), nil)
		return
	}
	record, _ := s.config.DB.GetMegaAccount(r.Context(), id)
	s.events.Publish(events.Event{Name: events.AccountUpdated, Timestamp: time.Now().UTC(), Data: map[string]any{"id": id}})
	writeJSON(w, 200, record)
}
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	if err := s.config.DB.DeleteMegaAccount(r.Context(), r.PathValue("id")); errors.Is(err, store.ErrRecordInUse) {
		writeError(w, http.StatusConflict, "account_in_use", "account is selected by a persisted download", nil)
		return
	} else if err != nil {
		writeError(w, 404, "account_not_found", "account was not found", nil)
		return
	}
	s.events.Publish(events.Event{Name: events.AccountUpdated, Timestamp: time.Now().UTC(), Data: map[string]any{"id": r.PathValue("id"), "deleted": true}})
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

type proxyRequest struct {
	Name                string `json:"name"`
	Type                string `json:"type"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	Username            string `json:"username"`
	Password            string `json:"password"`
	TimeoutSeconds      int    `json:"timeoutSeconds"`
	Enabled             *bool  `json:"enabled"`
	DefaultForDownloads *bool  `json:"defaultForDownloads"`
	URL                 string `json:"url"`
}

func (s *Server) proxyInput(r proxyRequest, password []byte) (store.ProxyProfileInput, error) {
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	def := false
	if r.DefaultForDownloads != nil {
		def = *r.DefaultForDownloads
	}
	if r.TimeoutSeconds == 0 {
		r.TimeoutSeconds = 15
	}
	profile := network.ProxyProfile{Type: network.ProxyType(r.Type), Host: r.Host, Port: r.Port, Timeout: time.Duration(r.TimeoutSeconds) * time.Second}
	if err := profile.Validate(); err != nil {
		return store.ProxyProfileInput{}, err
	}
	return store.ProxyProfileInput{Name: r.Name, Type: r.Type, Host: r.Host, Port: r.Port, Username: r.Username, PasswordCiphertext: password, TimeoutSeconds: r.TimeoutSeconds, Enabled: enabled, DefaultForDownloads: def}, nil
}
func (s *Server) handleListProxies(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	items, err := s.config.DB.ListProxyProfiles(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "could not list proxies", nil)
		return
	}
	writeJSON(w, 200, items)
}
func (s *Server) handleCreateProxy(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	var in proxyRequest
	if !decodeJSON(w, r, &in, maxJSONBody) {
		return
	}
	password, err := s.config.Secrets.Encrypt([]byte(in.Password))
	if err != nil {
		writeError(w, 500, "encryption_error", "could not protect proxy password", nil)
		return
	}
	value, err := s.proxyInput(in, password)
	if err != nil {
		writeError(w, 400, "proxy_invalid", err.Error(), nil)
		return
	}
	record, err := s.config.DB.InsertProxyProfile(r.Context(), value, time.Now())
	if err != nil {
		writeError(w, 400, "proxy_create_failed", err.Error(), nil)
		return
	}
	s.events.Publish(events.Event{Name: events.SettingsUpdated, Timestamp: time.Now().UTC(), Data: map[string]any{"proxyId": record.ID}})
	writeJSON(w, http.StatusCreated, record)
}
func (s *Server) proxyClient(r *http.Request, id string) (*http.Client, error) {
	profile, err := s.config.DB.GetProxyProfile(r.Context(), id)
	if err != nil {
		return nil, err
	}
	ciphertext, err := s.config.DB.ProxySecret(r.Context(), id)
	if err != nil {
		return nil, err
	}
	password, err := s.config.Secrets.Decrypt(ciphertext)
	if err != nil && len(ciphertext) > 0 {
		return nil, err
	}
	return s.transports.Client(network.ProxyProfile{ID: profile.ID, Name: profile.Name, Type: network.ProxyType(profile.Type), Host: profile.Host, Port: profile.Port, Username: profile.Username, Password: string(password), Timeout: time.Duration(profile.TimeoutSeconds) * time.Second, Enabled: profile.Enabled})
}
func (s *Server) handleTestProxy(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	client, err := s.proxyClient(r, r.PathValue("id"))
	if err != nil {
		writeError(w, 400, "proxy_invalid", err.Error(), nil)
		return
	}
	var input struct {
		URL string `json:"url"`
	}
	_ = decodeJSON(rwDiscard{}, r, &input, maxJSONBody)
	target := input.URL
	if target == "" {
		target = "https://g.api.mega.co.nz/"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err == nil {
		resp, requestErr := client.Do(req)
		if requestErr == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 400 {
				writeError(w, 400, "proxy_test_failed", fmt.Sprintf("proxy request returned HTTP %d", resp.StatusCode), nil)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "status": resp.StatusCode})
			return
		}
		err = requestErr
	}
	writeError(w, 400, "proxy_test_failed", "proxy request failed", nil)
}
func (s *Server) handleUpdateProxy(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	id := r.PathValue("id")
	if _, err := s.config.DB.GetProxyProfile(r.Context(), id); err != nil {
		writeError(w, 404, "proxy_not_found", "proxy was not found", nil)
		return
	}
	var in proxyRequest
	if !decodeJSON(w, r, &in, maxJSONBody) {
		return
	}
	var password []byte
	if in.Password != "" {
		password, _ = s.config.Secrets.Encrypt([]byte(in.Password))
	}
	value, err := s.proxyInput(in, password)
	if err != nil {
		writeError(w, 400, "proxy_invalid", err.Error(), nil)
		return
	}
	if err := s.config.DB.UpdateProxyProfile(r.Context(), id, value, time.Now()); err != nil {
		writeError(w, 400, "proxy_update_failed", err.Error(), nil)
		return
	}
	if s.transports != nil {
		s.transports.Remove(id)
	}
	record, _ := s.config.DB.GetProxyProfile(r.Context(), id)
	s.events.Publish(events.Event{Name: events.SettingsUpdated, Timestamp: time.Now().UTC(), Data: map[string]any{"proxyId": id}})
	writeJSON(w, 200, record)
}
func (s *Server) handleDeleteProxy(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	id := r.PathValue("id")
	if err := s.config.DB.DeleteProxyProfile(r.Context(), id); errors.Is(err, store.ErrRecordInUse) {
		writeError(w, http.StatusConflict, "proxy_in_use", "proxy is selected by a persisted download", nil)
		return
	} else if err != nil {
		writeError(w, 404, "proxy_not_found", "proxy was not found", nil)
		return
	}
	if s.transports != nil {
		s.transports.Remove(id)
	}
	s.events.Publish(events.Event{Name: events.SettingsUpdated, Timestamp: time.Now().UTC(), Data: map[string]any{"proxyId": id, "deleted": true}})
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

// rwDiscard is only used to parse the optional proxy test body while keeping
// the normal error response path in the shared helper. A malformed body is
// treated as an absent optional URL.
type rwDiscard struct{}

func (rwDiscard) Header() http.Header       { return http.Header{} }
func (rwDiscard) Write([]byte) (int, error) { return 0, nil }
func (rwDiscard) WriteHeader(int)           {}

type createDownloadRequest struct {
	URL                     string `json:"url"`
	AccountID               string `json:"accountId"`
	ProxyID                 string `json:"proxyId"`
	DestinationSubdirectory string `json:"destinationSubdirectory"`
	StartImmediately        *bool  `json:"startImmediately"`
}

func (s *Server) handleCreateDownload(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	var input createDownloadRequest
	if !decodeJSON(writer, request, &input, maxResolveBody) {
		return
	}
	if input.AccountID != "" {
		if _, err := s.config.DB.GetMegaAccount(request.Context(), input.AccountID); err != nil {
			writeError(writer, http.StatusBadRequest, "account_invalid", "selected account was not found", nil)
			return
		}
	}
	if input.ProxyID != "" {
		profile, err := s.config.DB.GetProxyProfile(request.Context(), input.ProxyID)
		if err != nil || !profile.Enabled {
			writeError(writer, http.StatusBadRequest, "proxy_invalid", "selected proxy profile is unavailable", nil)
			return
		}
	}
	destination, err := fsroot.SanitizeDestinationSubdirectory(input.DestinationSubdirectory)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "destination_invalid", err.Error(), nil)
		return
	}
	link, job, _, err := s.resolve(request.Context(), resolveRequest{URL: input.URL, AccountID: input.AccountID})
	if err != nil {
		writeError(writer, http.StatusBadRequest, errorCode(err), err.Error(), nil)
		return
	}
	if s.config.Secrets == nil || s.settings == nil || s.config.DB == nil {
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "download storage is unavailable", nil)
		return
	}
	currentSettings, err := s.settings.Get(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "settings_error", "could not read settings", nil)
		return
	}
	sourceCiphertext, err := s.config.Secrets.Encrypt([]byte(link.Key))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "encryption_error", "could not protect download source", nil)
		return
	}
	jobID := randomID()
	startImmediately := currentSettings.Downloads.AutoStart
	if input.StartImmediately != nil {
		startImmediately = *input.StartImmediately
	}
	state := "ready"
	if startImmediately {
		state = "queued"
	}
	files := make([]store.DownloadFileInput, 0, len(job.Files))
	seenPaths := make(map[string]struct{}, len(job.Files))
	completeRoot, err := fsroot.New(currentSettings.Paths.CompleteRoot)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "settings_error", "configured complete root is invalid", nil)
		return
	}
	for _, file := range job.Files {
		relativePath, err := fsroot.SanitizeRelativePath(file.RelativePath)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "destination_invalid", err.Error(), nil)
			return
		}
		relativePath = uniqueRelativePath(relativePath, seenPaths)
		finalRelativePath := joinRelative(destination, relativePath)
		for {
			plannedPath, planErr := completeRoot.PlanConflict(finalRelativePath, currentSettings.Downloads.ConflictPolicy)
			if planErr != nil {
				writeError(writer, http.StatusBadRequest, "destination_conflict", planErr.Error(), nil)
				return
			}
			plannedRelativePath, relErr := filepath.Rel(completeRoot.Path(), plannedPath)
			if relErr != nil {
				writeError(writer, http.StatusInternalServerError, "destination_invalid", "could not plan destination path", nil)
				return
			}
			if _, alreadyUsed := seenPaths[plannedRelativePath]; alreadyUsed {
				finalRelativePath = uniqueRelativePath(plannedRelativePath, seenPaths)
				continue
			}
			finalRelativePath = plannedRelativePath
			seenPaths[finalRelativePath] = struct{}{}
			break
		}
		keyCiphertext, err := s.config.Secrets.Encrypt(file.FileKey)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "encryption_error", "could not protect file key", nil)
			return
		}
		payloadCiphertext, err := s.config.Secrets.Encrypt([]byte(file.PayloadURL))
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "encryption_error", "could not protect payload location", nil)
			return
		}
		segmentCount := int64(0)
		if file.Size > 0 {
			segmentCount = (file.Size + currentSettings.Downloads.SegmentSizeBytes - 1) / currentSettings.Downloads.SegmentSizeBytes
		}
		files = append(files, store.DownloadFileInput{
			ID:                   randomID(),
			RemoteNodeID:         file.NodeID,
			RemotePath:           relativePath,
			FinalRelativePath:    finalRelativePath,
			SizeBytes:            file.Size,
			SegmentSizeBytes:     currentSettings.Downloads.SegmentSizeBytes,
			SegmentCount:         segmentCount,
			FileKeyCiphertext:    keyCiphertext,
			PayloadURLCiphertext: payloadCiphertext,
			PayloadContext:       file.PayloadContext,
			State:                "pending",
		})
	}
	record, err := s.config.DB.InsertDownloadJob(request.Context(), store.DownloadJobInput{
		ID:                  jobID,
		SourceKind:          string(link.Kind),
		SourceHandle:        link.Handle,
		SourceKeyCiphertext: sourceCiphertext,
		SourceSelectedPath:  link.SelectedPath,
		SourceSelectedNode:  link.SelectedNode,
		DisplayName:         job.DisplayName,
		TotalBytes:          job.TotalBytes,
		DestinationSubdir:   destination,
		AccountID:           input.AccountID,
		ProxyID:             input.ProxyID,
		CompleteRoot:        currentSettings.Paths.CompleteRoot,
		IncompleteRoot:      currentSettings.Paths.IncompleteRoot,
		State:               state,
		Files:               files,
	})
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not create download", nil)
		return
	}
	if state == "queued" && s.downloads != nil {
		if err := s.downloads.StartJob(record.ID); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "download_queue_unavailable", "download was saved but could not be started", nil)
			return
		}
	}
	s.events.Publish(events.Event{
		Name:      events.JobUpdated,
		JobID:     record.ID,
		Timestamp: time.Now().UTC(),
		Data:      map[string]any{"id": record.ID, "totalBytes": record.TotalBytes, "bytesCommitted": int64(0)},
	})
	writeJSON(writer, http.StatusCreated, record)
}

func (s *Server) handleListDownloads(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	if s.config.DB == nil {
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "download storage is unavailable", nil)
		return
	}
	records, err := s.config.DB.ListDownloadJobs(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not list downloads", nil)
		return
	}
	for index := range records {
		s.decorateDownload(&records[index])
	}
	writeJSON(writer, http.StatusOK, records)
}

func (s *Server) decorateDownload(record *store.DownloadJobRecord) {
	if record == nil {
		return
	}
	if s.downloads != nil {
		snapshot := s.downloads.Speed(record.ID)
		record.SpeedBytesPerSecond = snapshot.BytesPerSecond
		remaining := record.TotalBytes - record.BytesCommitted
		if remaining > 0 && snapshot.BytesPerSecond > 0 {
			record.ETASeconds = int64(float64(remaining) / snapshot.BytesPerSecond)
		}
	}
	if s.config.DB != nil {
		if record.AccountID != "" {
			if account, err := s.config.DB.GetMegaAccount(context.Background(), record.AccountID); err == nil {
				record.AccountLabel = account.Label
			}
		}
		if record.ProxyID != "" {
			if proxy, err := s.config.DB.GetProxyProfile(context.Background(), record.ProxyID); err == nil {
				record.ProxyLabel = proxy.Name
			}
		}
	}
}

func (s *Server) handleGetDownload(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	if s.config.DB == nil {
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "download storage is unavailable", nil)
		return
	}
	id := request.PathValue("id")
	if id == "" {
		writeError(writer, http.StatusBadRequest, "download_invalid", "download id is required", nil)
		return
	}
	record, err := s.config.DB.GetDownloadJobDetail(request.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "download_not_found", "download was not found", nil)
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not read download", nil)
		return
	}
	s.decorateDownload(&record)
	writeJSON(writer, http.StatusOK, record)
}

func (s *Server) handleDownloadEvents(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	if s.config.DB == nil {
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "download storage is unavailable", nil)
		return
	}
	if _, err := s.config.DB.GetDownloadJob(request.Context(), request.PathValue("id")); errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "download_not_found", "download was not found", nil)
		return
	} else if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not read download", nil)
		return
	}
	items, err := s.config.DB.ListDownloadEvents(request.Context(), request.PathValue("id"), 200)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not read download events", nil)
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (s *Server) handlePauseDownload(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	if s.downloads == nil {
		writeError(writer, http.StatusServiceUnavailable, "download_manager_unavailable", "download manager is unavailable", nil)
		return
	}
	if err := s.downloads.PauseJob(request.Context(), request.PathValue("id")); err != nil {
		writeError(writer, http.StatusBadRequest, "download_pause_failed", err.Error(), nil)
		return
	}
	s.writeDownloadActionResult(writer, request)
}

func (s *Server) handleResumeDownload(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	if s.downloads == nil {
		writeError(writer, http.StatusServiceUnavailable, "download_manager_unavailable", "download manager is unavailable", nil)
		return
	}
	if err := s.downloads.ResumeJob(request.Context(), request.PathValue("id")); err != nil {
		writeError(writer, http.StatusBadRequest, "download_resume_failed", err.Error(), nil)
		return
	}
	s.writeDownloadActionResult(writer, request)
}

func (s *Server) handleRetryDownload(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	if s.downloads == nil {
		writeError(writer, http.StatusServiceUnavailable, "download_manager_unavailable", "download manager is unavailable", nil)
		return
	}
	if err := s.downloads.ResumeJob(request.Context(), request.PathValue("id")); err != nil {
		writeError(writer, http.StatusBadRequest, "download_retry_failed", err.Error(), nil)
		return
	}
	s.writeDownloadActionResult(writer, request)
}

type cancelDownloadRequest struct {
	DeletePartialFiles bool `json:"deletePartialFiles"`
}

func (s *Server) handleCancelDownload(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	if s.downloads == nil {
		writeError(writer, http.StatusServiceUnavailable, "download_manager_unavailable", "download manager is unavailable", nil)
		return
	}
	var input cancelDownloadRequest
	if !decodeJSON(writer, request, &input, maxJSONBody) {
		return
	}
	if err := s.downloads.CancelJob(request.Context(), request.PathValue("id"), input.DeletePartialFiles); err != nil {
		writeError(writer, http.StatusBadRequest, "download_cancel_failed", err.Error(), nil)
		return
	}
	s.writeDownloadActionResult(writer, request)
}

func (s *Server) handleDeleteDownload(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	if s.config.DB == nil {
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "download storage is unavailable", nil)
		return
	}
	deleteFiles := false
	if raw := request.URL.Query().Get("deleteFiles"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "delete_invalid", "deleteFiles must be true or false", nil)
			return
		}
		deleteFiles = value
	}
	if s.downloads == nil {
		writeError(writer, http.StatusServiceUnavailable, "download_manager_unavailable", "download manager is unavailable", nil)
		return
	}
	if err := s.downloads.DeleteJob(request.Context(), request.PathValue("id"), deleteFiles); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(writer, http.StatusNotFound, "download_not_found", "download was not found", nil)
			return
		}
		writeError(writer, http.StatusBadRequest, "download_delete_failed", err.Error(), nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) writeDownloadActionResult(writer http.ResponseWriter, request *http.Request) {
	if s.config.DB == nil {
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "download storage is unavailable", nil)
		return
	}
	record, err := s.config.DB.GetDownloadJobDetail(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not read download after action", nil)
		return
	}
	s.decorateDownload(&record)
	writeJSON(writer, http.StatusOK, record)
}

func (s *Server) handlePauseQueue(writer http.ResponseWriter, _ *http.Request, _ auth.Principal) {
	if s.downloads == nil {
		writeError(writer, http.StatusServiceUnavailable, "download_manager_unavailable", "download manager is unavailable", nil)
		return
	}
	s.downloads.PauseQueue()
	writeJSON(writer, http.StatusOK, map[string]bool{"paused": true})
}

func (s *Server) handleResumeQueue(writer http.ResponseWriter, _ *http.Request, _ auth.Principal) {
	if s.downloads == nil {
		writeError(writer, http.StatusServiceUnavailable, "download_manager_unavailable", "download manager is unavailable", nil)
		return
	}
	s.downloads.ResumeQueue()
	writeJSON(writer, http.StatusOK, map[string]bool{"paused": false})
}

func (s *Server) handleEvents(writer http.ResponseWriter, request *http.Request, _ auth.Principal) {
	_, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusInternalServerError, "sse_unavailable", "the server does not support event streaming", nil)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache, no-store")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(writer)
	writeEvent := func(payload string) error {
		// A disconnected or non-reading peer must not retain an SSE handler and
		// its socket forever. The server-wide WriteTimeout is intentionally zero
		// for streams, so bound each individual write instead.
		if err := controller.SetWriteDeadline(time.Now().Add(sseWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if _, err := io.WriteString(writer, payload); err != nil {
			return err
		}
		if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		return nil
	}
	subscription := s.events.Subscribe(request.Context())
	defer subscription.Close()
	// Register before flushing the prelude. Once a client observes the
	// connected marker, every subsequently published event must have a live
	// subscriber rather than falling into a pre-subscription race window.
	if err := writeEvent(": connected\n\n"); err != nil {
		return
	}
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-subscription.Done():
			return
		case event, open := <-subscription.Events():
			if !open {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if err := writeEvent(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Name, payload)); err != nil {
				return
			}
		case <-keepAlive.C:
			if err := writeEvent(": keep-alive\n\n"); err != nil {
				return
			}
		}
	}
}

func randomID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return fmt.Sprintf("%x", raw[:])
	}
	// The database also has a cryptographic identifier fallback. This branch
	// is only for an OS entropy failure and is deliberately not used for
	// session tokens.
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func joinRelative(prefix, value string) string {
	if prefix == "" {
		return value
	}
	if value == "" {
		return prefix
	}
	return prefix + "/" + value
}

func uniqueRelativePath(value string, seen map[string]struct{}) string {
	if _, ok := seen[value]; !ok {
		return value
	}
	base, ext := value, ""
	if index := strings.LastIndexByte(value, '.'); index > 0 {
		base, ext = value[:index], value[index:]
	}
	for index := 1; ; index++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, index, ext)
		if _, ok := seen[candidate]; !ok {
			return candidate
		}
	}
}

func errorCode(err error) string {
	if errors.Is(err, mega.ErrInvalidLink) || errors.Is(err, mega.ErrInvalidKey) {
		return "link_invalid"
	}
	return "resolve_failed"
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any, limit int64) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, limit)
	defer request.Body.Close()
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		code := "invalid_json"
		if errors.Is(err, io.EOF) {
			code = "empty_body"
		}
		writeError(writer, http.StatusBadRequest, code, "request body is invalid", nil)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one JSON value", nil)
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, status int, data any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"data": data})
}

func writeError(writer http.ResponseWriter, status int, code, message string, details any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "details": details}})
}

func sameOrigin(allowedHosts map[string]struct{}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost || request.Method == http.MethodPut || request.Method == http.MethodPatch || request.Method == http.MethodDelete {
			if !requestHostAllowed(request, allowedHosts) || !requestHasSameOrigin(request) {
				writeError(writer, http.StatusForbidden, "origin_forbidden", "request origin is not allowed", nil)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func requestHostAllowed(request *http.Request, allowedHosts map[string]struct{}) bool {
	if len(allowedHosts) == 0 {
		return request != nil && request.Host != ""
	}
	if request == nil {
		return false
	}
	_, ok := allowedHosts[normalizeHost(request.Host)]
	return ok
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

func requestHasSameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		// Non-browser clients do not send Origin. Host is still checked by the
		// server and cookies remain SameSite=Lax; when Origin is present it is
		// always validated strictly.
		return request.Host != ""
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Host != request.Host {
		return false
	}
	if request.TLS != nil {
		return parsed.Scheme == "https"
	}
	return parsed.Scheme == "http"
}
