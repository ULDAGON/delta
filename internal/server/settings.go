package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/internal/storage"
)

type settingsState struct {
	svc     *service.Service
	auth    *authState
	restart *restartState
	// lanActive is the LAN state this handler was built with, which stays
	// behind the configured flag until the process restarts.
	lanActive bool

	configMu      sync.RWMutex
	config        config.Config
	configPath    string
	configPathErr error
	regenerateMu  sync.Mutex
	// patchMu serializes settings changes so a database path change that fails
	// cannot undo the write block a concurrent one just installed.
	patchMu sync.Mutex
}

type settingsResponse struct {
	DatabasePath string `json:"database_path"`
	Key          string `json:"key"`
	APIAddress   string `json:"api_address"`
	Token        string `json:"token"`
	BackupsPath  string `json:"backups_path"`
	// BackupsPathConfigured is the raw configured value, empty when snapshots
	// live in the directory derived beside the database. The UI needs it to seed
	// its editor without pinning the derived default into the config.
	BackupsPathConfigured string   `json:"backups_path_configured"`
	LAN                   bool     `json:"lan"`
	LANActive             bool     `json:"lan_active"`
	LANURLs               []string `json:"lan_urls"`
	LastBackup            string   `json:"last_backup,omitempty"`
	LastBackupError       string   `json:"last_backup_error,omitempty"`
}

// settingsPatchResponse tells the UI which of the two database path outcomes
// happened: a copy to a fresh file, or the adoption of a diary already there.
type settingsPatchResponse struct {
	Settings        settingsResponse `json:"settings"`
	RestartRequired bool             `json:"restart_required"`
	DatabaseMoved   bool             `json:"database_moved"`
	DatabaseAdopted bool             `json:"database_adopted"`
}

type tokenRegenerateResponse struct {
	Token    string           `json:"token"`
	Settings settingsResponse `json:"settings"`
}

func newSettingsState(svc *service.Service, token string, supplied *config.Config, auth *authState, restart *restartState, lanActive bool) *settingsState {
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
		restart:       restart,
		lanActive:     lanActive,
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
	if reveal != "" && !isLoopbackPeer(r.RemoteAddr) {
		writeServiceError(w, errLoopbackOnly())
		return
	}
	writeJSON(w, http.StatusOK, s.snapshot(reveal == "key", reveal == "token"))
}

// errLoopbackOnly fences the config-changing surfaces onto the machine
// itself: a leaked LAN session or token must never be able to extract the
// key or repoint the diary.
func errLoopbackOnly() error {
	return apperror.New(apperror.CodeLoopbackOnly, "only available from the machine DELTA runs on")
}

