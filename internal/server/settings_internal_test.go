package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/internal/storage"
)

func TestConcurrentRegeneratesLeaveLiveAndPersistedTokensAligned(t *testing.T) {
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	databasePath := filepath.Join(t.TempDir(), "diary.db")
	key := strings.Repeat("a1", storage.KeyBytes)
	store, err := storage.Open(context.Background(), databasePath, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value, err := config.New(databasePath, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(value); err != nil {
		t.Fatal(err)
	}
	auth := &authState{token: value.APIToken}
	state := newSettingsState(service.New(store), value.APIToken, &value, auth)

	type result struct {
		token string
		err   error
	}
	results := make(chan result, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/settings/token/regenerate", nil)
			state.handleRegenerate(recorder, request)
			var response tokenRegenerateResponse
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				results <- result{err: err}
				return
			}
			results <- result{token: response.Token}
		}()
	}
	group.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.token == "" {
			t.Fatal("regenerate response did not contain a token")
		}
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if auth.current() != loaded.APIToken {
		t.Fatalf("live token = %q, persisted token = %q", auth.current(), loaded.APIToken)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	request.Header.Set("Authorization", "Bearer "+auth.current())
	if !authorized(request, auth.current()) {
		t.Fatal("final live token did not authenticate")
	}
}
