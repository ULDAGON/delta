package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/storage"
	"github.com/ferriskleier/delta/web"
)

// SetupRequest is the unconfigured-machine operation selected in the browser.
// Key whitespace is deliberately accepted here as well as in storage so the
// REST seam has the same behavior as the CLI and the UI.
type SetupRequest struct {
	Door      string
	Path      string
	Key       string
	Confirmed bool
}

// SetupDone is the data shown on the final setup screen. Create and open use
// different subsets of the fields, but keeping one response shape makes the
// browser transition independent of the selected door.
type SetupDone struct {
	Door         string `json:"door"`
	DatabasePath string `json:"database_path"`
	ConfigPath   string `json:"config_path"`
	APIToken     string `json:"api_token"`
	EntryCount   int    `json:"entry_count"`
	FirstDate    string `json:"first_date,omitempty"`
	LastDate     string `json:"last_date,omitempty"`
}

// SetupCompletion contains the authenticated handler that replaces setup
// mode after a successful create/open operation.
type SetupCompletion struct {
	Done    SetupDone
	Handler http.Handler
}

// SetupFinalizer persists the configuration and returns the normal serving
// handler. It is injected by the CLI so the setup handler remains usable at
// the HTTP seam without knowing how a long-running process owns its Store.
type SetupFinalizer func(context.Context, SetupRequest) (SetupCompletion, error)

type setupState struct {
	mu         sync.RWMutex
	finishMu   sync.Mutex
	configured bool
	finishing  bool
	pendingKey string
	normal     http.Handler
	finalize   SetupFinalizer
	frontend   frontend
	info       setupInfo
}

type setupInfo struct {
	Mode                string `json:"mode"`
	DefaultDatabasePath string `json:"default_database_path"`
	ICloudDatabasePath  string `json:"icloud_database_path"`
	ConfigPath          string `json:"config_path"`
}

type setupPayload struct {
	Path         string `json:"path"`
	DatabasePath string `json:"database_path"`
	Key          string `json:"key"`
	Confirmed    bool   `json:"confirmed"`
	Regenerate   bool   `json:"regenerate"`
}

// NewSetupHandler serves the unauthenticated first-run API and the embedded
// frontend until a setup operation succeeds. Once configured, every request
// is delegated to the authenticated handler supplied by the finalizer;
// setup endpoints cannot be reached again in that process.
func NewSetupHandler(finalize SetupFinalizer, options ...HandlerOption) http.Handler {
	configOptions := handlerConfig{frontendFS: nil}
	for _, option := range options {
		if option != nil {
			option(&configOptions)
		}
	}
	if configOptions.frontendFS == nil {
		// Match NewHandler's default without making HandlerOption's internal
		// representation part of the setup API.
		configOptions.frontendFS = web.Files
	}

	defaultPath, _ := config.DefaultDatabasePath()
	configPath, _ := config.Path()
	state := &setupState{
		finalize: finalize,
		frontend: newFrontend(configOptions.frontendFS),
		info: setupInfo{
			Mode:                "setup",
			DefaultDatabasePath: defaultPath,
			ICloudDatabasePath:  iCloudDatabasePath(),
			ConfigPath:          configPath,
		},
	}
	return state
}

// ServeHTTP implements the state transition without replacing the net/http
// server's Handler field (which would race with in-flight requests).
func (s *setupState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !validHost(r.Host) {
		http.Error(w, "invalid host", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	configured, finishing, normal := s.configured, s.finishing, s.normal
	s.mu.RUnlock()
	if configured && !finishing {
		normal.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
		s.serveSetupAPI(w, r)
		return
	}
	// Setup mode intentionally has no bearer token yet.
	s.frontend.serveHTTP(w, r, "")
}

func (s *setupState) serveSetupAPI(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/setup":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
			return
		}
		writeJSON(w, http.StatusOK, s.info)
	case "/api/setup/key":
		s.handleSetupKey(w, r)
	case "/api/setup/create":
		s.handleSetupCreate(w, r)
	case "/api/setup/open":
		s.handleSetupOpen(w, r)
	default:
		writeError(w, http.StatusNotFound, apperror.New(apperror.CodeNotFound, "not found"))
	}
}

func (s *setupState) handleSetupKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	payload, err := decodeSetupPayload(r)
	if err != nil {
		writeSetupError(w, err)
		return
	}
	path, err := setupPath(payload)
	if err != nil {
		writeSetupError(w, err)
		return
	}
	if err := EnsureCreatePath(path); err != nil {
		writeSetupError(w, err)
		return
	}
	s.handleSetupKeyPayload(w, path, payload.Regenerate)
}

