package server_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/server"
	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/internal/storage"
)

func TestSettingsMasksSecretsUntilExplicitReveal(t *testing.T) {
	h := api.NewTestHarness(t)
	response := settingsRequest(t, h, http.MethodGet, "/api/settings", nil, h.Token)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("settings status = %d, want 200", response.StatusCode)
	}
	var info struct {
		DatabasePath string `json:"database_path"`
		Key          string `json:"key"`
		Token        string `json:"token"`
	}
	decodeJSON(t, response, &info)
	if info.DatabasePath != h.DBPath {
		t.Fatalf("database path = %q, want %q", info.DatabasePath, h.DBPath)
	}
	if info.Key == h.Key || info.Key == "" || strings.Contains(info.Key, h.Key[:8]) {
		t.Fatalf("settings key = %q, want masked value", info.Key)
	}
	if info.Token == h.Token || info.Token == "" || strings.Contains(info.Token, h.Token[:8]) {
		t.Fatalf("settings token = %q, want masked value", info.Token)
	}

	response = settingsRequest(t, h, http.MethodGet, "/api/settings?reveal=key", nil, h.Token)
	var revealed struct {
		Key string `json:"key"`
	}
	decodeJSON(t, response, &revealed)
	if revealed.Key != h.Key {
		t.Fatalf("revealed key = %q, want %q", revealed.Key, h.Key)
	}
	response = settingsRequest(t, h, http.MethodGet, "/api/settings?reveal=token", nil, h.Token)
	var revealedToken struct {
		Token string `json:"token"`
	}
	decodeJSON(t, response, &revealedToken)
	if revealedToken.Token != h.Token {
		t.Fatalf("revealed token = %q, want %q", revealedToken.Token, h.Token)
	}
}

func TestSettingsExposesOnlyCanonicalRoutesAndFields(t *testing.T) {
	h := api.NewTestHarness(t)
	response := settingsRequest(t, h, http.MethodGet, "/api/settings", nil, h.Token)
	var fields map[string]json.RawMessage
	decodeJSON(t, response, &fields)
	for _, alias := range []string{"encryption_key", "api_token", "listening_address"} {
		if _, ok := fields[alias]; ok {
			t.Fatalf("settings response contains removed alias %q", alias)
		}
	}

	response = settingsRequest(t, h, http.MethodGet, "/api/settings?reveal=all", nil, h.Token)
	var masked struct {
		Key   string `json:"key"`
		Token string `json:"token"`
	}
	decodeJSON(t, response, &masked)
	if masked.Key == h.Key || masked.Token == h.Token {
		t.Fatalf("reveal=all exposed a secret: %#v", masked)
	}

	for _, request := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/settings/key"},
		{method: http.MethodPatch, path: "/api/settings/key"},
		{method: http.MethodPost, path: "/api/settings/key"},
		{method: http.MethodGet, path: "/api/settings/token"},
		{method: http.MethodPost, path: "/api/settings/token"},
	} {
		response = settingsRequest(t, h, request.method, request.path, nil, h.Token)
		if response.StatusCode != http.StatusNotFound {
			response.Body.Close()
			t.Fatalf("%s %s status = %d, want 404", request.method, request.path, response.StatusCode)
		}
		response.Body.Close()
	}

	response = settingsRequest(t, h, http.MethodPatch, "/api/settings", []byte(`{"key":123}`), h.Token)
	assertErrorCode(t, response, http.StatusBadRequest, "invalid_setup")
}

func TestSettingsKeyChangeOnlyUpdatesConfigAndCanBeRestored(t *testing.T) {
	h := api.NewTestHarness(t)
	before, err := os.ReadFile(h.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	newKey := strings.Repeat("b", storage.KeyHexLength)
	response := settingsRequest(t, h, http.MethodPatch, "/api/settings", []byte(`{"key":"`+newKey+`"}`), h.Token)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("key update status = %d", response.StatusCode)
	}
	response.Body.Close()
	after, err := os.ReadFile(h.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("database bytes changed while changing the configured key")
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Key != newKey {
		t.Fatalf("configured key = %q, want %q", loaded.Key, newKey)
	}

	response = settingsRequest(t, h, http.MethodPatch, "/api/settings", []byte(`{"key":"`+h.Key+`"}`), h.Token)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restoring key status = %d", response.StatusCode)
	}
	response.Body.Close()
	loaded, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Key != h.Key {
		t.Fatalf("restored configured key = %q, want %q", loaded.Key, h.Key)
	}
	if _, err := storage.Open(t.Context(), h.DBPath, loaded.Key); err != nil {
		t.Fatalf("database did not recover with the right key: %v", err)
	}
}

