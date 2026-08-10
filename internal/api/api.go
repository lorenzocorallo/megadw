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
	"strings"
	"time"

	"github.com/lorenzocorallo/megadw/internal/auth"
	"github.com/lorenzocorallo/megadw/internal/download"
	"github.com/lorenzocorallo/megadw/internal/fsroot"
	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
)

const (
	maxJSONBody       = 256 << 10
	maxResolveBody    = 256 << 10
	maxSetupBody      = 256 << 10
	maxSettingsBody   = 256 << 10
	defaultAPIVersion = "dev"
)

type Config struct {
	DB            *store.DB
	Secrets       *store.SecretStore
	Settings      *settings.Service
	Auth          *auth.Manager
	Mega          *mega.Client
	Downloads     *download.Manager
	Version       string
	SecureCookies bool
	Now           func() time.Time
}

type Server struct {
	config    Config
	auth      *auth.Manager
	settings  *settings.Service
	mega      *mega.Client
	downloads *download.Manager
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
		config.Version = defaultAPIVersion
	}
	return &Server{config: config, auth: config.Auth, settings: config.Settings, mega: config.Mega, downloads: config.Downloads}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/version", s.handleVersion)
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
	mux.HandleFunc("POST /api/v1/downloads/{id}/pause", s.withAuth(s.handlePauseDownload))
	mux.HandleFunc("POST /api/v1/downloads/{id}/resume", s.withAuth(s.handleResumeDownload))
	mux.HandleFunc("POST /api/v1/downloads/{id}/cancel", s.withAuth(s.handleCancelDownload))
	mux.HandleFunc("POST /api/v1/queue/pause", s.withAuth(s.handlePauseQueue))
	mux.HandleFunc("POST /api/v1/queue/resume", s.withAuth(s.handleResumeQueue))

	return sameOrigin(mux)
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
	writeJSON(writer, http.StatusOK, map[string]string{"version": s.config.Version})
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
	if input.AccountID != "" {
		return mega.PublicLink{}, mega.ResolvedJob{}, resolvedResponse{}, fmt.Errorf("account selection is not available yet")
	}
	if s.mega == nil {
		return mega.PublicLink{}, mega.ResolvedJob{}, resolvedResponse{}, fmt.Errorf("MEGA resolver is unavailable")
	}
	job, err := s.mega.ResolveLink(ctx, input.URL, input.AccountID)
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
	if input.AccountID != "" || input.ProxyID != "" {
		writeError(writer, http.StatusBadRequest, "selection_unavailable", "account and proxy selection are not available in this phase", nil)
		return
	}
	destination, err := fsroot.SanitizeDestinationSubdirectory(input.DestinationSubdirectory)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "destination_invalid", err.Error(), nil)
		return
	}
	link, job, _, err := s.resolve(request.Context(), resolveRequest{URL: input.URL})
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
	writeJSON(writer, http.StatusOK, records)
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
	record, err := s.config.DB.GetDownloadJob(request.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "download_not_found", "download was not found", nil)
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not read download", nil)
		return
	}
	writeJSON(writer, http.StatusOK, record)
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

func (s *Server) writeDownloadActionResult(writer http.ResponseWriter, request *http.Request) {
	if s.config.DB == nil {
		writeError(writer, http.StatusInternalServerError, "storage_unavailable", "download storage is unavailable", nil)
		return
	}
	record, err := s.config.DB.GetDownloadJob(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not read download after action", nil)
		return
	}
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

func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost || request.Method == http.MethodPut || request.Method == http.MethodPatch || request.Method == http.MethodDelete {
			if !requestHasSameOrigin(request) {
				writeError(writer, http.StatusForbidden, "origin_forbidden", "request origin is not allowed", nil)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
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