func (s *setupState) handleSetupCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	payload, err := decodeSetupPayload(r)
	if err != nil {
		writeSetupError(w, err)
		return
	}
	path, err := setupPath(payload)
	if err != nil {
		writeSetupError(w, err)
		return
	}
	s.mu.RLock()
	pendingKey := s.pendingKey
	s.mu.RUnlock()
	key := storage.NormalizeKey(payload.Key)
	if pendingKey == "" || key != pendingKey {
		writeSetupError(w, apperror.New(apperror.CodeSetupRequired, "request the generated key before creating the diary"))
		return
	}
	if !payload.Confirmed {
		writeSetupError(w, apperror.New(apperror.CodeInvalidSetup, "confirm that you saved the encryption key before creating the diary"))
		return
	}
	if err := EnsureCreatePath(path); err != nil {
		writeSetupError(w, err)
		return
	}
	s.finish(w, r, SetupRequest{Door: "create", Path: path, Key: key, Confirmed: true})
}

func (s *setupState) handleSetupKeyPayload(w http.ResponseWriter, path string, regenerate bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingKey != "" && !regenerate {
		writeSetupError(w, apperror.New(apperror.CodeSetupKeyShown, "a key was already generated; request a new key to replace it"))
		return
	}
	key, err := config.NewKey()
	if err != nil {
		writeSetupError(w, err)
		return
	}
	s.pendingKey = key
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "database_path": path})
}

func (s *setupState) handleSetupOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
		return
	}
	payload, err := decodeSetupPayload(r)
	if err != nil {
		writeSetupError(w, err)
		return
	}
	path, err := setupPath(payload)
	if err != nil {
		writeSetupError(w, err)
		return
	}
	key := storage.NormalizeKey(payload.Key)
	if err := storage.ValidateKey(key); err != nil {
		writeSetupError(w, err)
		return
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeSetupError(w, apperror.Wrap(apperror.CodeDatabaseNotFound, "database file not found", err))
			return
		}
		writeSetupError(w, err)
		return
	}
	s.finish(w, r, SetupRequest{Door: "open", Path: path, Key: key})
}

func (s *setupState) finish(w http.ResponseWriter, r *http.Request, request SetupRequest) {
	if s.finalize == nil {
		writeSetupError(w, apperror.New(apperror.CodeInternalError, "setup is not initialized"))
		return
	}
	s.finishMu.Lock()
	defer s.finishMu.Unlock()

	s.mu.Lock()
	if s.configured {
		s.mu.Unlock()
		writeSetupError(w, apperror.New(apperror.CodeInvalidSetup, "another setup request completed first"))
		return
	}
	s.finishing = true
	s.mu.Unlock()

	completion, err := s.finalize(r.Context(), request)
	if err != nil {
		s.mu.Lock()
		s.finishing = false
		s.mu.Unlock()
		writeSetupError(w, err)
		return
	}
	if completion.Handler == nil {
		s.mu.Lock()
		s.finishing = false
		s.mu.Unlock()
		writeSetupError(w, apperror.New(apperror.CodeInternalError, "setup did not produce a serving handler"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.configured {
		s.finishing = false
		writeSetupError(w, apperror.New(apperror.CodeInvalidSetup, "another setup request completed first"))
		return
	}
	s.configured = true
	s.normal = completion.Handler
	s.pendingKey = ""
	s.finishing = false
	writeJSON(w, http.StatusOK, completion.Done)
}

func decodeSetupPayload(r *http.Request) (setupPayload, error) {
	if r.Body == nil {
		return setupPayload{}, apperror.New(apperror.CodeInvalidSetup, "setup request must be JSON")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return setupPayload{}, apperror.Wrap(apperror.CodeInvalidSetup, "invalid setup JSON", err)
	}
	if strings.TrimSpace(string(body)) == "" {
		return setupPayload{}, apperror.New(apperror.CodeInvalidSetup, "setup request must be JSON")
	}
	var payload setupPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return setupPayload{}, apperror.Wrap(apperror.CodeInvalidSetup, "invalid setup JSON", err)
	}
	if payload.Path == "" {
		payload.Path = payload.DatabasePath
	}
	return payload, nil
}

func setupPath(payload setupPayload) (string, error) {
	path := strings.TrimSpace(payload.Path)
	if path == "" {
		return "", apperror.New(apperror.CodeInvalidSetup, "database path is required")
	}
	path, err := config.ExpandPath(path)
	if err != nil {
		return "", apperror.Wrap(apperror.CodeInvalidSetup, "invalid database path", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", apperror.Wrap(apperror.CodeInvalidSetup, "invalid database path", err)
	}
	return path, nil
}

func EnsureCreatePath(path string) error {
	if _, err := os.Stat(path); err == nil {
		return apperror.New(apperror.CodeDatabaseExists, "a database already exists at this path; choose open existing diary")
	} else if !errors.Is(err, os.ErrNotExist) {
		return apperror.Wrap(apperror.CodeInvalidSetup, "cannot check database path", err)
	}
	return nil
}

func writeSetupError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch apperror.Code(err) {
	case apperror.CodeSetupKeyShown, apperror.CodeDatabaseExists:
		status = http.StatusConflict
	case apperror.CodeDatabaseNotFound:
		status = http.StatusNotFound
	case apperror.CodeInternalError:
		status = http.StatusInternalServerError
	}
	writeError(w, status, err)
}

func iCloudDatabasePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", "Library", "Mobile Documents", "com~apple~CloudDocs", "delta", "delta.db")
	}
	return filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "delta", "delta.db")
}