func TestSettingsDatabasePathCopiesTheDiaryAndLeavesTheOriginal(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-02", map[string]any{"text": "moved diary"})
	target := filepath.Join(t.TempDir(), "moved", "diary.db")
	response := settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"database_path": target}), h.Token)
	var result struct {
		Settings struct {
			DatabasePath string `json:"database_path"`
			BackupsPath  string `json:"backups_path"`
		} `json:"settings"`
		RestartRequired bool `json:"restart_required"`
		DatabaseMoved   bool `json:"database_moved"`
		DatabaseAdopted bool `json:"database_adopted"`
	}
	decodeJSON(t, response, &result)
	if result.Settings.DatabasePath != target || !result.RestartRequired {
		t.Fatalf("database path update result = %#v, want %q and a restart", result, target)
	}
	if !result.DatabaseMoved || result.DatabaseAdopted {
		t.Fatalf("fresh target result = %#v, want a copy rather than an adoption", result)
	}
	if want := storage.BackupDirectory(target); result.Settings.BackupsPath != want {
		t.Fatalf("backups path = %q, want %q", result.Settings.BackupsPath, want)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DatabasePath != target {
		t.Fatalf("configured database path = %q, want %q", loaded.DatabasePath, target)
	}
	if _, err := os.Stat(h.DBPath); err != nil {
		t.Fatalf("original database was not left behind: %v", err)
	}
	copied, err := storage.Open(t.Context(), target, h.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = copied.Close() })
	var text string
	if err := copied.DB.QueryRowContext(t.Context(), `SELECT text FROM entries WHERE date = '2026-08-02'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "moved diary" {
		t.Fatalf("copied entry text = %q, want %q", text, "moved diary")
	}

	response = settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"database_path": target}), h.Token)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unchanged database path status = %d, want 200", response.StatusCode)
	}
	response.Body.Close()

	occupied := filepath.Join(t.TempDir(), "occupied.db")
	if err := os.WriteFile(occupied, []byte("not a diary"), 0o600); err != nil {
		t.Fatal(err)
	}
	response = settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"database_path": occupied}), h.Token)
	assertErrorCode(t, response, http.StatusBadRequest, "delta_wrong_key")
	loaded, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DatabasePath != target {
		t.Fatalf("refused update changed the configured database path to %q", loaded.DatabasePath)
	}
}

func TestSettingsDatabasePathChangeStopsDiaryWritesUntilRestart(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-02", map[string]any{"text": "written before the move"})
	target := filepath.Join(t.TempDir(), "moved", "diary.db")
	response := settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"database_path": target}), h.Token)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("database path update status = %d, want 200", response.StatusCode)
	}
	response.Body.Close()

	// The running Store still has the old file open, so every diary write is
	// refused: accepting one would drop it at the next start.
	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{name: "entry", method: http.MethodPut, path: "/api/entries/2026-08-03", body: []byte(`{"text":"would be lost"}`)},
		{name: "entry delete", method: http.MethodDelete, path: "/api/entries/2026-08-02"},
		{name: "habit", method: http.MethodPost, path: "/api/habits", body: []byte(`{"name":"Read"}`)},
		{name: "check-off", method: http.MethodPost, path: "/api/entries/2026-08-02/checkoffs/1"},
		{name: "colors", method: http.MethodPut, path: "/api/settings/colors", body: []byte(`{"accent":"#ff0000"}`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			blocked := settingsRequest(t, h, tt.method, tt.path, tt.body, h.Token)
			assertError(t, blocked, http.StatusServiceUnavailable, "restart_required", target)
		})
	}

	reads := settingsRequest(t, h, http.MethodGet, "/api/entries", nil, h.Token)
	var entries []map[string]any
	decodeJSON(t, reads, &entries)
	if len(entries) != 1 || entries[0]["text"] != "written before the move" {
		t.Fatalf("entries after the move = %#v, want the diary this instance still serves", entries)
	}

	// Config-only changes and a manual backup of the open file stay available so
	// the move can be corrected or completed without a restart.
	lan := settingsRequest(t, h, http.MethodPatch, "/api/settings", []byte(`{"lan":true}`), h.Token)
	if lan.StatusCode != http.StatusOK {
		t.Fatalf("lan update after the move status = %d, want 200", lan.StatusCode)
	}
	lan.Body.Close()
	backup := settingsRequest(t, h, http.MethodPost, "/api/backup", nil, h.Token)
	if backup.StatusCode != http.StatusOK {
		t.Fatalf("manual backup after the move status = %d, want 200", backup.StatusCode)
	}
	backup.Body.Close()
	regenerate := settingsRequest(t, h, http.MethodPost, "/api/settings/token/regenerate", nil, h.Token)
	if regenerate.StatusCode != http.StatusOK {
		t.Fatalf("token regeneration after the move status = %d, want 200", regenerate.StatusCode)
	}
	regenerate.Body.Close()
}

func TestSettingsRejectsAKeyAndDatabasePathChangeInOneRequest(t *testing.T) {
	h := api.NewTestHarness(t)
	target := filepath.Join(t.TempDir(), "moved", "diary.db")
	response := settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{
		"key": strings.Repeat("b", storage.KeyHexLength), "database_path": target,
	}), h.Token)
	assertError(t, response, http.StatusBadRequest, "invalid_setup", "separate requests")

	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Key != h.Key || loaded.DatabasePath != h.DBPath {
		t.Fatalf("refused request changed the config: %#v", loaded)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("stat %s = %v, want the refused request to copy nothing", target, err)
	}
	putJSON(t, h, "2026-08-02", map[string]any{"text": "still writable"})
}

func TestSettingsAdoptsAnExistingDiaryWithoutCopyingOverIt(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-02", map[string]any{"text": "original diary"})
	target := filepath.Join(t.TempDir(), "existing", "diary.db")
	newDiaryFile(t, target, h.Key, "2026-08-04", "diary already at the new path")

	response := settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"database_path": target}), h.Token)
	var result struct {
		Settings struct {
			DatabasePath string `json:"database_path"`
		} `json:"settings"`
		RestartRequired bool `json:"restart_required"`
		DatabaseMoved   bool `json:"database_moved"`
		DatabaseAdopted bool `json:"database_adopted"`
	}
	decodeJSON(t, response, &result)
	if result.Settings.DatabasePath != target || !result.RestartRequired {
		t.Fatalf("adoption result = %#v, want %q and a restart", result, target)
	}
	if !result.DatabaseAdopted || result.DatabaseMoved {
		t.Fatalf("adoption result = %#v, want an adoption rather than a copy", result)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DatabasePath != target {
		t.Fatalf("configured database path = %q, want %q", loaded.DatabasePath, target)
	}
	if text, found := diaryText(t, target, h.Key, "2026-08-04"); !found || text != "diary already at the new path" {
		t.Fatalf("adopted diary entry = %q (found %v), want it untouched", text, found)
	}
	if _, found := diaryText(t, target, h.Key, "2026-08-02"); found {
		t.Fatal("adopted diary received a copy of the live diary, want it adopted as it is")
	}
	if text, found := diaryText(t, h.DBPath, h.Key, "2026-08-02"); !found || text != "original diary" {
		t.Fatalf("old diary entry = %q (found %v), want it left behind untouched", text, found)
	}
}

func TestSettingsRefusesADiaryTheConfiguredKeyCannotOpen(t *testing.T) {
	h := api.NewTestHarness(t)
	foreignKey := strings.Repeat("b2", storage.KeyBytes)
	foreign := filepath.Join(t.TempDir(), "foreign", "diary.db")
	newDiaryFile(t, foreign, foreignKey, "2026-08-05", "another machine's diary")

	response := settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"database_path": foreign}), h.Token)
	assertErrorCode(t, response, http.StatusBadRequest, "delta_wrong_key")
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DatabasePath != h.DBPath {
		t.Fatalf("refused adoption changed the configured database path to %q", loaded.DatabasePath)
	}
	if text, found := diaryText(t, foreign, foreignKey, "2026-08-05"); !found || text != "another machine's diary" {
		t.Fatalf("refused diary entry = %q (found %v), want it untouched", text, found)
	}
	// A refused change must not leave the instance write-blocked.
	putJSON(t, h, "2026-08-06", map[string]any{"text": "still writable"})
}

func TestSettingsRefusesADiaryFromANewerDELTA(t *testing.T) {
	h := api.NewTestHarness(t)
	newer := filepath.Join(t.TempDir(), "newer", "diary.db")
	newDiaryFile(t, newer, h.Key, "2026-08-04", "written by a newer DELTA")
	store, err := storage.Open(t.Context(), newer, h.Key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(t.Context(), "PRAGMA user_version = "+strconv.Itoa(storage.CurrentVersion()+1)); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	response := settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"database_path": newer}), h.Token)
	assertError(t, response, http.StatusBadRequest, "upgrade_required", "newer DELTA")
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DatabasePath != h.DBPath {
		t.Fatalf("refused adoption changed the configured database path to %q", loaded.DatabasePath)
	}
	putJSON(t, h, "2026-08-02", map[string]any{"text": "still writable"})
}

func TestSettingsRefusesADatabasePathThatIsNotAFile(t *testing.T) {
	h := api.NewTestHarness(t)
	directory := filepath.Join(t.TempDir(), "not-a-diary")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	response := settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"database_path": directory}), h.Token)
	assertErrorCode(t, response, http.StatusBadRequest, "invalid_setup")
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DatabasePath != h.DBPath {
		t.Fatalf("refused update changed the configured database path to %q", loaded.DatabasePath)
	}
	putJSON(t, h, "2026-08-02", map[string]any{"text": "still writable"})
}

func TestSettingsDatabasePathCanBeRevertedToTheOriginalDiary(t *testing.T) {
	h := api.NewTestHarness(t)
	putJSON(t, h, "2026-08-02", map[string]any{"text": "original diary"})
	target := filepath.Join(t.TempDir(), "moved", "diary.db")

	moved := settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"database_path": target}), h.Token)
	var result struct {
		DatabaseMoved   bool `json:"database_moved"`
		DatabaseAdopted bool `json:"database_adopted"`
	}
	decodeJSON(t, moved, &result)
	if !result.DatabaseMoved {
		t.Fatalf("move to a fresh path = %#v, want a copy", result)
	}
	reverted := settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"database_path": h.DBPath}), h.Token)
	decodeJSON(t, reverted, &result)
	if !result.DatabaseAdopted || result.DatabaseMoved {
		t.Fatalf("revert result = %#v, want the original diary adopted", result)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DatabasePath != h.DBPath {
		t.Fatalf("configured database path = %q, want the original %q", loaded.DatabasePath, h.DBPath)
	}
}

// newDiaryFile creates a second real DELTA database holding one entry, so
// adoption can be tested against a file this process does not keep open.
func newDiaryFile(t *testing.T, path, key, date, text string) {
	t.Helper()
	store, err := storage.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(t.Context(), store.DB); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := service.New(store).UpsertEntry(t.Context(), date, service.EntryPatch{
		Text: service.OptionalString{Set: true, Value: text},
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func diaryText(t *testing.T, path, key, date string) (string, bool) {
	t.Helper()
	store, err := storage.Open(t.Context(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var text string
	err = store.DB.QueryRowContext(t.Context(), `SELECT text FROM entries WHERE date = ?`, date).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return text, true
}

func TestSettingsBackupsPathSetsAndResetsTheSnapshotDirectory(t *testing.T) {
	h := api.NewTestHarness(t)
	directory := filepath.Join(t.TempDir(), "elsewhere", "backups")
	response := settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"backups_path": directory}), h.Token)
	var result struct {
		Settings struct {
			BackupsPath           string `json:"backups_path"`
			BackupsPathConfigured string `json:"backups_path_configured"`
		} `json:"settings"`
		RestartRequired bool `json:"restart_required"`
	}
	decodeJSON(t, response, &result)
	if result.Settings.BackupsPath != directory || result.Settings.BackupsPathConfigured != directory {
		t.Fatalf("backups path = %#v, want the configured %q", result.Settings, directory)
	}
	// The directory swap is applied live, so a backups-only change must not
	// tell the UI a restart is needed.
	if result.RestartRequired {
		t.Fatal("backups-only patch reported restart_required = true")
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("backups path %q is not a directory", directory)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BackupsPath != directory {
		t.Fatalf("configured backups path = %q, want %q", loaded.BackupsPath, directory)
	}

	response = settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"backups_path": ""}), h.Token)
	decodeJSON(t, response, &result)
	if want := storage.BackupDirectory(h.DBPath); result.Settings.BackupsPath != want {
		t.Fatalf("reset backups path = %q, want derived %q", result.Settings.BackupsPath, want)
	}
	// The UI seeds its editor from the configured value, so a derived default
	// must not come back as something a save would pin into the config.
	if result.Settings.BackupsPathConfigured != "" {
		t.Fatalf("reset backups_path_configured = %q, want empty", result.Settings.BackupsPathConfigured)
	}
	loaded, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BackupsPath != "" {
		t.Fatalf("configured backups path = %q, want empty after reset", loaded.BackupsPath)
	}
}

func TestSettingsBackupsPathAppliesToTheNextSnapshotWithoutRestart(t *testing.T) {
	h := api.NewTestHarness(t)
	directory := filepath.Join(t.TempDir(), "elsewhere", "backups")
	response := settingsRequest(t, h, http.MethodPatch, "/api/settings", settingsBody(t, map[string]string{"backups_path": directory}), h.Token)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("backups path update status = %d, want 200", response.StatusCode)
	}
	response.Body.Close()

	var backup struct {
		Filename string `json:"filename"`
		Path     string `json:"path"`
	}
	decodeJSON(t, settingsRequest(t, h, http.MethodPost, "/api/backup", nil, h.Token), &backup)
	if filepath.Dir(backup.Path) != directory {
		t.Fatalf("manual backup path = %q, want a file in %q", backup.Path, directory)
	}
	if _, err := os.Stat(backup.Path); err != nil {
		t.Fatalf("stat manual backup: %v", err)
	}
	if _, err := os.Stat(storage.BackupDirectory(h.DBPath)); !os.IsNotExist(err) {
		t.Fatalf("derived backups directory stat = %v, want nothing written there", err)
	}

	var after struct {
		BackupsPath     string `json:"backups_path"`
		LastBackup      string `json:"last_backup"`
		LastBackupError string `json:"last_backup_error"`
	}
	decodeJSON(t, settingsRequest(t, h, http.MethodGet, "/api/settings", nil, h.Token), &after)
	if after.BackupsPath != directory || after.LastBackup == "" || after.LastBackupError != "" {
		t.Fatalf("settings after the backup = %#v, want a snapshot reported in %q", after, directory)
	}
}

func TestSettingsLanTogglePersistsAndAlwaysPreviewsLANURLs(t *testing.T) {
	h := api.NewTestHarness(t)
	response := settingsRequest(t, h, http.MethodGet, "/api/settings", nil, h.Token)
	var before struct {
		LAN     bool     `json:"lan"`
		LANURLs []string `json:"lan_urls"`
	}
	decodeJSON(t, response, &before)
	if before.LAN {
		t.Fatal("fresh settings lan = true, want false")
	}
	if before.LANURLs == nil {
		t.Fatal("settings lan_urls = null, want an array the UI can preview")
	}

	for _, value := range []bool{true, false} {
		response = settingsRequest(t, h, http.MethodPatch, "/api/settings", []byte(`{"lan":`+strconv.FormatBool(value)+`}`), h.Token)
		var result struct {
			Settings struct {
				LAN       bool     `json:"lan"`
				LANActive bool     `json:"lan_active"`
				LANURLs   []string `json:"lan_urls"`
			} `json:"settings"`
			RestartRequired bool `json:"restart_required"`
		}
		decodeJSON(t, response, &result)
		if result.Settings.LAN != value || !result.RestartRequired {
			t.Fatalf("lan update result = %#v, want lan %v and a restart", result, value)
		}
		if result.Settings.LANActive {
			t.Fatalf("lan update lan_active = true on a loopback-bound handler: %#v", result)
		}
		if result.Settings.LANURLs == nil {
			t.Fatal("lan update lan_urls = null, want an array")
		}
		for _, url := range result.Settings.LANURLs {
			if !strings.HasPrefix(url, "http://") || !strings.HasSuffix(url, ":7331") {
				t.Fatalf("lan URL = %q, want an http URL on the configured port", url)
			}
		}
		loaded, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Lan != value {
			t.Fatalf("configured LAN = %v, want %v", loaded.Lan, value)
		}
		if loaded.DatabasePath != h.DBPath || loaded.Key != h.Key {
			t.Fatalf("lan update changed protected config fields: %#v", loaded)
		}
	}
}

func TestSettingsSeparatesConfiguredLANFromTheServedLANState(t *testing.T) {
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	key := strings.Repeat("a1", storage.KeyBytes)
	store, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "diary.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := storage.Migrate(t.Context(), store.DB); err != nil {
		t.Fatal(err)
	}
	c, err := config.New(store.Path, key)
	if err != nil {
		t.Fatal(err)
	}
	c.Lan = true
	if err := config.Save(c); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name       string
		options    []server.HandlerOption
		wantActive bool
	}{
		// A process started before the flag was turned on still serves
		// loopback only, so lan_active stays false until the next restart.
		{name: "configured but not serving", options: []server.HandlerOption{server.WithSettingsConfig(c)}},
		{name: "configured and serving", options: []server.HandlerOption{server.WithSettingsConfig(c), server.WithLANAccess(true)}, wantActive: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(server.NewHandler(service.New(store), c.APIToken, tt.options...))
			t.Cleanup(ts.Close)
			request, err := http.NewRequest(http.MethodGet, ts.URL+"/api/settings", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+c.APIToken)
			response, err := ts.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			var info struct {
				LAN       bool `json:"lan"`
				LANActive bool `json:"lan_active"`
			}
			decodeJSON(t, response, &info)
			if !info.LAN {
				t.Fatal("settings lan = false, want the configured flag")
			}
			if info.LANActive != tt.wantActive {
				t.Fatalf("settings lan_active = %v, want %v", info.LANActive, tt.wantActive)
			}
		})
	}
}

func TestSettingsPatchRejectsEmptyAndUnknownFields(t *testing.T) {
	h := api.NewTestHarness(t)
	tests := []struct {
		name string
		body string
	}{
		{name: "no fields", body: `{}`},
		{name: "unknown field", body: `{"future_field":"not accepted"}`},
		{name: "empty database path", body: `{"database_path":""}`},
		{name: "non-string backups path", body: `{"backups_path":123}`},
		{name: "non-boolean lan", body: `{"lan":"yes"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := settingsRequest(t, h, http.MethodPatch, "/api/settings", []byte(tt.body), h.Token)
			assertErrorCode(t, response, http.StatusBadRequest, "invalid_setup")
		})
	}
}