func (s *settingsState) snapshot(revealKey, revealToken bool) settingsResponse {
	s.configMu.RLock()
	value := s.config
	s.configMu.RUnlock()
	result := settingsResponse{
		DatabasePath:          value.DatabasePath,
		Key:                   maskSecret(value.Key),
		APIAddress:            value.APIAddress,
		Token:                 maskSecret(value.APIToken),
		BackupsPath:           storage.ResolveBackupDirectory(value.DatabasePath, value.BackupsPath),
		BackupsPathConfigured: value.BackupsPath,
		LAN:                   value.Lan,
		LANActive:             s.lanActive,
		LANURLs:               LANURLs(addressPort(value.APIAddress)),
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
	if !isLoopbackPeer(r.RemoteAddr) {
		writeServiceError(w, errLoopbackOnly())
		return
	}
	if s.configPathErr != nil {
		writeServiceError(w, apperror.Wrap(apperror.CodeInternalError, "config path unavailable", s.configPathErr))
		return
	}
	if s.svc == nil || s.svc.Store == nil {
		writeServiceError(w, apperror.New(apperror.CodeInternalError, "server is not initialized"))
		return
	}
	fields, err := decodeSettingsObject(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	s.patchMu.Lock()
	defer s.patchMu.Unlock()

	patch, err := parseSettingsPatch(fields, s.currentDatabasePath())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if patch.backupsPath.Set && patch.backupsPath.Value != "" {
		if err := os.MkdirAll(patch.backupsPath.Value, 0o700); err != nil {
			writeServiceError(w, apperror.Wrap(apperror.CodeInvalidSetup, "cannot create the backups directory", err))
			return
		}
	}
	// Diary writes stop before the copy is taken, not after the config is
	// written: the running Store keeps the old file open, so a write accepted in
	// between would be missing from the file the next start opens. The previous
	// state is restored if the change never reaches the config.
	var restartPending bool
	var restartPath string
	if patch.databasePath.Set {
		restartPending, restartPath = s.restart.require(patch.databasePath.Value)
		if err := s.prepareDatabasePath(r.Context(), patch); err != nil {
			s.restart.restore(restartPending, restartPath)
			writeServiceError(w, err)
			return
		}
	}
	updated, err := config.UpdateAt(s.configPath, func(value *config.Config) error {
		if patch.key.Set {
			value.Key = patch.key.Value
		}
		if patch.databasePath.Set {
			value.DatabasePath = patch.databasePath.Value
		}
		if patch.backupsPath.Set {
			value.BackupsPath = patch.backupsPath.Value
		}
		if patch.lan.Set {
			value.Lan = patch.lan.Value
		}
		return nil
	})
	if err != nil {
		if patch.databasePath.Set {
			s.restart.restore(restartPending, restartPath)
		}
		writeServiceError(w, err)
		return
	}
	s.configMu.Lock()
	s.config = updated
	s.configMu.Unlock()
	if patch.backupsPath.Set {
		// Snapshots follow the configured directory immediately. Waiting for a
		// restart would keep writing them where Settings no longer points.
		s.svc.SetBackupsPath(patch.backupsPath.Value)
	}
	writeJSON(w, http.StatusOK, settingsPatchResponse{
		Settings: s.snapshot(false, false),
		// A backups-only change is applied live above; everything else waits for
		// the next start.
		RestartRequired: patch.key.Set || patch.databasePath.Set || patch.lan.Set,
		DatabaseMoved:   patch.databasePath.Set && !patch.adoptDatabase,
		DatabaseAdopted: patch.databasePath.Set && patch.adoptDatabase,
	})
}

// prepareDatabasePath makes the new location openable by the next start: a
// fresh path receives a copy of the live diary, and a path that already holds a
// diary is adopted as it is. The old database file is left in place either way:
// a move that fails halfway must never be the only copy of a diary.
func (s *settingsState) prepareDatabasePath(ctx context.Context, patch settingsPatch) error {
	if patch.adoptDatabase {
		return adoptableDatabase(ctx, patch.databasePath.Value, s.currentKey())
	}
	if err := s.svc.Store.Snapshot(ctx, patch.databasePath.Value); err != nil {
		return apperror.Wrap(apperror.CodeInvalidSetup, "cannot copy the database to the new location", err)
	}
	return nil
}

// adoptableDatabase reports whether an existing file is a diary this machine can
// hand to the next start: the configured key must open it, and its schema must
// not be newer than this binary can migrate, which would brick that start.
func adoptableDatabase(ctx context.Context, path, key string) error {
	store, err := storage.Open(ctx, path, key)
	if err != nil {
		// A path that is not an openable file at all is a setup mistake, not a
		// server fault: report it like every other rejected settings value.
		if apperror.Code(err) == apperror.CodeInternalError {
			return apperror.Wrap(apperror.CodeInvalidSetup, "cannot open a diary at this path", err)
		}
		return err
	}
	defer store.Close()
	var version int
	if err := store.DB.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return apperror.Wrap(apperror.CodeWrongKey, apperror.WrongKeyMessage, err)
	}
	if version > storage.CurrentVersion() {
		return apperror.New(apperror.CodeUpgrade,
			"the diary at this path was created by a newer DELTA; upgrade delta before pointing at it")
	}
	return nil
}

// settingsPatch is the validated subset of settings one PATCH changes. An
// unset field is left exactly as configured.
type settingsPatch struct {
	key          service.OptionalString
	databasePath service.OptionalString
	// adoptDatabase marks a new database path that already holds a file: the
	// diary there is validated and adopted instead of being copied over.
	adoptDatabase bool
	backupsPath   service.OptionalString
	lan           optionalBool
}

// optionalBool separates an omitted lan field from an explicit false.
type optionalBool struct {
	Set   bool
	Value bool
}

func parseSettingsPatch(fields map[string]json.RawMessage, databasePath string) (settingsPatch, error) {
	var patch settingsPatch
	_, changingKey := fields["key"]
	_, changingDatabasePath := fields["database_path"]
	// A key written together with a path change would describe the new file with
	// the new key while the copy or the adopted diary still uses the old one, so
	// the next start could not open the diary the config points at.
	if changingKey && changingDatabasePath {
		return settingsPatch{}, apperror.New(apperror.CodeInvalidSetup, "change the encryption key and the database path in separate requests")
	}
	if raw, ok := fields["key"]; ok {
		value, err := settingsString(raw, "key")
		if err != nil {
			return settingsPatch{}, err
		}
		key := storage.NormalizeKey(value)
		if err := storage.ValidateKey(key); err != nil {
			return settingsPatch{}, err
		}
		patch.key = service.OptionalString{Set: true, Value: key}
	}
	if raw, ok := fields["database_path"]; ok {
		value, err := settingsString(raw, "database_path")
		if err != nil {
			return settingsPatch{}, err
		}
		if strings.TrimSpace(value) == "" {
			return settingsPatch{}, apperror.New(apperror.CodeInvalidSetup, "database path is required")
		}
		target, err := settingsAbsolutePath("database path", value)
		if err != nil {
			return settingsPatch{}, err
		}
		if target != databasePath {
			occupied, err := databaseFileExists(target)
			if err != nil {
				return settingsPatch{}, err
			}
			patch.databasePath = service.OptionalString{Set: true, Value: target}
			patch.adoptDatabase = occupied
		}
	}
	if raw, ok := fields["backups_path"]; ok {
		value, err := settingsString(raw, "backups_path")
		if err != nil {
			return settingsPatch{}, err
		}
		directory := ""
		if strings.TrimSpace(value) != "" {
			if directory, err = settingsAbsolutePath("backups path", value); err != nil {
				return settingsPatch{}, err
			}
		}
		patch.backupsPath = service.OptionalString{Set: true, Value: directory}
	}
	if raw, ok := fields["lan"]; ok {
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return settingsPatch{}, apperror.Wrap(apperror.CodeInvalidSetup, "settings lan must be a boolean", err)
		}
		patch.lan = optionalBool{Set: true, Value: value}
	}
	return patch, nil
}

