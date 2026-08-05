package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/config"
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