func TestSettingsTokenRegenerationInvalidatesOldTokenImmediately(t *testing.T) {
	h := api.NewTestHarness(t)
	response := settingsRequest(t, h, http.MethodPost, "/api/settings/token/regenerate", nil, h.Token)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("regenerate status = %d", response.StatusCode)
	}
	var result struct {
		Token string `json:"token"`
	}
	decodeJSON(t, response, &result)
	if result.Token == "" || result.Token == h.Token {
		t.Fatalf("regenerated token = %q, old token = %q", result.Token, h.Token)
	}
	old := settingsRequest(t, h, http.MethodGet, "/api/settings", nil, h.Token)
	if old.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token status = %d, want 401", old.StatusCode)
	}
	old.Body.Close()
	current := settingsRequest(t, h, http.MethodGet, "/api/settings", nil, result.Token)
	if current.StatusCode != http.StatusOK {
		t.Fatalf("new token status = %d, want 200", current.StatusCode)
	}
	current.Body.Close()
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIToken != result.Token {
		t.Fatalf("configured token = %q, want %q", loaded.APIToken, result.Token)
	}
}

func TestSettingsBackupNowUpdatesLastBackup(t *testing.T) {
	h := api.NewTestHarness(t)
	response := settingsRequest(t, h, http.MethodGet, "/api/settings", nil, h.Token)
	var before struct {
		LastBackup string `json:"last_backup"`
	}
	decodeJSON(t, response, &before)
	response = settingsRequest(t, h, http.MethodPost, "/api/backup", nil, h.Token)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("backup status = %d", response.StatusCode)
	}
	response.Body.Close()
	response = settingsRequest(t, h, http.MethodGet, "/api/settings", nil, h.Token)
	var after struct {
		LastBackup      string `json:"last_backup"`
		BackupsPath     string `json:"backups_path"`
		LastBackupError string `json:"last_backup_error"`
	}
	decodeJSON(t, response, &after)
	if after.LastBackup == "" || after.LastBackup == before.LastBackup {
		t.Fatalf("last backup before=%q after=%q, want updated timestamp", before.LastBackup, after.LastBackup)
	}
	if after.BackupsPath == "" || after.LastBackupError != "" {
		t.Fatalf("backup settings = %#v", after)
	}
}

func settingsBody(t *testing.T, fields map[string]string) []byte {
	t.Helper()
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func settingsRequest(t *testing.T, h *api.Harness, method, path string, body []byte, token string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, h.Server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := h.Server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