func addressPort(address string) string {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		return ""
	}
	return parsed.Port()
}

func settingsString(raw json.RawMessage, field string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", apperror.Wrap(apperror.CodeInvalidSetup, "settings "+field+" must be a string", err)
	}
	return value, nil
}

func settingsAbsolutePath(field, value string) (string, error) {
	path, err := config.ExpandPath(strings.TrimSpace(value))
	if err != nil {
		return "", apperror.Wrap(apperror.CodeInvalidSetup, "invalid "+field, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", apperror.Wrap(apperror.CodeInvalidSetup, "invalid "+field, err)
	}
	return path, nil
}

func (s *settingsState) currentDatabasePath() string {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.config.DatabasePath
}

func (s *settingsState) currentKey() string {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.config.Key
}

func (s *settingsState) handleRegenerate(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackPeer(r.RemoteAddr) {
		writeServiceError(w, errLoopbackOnly())
		return
	}
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
		switch field {
		case "key", "database_path", "backups_path", "lan":
		default:
			return nil, apperror.New(apperror.CodeInvalidSetup, "unknown settings field: "+field)
		}
	}
	if len(fields) == 0 {
		return nil, apperror.New(apperror.CodeInvalidSetup, "settings update requires at least one of key, database_path, backups_path, or lan")
	}
	return fields, nil
}
