// Package server exposes the authenticated REST boundary.
package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/web"
)

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type okEnvelope struct {
	OK bool `json:"ok"`
}

type HandlerOption func(*handlerConfig)

type handlerConfig struct {
	frontendFS     fs.FS
	settingsConfig *config.Config
}

// WithFrontendFS replaces the embedded frontend filesystem. It is intended
// for tests and alternate server packaging.
func WithFrontendFS(files fs.FS) HandlerOption {
	return func(config *handlerConfig) {
		config.frontendFS = files
	}
}

// WithSettingsConfig supplies the serving configuration to Settings. The
// option is used by the real server and keeps tests independent of a user's
// ambient config path.
func WithSettingsConfig(value config.Config) HandlerOption {
	return func(config *handlerConfig) {
		config.settingsConfig = &value
	}
}

type frontend struct {
	files      fs.FS
	fileServer http.Handler
	err        error
}

func newFrontend(files fs.FS) frontend {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		return frontend{err: err}
	}
	return frontend{
		files:      sub,
		fileServer: http.FileServer(http.FS(sub)),
	}
}

const frontendTokenPlaceholder = `<script>window.__DELTA_TOKEN__ = null;</script>`

func (f frontend) serveHTTP(w http.ResponseWriter, r *http.Request, token string) {
	if f.err != nil {
		http.Error(w, "frontend is unavailable", http.StatusInternalServerError)
		return
	}

	requested := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if requested != "" && requested != "." && requested != "index.html" {
		if _, err := fs.Stat(f.files, requested); err == nil {
			f.fileServer.ServeHTTP(w, r)
			return
		}
	}

	if _, err := fs.Stat(f.files, "index.html"); err != nil {
		http.Error(w, "frontend is not built; run `cd web && npm run build`", http.StatusNotFound)
		return
	}
	index, err := fs.ReadFile(f.files, "index.html")
	if err != nil {
		http.Error(w, "frontend index is unavailable", http.StatusInternalServerError)
		return
	}
	if token != "" {
		index = injectFrontendToken(index, token)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
}

func injectFrontendToken(index []byte, token string) []byte {
	encoded, _ := json.Marshal(token)
	script := []byte(`<script>window.__DELTA_TOKEN__ = ` + string(encoded) + `;</script>`)
	if bytes.Contains(index, []byte(frontendTokenPlaceholder)) {
		return bytes.Replace(index, []byte(frontendTokenPlaceholder), script, 1)
	}
	if head := bytes.Index(index, []byte("</head>")); head >= 0 {
		result := make([]byte, 0, len(index)+len(script))
		result = append(result, index[:head]...)
		result = append(result, script...)
		result = append(result, index[head:]...)
		return result
	}
	return append(append([]byte(nil), index...), script...)
}

// authState owns the live bearer token so regeneration can revoke previous
// clients without replacing the HTTP handler.
type authState struct {
	mu    sync.RWMutex
	token string
}

func (a *authState) current() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.token
}

func (a *authState) replace(token string) {
	a.mu.Lock()
	a.token = token
	a.mu.Unlock()
}

// NewHandler returns the HTTP handler for one serving DELTA instance. Every
// /api request is authenticated, including unknown routes, so callers never
// get route information without the machine token.
func NewHandler(svc *service.Service, token string, options ...HandlerOption) http.Handler {
	config := handlerConfig{frontendFS: web.Files}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	frontend := newFrontend(config.frontendFS)
	auth := &authState{token: token}
	settings := newSettingsState(svc, token, config.settingsConfig, auth)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
			return
		}
		if svc == nil || svc.Store == nil {
			writeError(w, http.StatusInternalServerError, apperror.New(apperror.CodeInternalError, "server is not initialized"))
			return
		}
		writeJSON(w, http.StatusOK, okEnvelope{OK: true})
	})
	registerEntryRoutes(mux, svc)
	registerHabitRoutes(mux, svc)
	registerGridRoutes(mux, svc)
	registerStatsRoutes(mux, svc)
	registerSearchRoutes(mux, svc)
	registerSettingsRoutes(mux, settings)
	mux.HandleFunc("/api/backup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
			return
		}
		backup, err := svc.CreateBackup(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, backup)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, apperror.New(apperror.CodeNotFound, "not found"))
	})
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, apperror.New(apperror.CodeNotFound, "not found"))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validHost(r.Host) {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api") {
			if !authorized(r, auth.current()) {
				writeError(w, http.StatusUnauthorized, apperror.New(apperror.CodeUnauthorized, "unauthorized"))
				return
			}
			mux.ServeHTTP(w, r)
			return
		}
		frontend.serveHTTP(w, r, auth.current())
	})
}

func validHost(host string) bool {
	if strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "[::1]" {
		return true
	}
	if strings.HasPrefix(host, "[") {
		name, port, err := net.SplitHostPort(host)
		return err == nil && name == "::1" && validPort(port)
	}
	name, port, err := net.SplitHostPort(host)
	return err == nil && (strings.EqualFold(name, "localhost") || name == "127.0.0.1") && validPort(port)
}

func validPort(port string) bool {
	value, err := strconv.Atoi(port)
	return err == nil && value >= 0 && value <= 65535
}

func authorized(r *http.Request, token string) bool {
	parts := strings.Fields(r.Header.Get("Authorization"))
	return len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && token != "" && subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) == 1
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: apperror.Code(err), Message: apperror.Message(err)}})
}

func writeServiceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch apperror.Code(err) {
	case apperror.CodeWrongKey, apperror.CodeInvalidDate, apperror.CodeInvalidEntry, apperror.CodeInvalidHabit, apperror.CodeHabitNotActive, apperror.CodeInvalidGrid, apperror.CodeInvalidStats, apperror.CodeInvalidSetup:
		status = http.StatusBadRequest
	case apperror.CodeEntryNotFound, apperror.CodeHabitNotFound, apperror.CodeNotFound:
		status = http.StatusNotFound
	case apperror.CodeMethodNotAllowed:
		status = http.StatusMethodNotAllowed
	}
	writeError(w, status, err)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
