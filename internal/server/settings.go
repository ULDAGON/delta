package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/internal/storage"
)

type settingsState struct {
	svc  *service.Service
	auth *authState

	configMu      sync.RWMutex
	config        config.Config
	configPath    string
	configPathErr error
	regenerateMu  sync.Mutex
}

type settingsResponse struct {
	DatabasePath    string `json:"database_path"`
	Key             string `json:"key"`
	APIAddress      string `json:"api_address"`
	Token           string `json:"token"`
	BackupsPath     string `json:"backups_path"`
	LastBackup      string `json:"last_backup,omitempty"`
	LastBackupError string `json:"last_backup_error,omitempty"`
}

type tokenRegenerateResponse struct {
	Token    string           `json:"token"`
	Settings settingsResponse `json:"settings"`
}

func newSettingsState(svc *service.Service, token string, supplied *config.Config, auth *authState) *settingsState {
	value := config.Config{
		APIToken:   token,
		APIAddress: config.DefaultAPIAddress,
	}
	if svc != nil && svc.Store != nil {
		value.DatabasePath = svc.Store.Path
		value.Key = svc.Store.Key
	}
	if supplied != nil {
		value = *supplied
		if value.DatabasePath == "" && svc != nil && svc.Store != nil {
			value.DatabasePath = svc.Store.Path
		}
		if value.Key == "" && svc != nil && svc.Store != nil {
			value.Key = svc.Store.Key
		}
		if value.APIAddress == "" {
			value.APIAddress = config.DefaultAPIAddress
		}
	}
	// The handler argument is the live auth source. Keeping it authoritative
	// also makes legacy NewHandler callers safe when they do not pass config.
	value.APIToken = token
	configPath, configPathErr := config.Path()
	return &settingsState{
		svc:           svc,
		auth:          auth,
		config:        value,
		configPath:    configPath,
		configPathErr: configPathErr,
	}
}

func registerSettingsRoutes(mux *http.ServeMux, state *settingsState) {
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			state.handleInfo(w, r)
		case http.MethodPatch:
			state.handlePatch(w, r)
		default:
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
		}
	})
	mux.HandleFunc("/api/settings/", func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.URL.Path, "/api/settings/") == "token/regenerate" {
			if r.Method == http.MethodPost {
				state.handleRegenerate(w, r)
				return
			}
		}
		writeServiceError(w, apperror.New(apperror.CodeNotFound, "not found"))
	})
}

func (s *settingsState) handleInfo(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.svc == nil || s.svc.Store == nil {
		writeServiceError(w, apperror.New(apperror.CodeInternalError, "server is not initialized"))
		return
	}
	reveal := r.URL.Query().Get("reveal")
	writeJSON(w, http.StatusOK, s.snapshot(reveal == "key", reveal == "token"))
}

func (s *settingsState) snapshot(revealKey, revealToken bool) settingsResponse {
	s.configMu.RLock()
	value := s.config
	s.configMu.RUnlock()
	result := settingsResponse{
		DatabasePath: value.DatabasePath,
		Key:          maskSecret(value.Key),
		APIAddress:   value.APIAddress,
		Token:        maskSecret(value.APIToken),
		BackupsPath:  storage.BackupDirectory(value.DatabasePath),
	}
	if revealKey {
		result.Key = value.Key
	}
	if revealToken {
		result.Token = value.APIToken
	}
	if last := s.svc.LastBackup(); last != nil {
		result.LastBackup = last.Format(time.RFC3339Nano)
	}
	if err := s.svc.LastBackupError(); err != nil {
		result.LastBackupError = err.Error()
	}
	return result
}

func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	length := len([]rune(value))
	if length > 24 {
		length = 24
	}
	return strings.Repeat("•", length)
}

func (s *settingsState) handlePatch(w http.ResponseWriter, r *http.Request) {
	if s.configPathErr != nil {
		writeServiceError(w, apperror.Wrap(apperror.CodeInternalError, "config path unavailable", s.configPathErr))
		return
	}
	fields, err := decodeSettingsObject(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	raw, ok := fields["key"]
	if !ok {
		writeServiceError(w, apperror.New(apperror.CodeInvalidSetup, "settings key is required"))
		return
	}
	var key string
	if err := json.Unmarshal(raw, &key); err != nil {
		writeServiceError(w, apperror.Wrap(apperror.CodeInvalidSetup, "settings key must be a string", err))
		return
	}
	key = storage.NormalizeKey(key)
	if err := storage.ValidateKey(key); err != nil {
		writeServiceError(w, err)
		return
	}
	updated, err := config.UpdateAt(s.configPath, func(value *config.Config) error {
		value.Key = key
		return nil
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	s.configMu.Lock()
	s.config = updated
	s.configMu.Unlock()
	response := s.snapshot(false, false)
	writeJSON(w, http.StatusOK, struct {
		Settings        settingsResponse `json:"settings"`
		RestartRequired bool             `json:"restart_required"`
	}{Settings: response, RestartRequired: true})
}

func (s *settingsState) handleRegenerate(w http.ResponseWriter, _ *http.Request) {
	if s.configPathErr != nil {
		writeServiceError(w, apperror.Wrap(apperror.CodeInternalError, "config path unavailable", s.configPathErr))
		return
	}
	s.regenerateMu.Lock()
	defer s.regenerateMu.Unlock()

	token, err := config.NewToken()
	if err != nil {
		writeServiceError(w, apperror.Wrap(apperror.CodeInternalError, "generate API token", err))
		return
	}
	updated, err := config.UpdateAt(s.configPath, func(value *config.Config) error {
		value.APIToken = token
		return nil
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	s.configMu.Lock()
	s.config = updated
	s.configMu.Unlock()
	// Replace the auth token before writing the response. Any request arriving
	// after this point must use the newly persisted token.
	s.auth.replace(token)
	response := s.snapshot(false, false)
	writeJSON(w, http.StatusOK, tokenRegenerateResponse{Token: token, Settings: response})
}

func decodeSettingsObject(r *http.Request) (map[string]json.RawMessage, error) {
	if r.Body == nil {
		return nil, apperror.New(apperror.CodeInvalidSetup, "settings JSON is required")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, apperror.Wrap(apperror.CodeInvalidSetup, "invalid settings JSON", err)
	}
	if strings.TrimSpace(string(body)) == "" {
		return nil, apperror.New(apperror.CodeInvalidSetup, "settings JSON is required")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, apperror.Wrap(apperror.CodeInvalidSetup, "invalid settings JSON", err)
	}
	for field := range fields {
		if field != "key" {
			return nil, apperror.New(apperror.CodeInvalidSetup, "unknown settings field: "+field)
		}
	}
	return fields, nil
}
