package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/config"
	"github.com/ferriskleier/delta/internal/server"
	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/internal/storage"
)

type setupHarness struct {
	server *httptest.Server
	store  *storage.Store
}

func newSetupHarness(t *testing.T) *setupHarness {
	t.Helper()
	t.Setenv(config.ConfigEnv, filepath.Join(t.TempDir(), "config.toml"))
	var store *storage.Store
	finalize := func(ctx context.Context, request server.SetupRequest) (server.SetupCompletion, error) {
		opened, err := storage.Open(ctx, request.Path, request.Key)
		if err != nil {
			return server.SetupCompletion{}, err
		}
		if err := storage.MigrateStore(ctx, opened); err != nil {
			_ = opened.Close()
			return server.SetupCompletion{}, err
		}
		c, err := config.New(request.Path, request.Key)
		if err != nil {
			_ = opened.Close()
			return server.SetupCompletion{}, err
		}
		c.APIAddress = "http://127.0.0.1:9999"
		if err := config.Save(c); err != nil {
			_ = opened.Close()
			return server.SetupCompletion{}, err
		}
		entries, err := service.New(opened).ListEntries(ctx, "", "")
		if err != nil {
			_ = opened.Close()
			return server.SetupCompletion{}, err
		}
		done := server.SetupDone{
			Door:         request.Door,
			DatabasePath: c.DatabasePath,
			ConfigPath:   configPath(t),
			APIToken:     c.APIToken,
			EntryCount:   len(entries),
		}
		if len(entries) > 0 {
			done.FirstDate = entries[0].Date
			done.LastDate = entries[len(entries)-1].Date
		}
		store = opened
		return server.SetupCompletion{Done: done, Handler: server.NewHandler(service.New(opened), c.APIToken)}, nil
	}
	ts := httptest.NewServer(server.NewSetupHandler(finalize))
	h := &setupHarness{server: ts}
	t.Cleanup(func() {
		ts.Close()
		if store != nil {
			_ = store.Close()
		}
	})
	return h
}

func TestSetupCreateShowsKeyOnceGatesCompletionAndRemovesSetupRoutes(t *testing.T) {
	h := newSetupHarness(t)
	path := filepath.Join(t.TempDir(), "created", "diary.db")

	status := setupRequest(t, h, http.MethodGet, "/api/setup", nil)
	if status.StatusCode != http.StatusOK {
		t.Fatalf("setup status = %d, want 200", status.StatusCode)
	}
	status.Body.Close()

	keyResponse := setupJSON(t, h, http.MethodPost, "/api/setup/key", map[string]any{"path": path})
	var keyPayload struct {
		Key string `json:"key"`
	}
	decodeSetup(t, keyResponse, http.StatusOK, &keyPayload)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(keyPayload.Key) {
		t.Fatalf("generated key = %q, want 64 hex characters", keyPayload.Key)
	}
	secondKey := setupJSON(t, h, http.MethodPost, "/api/setup/key", map[string]any{"path": path})
	assertErrorCode(t, secondKey, http.StatusConflict, apperror.CodeSetupKeyShown)

	unconfirmed := setupJSON(t, h, http.MethodPost, "/api/setup/create", map[string]any{
		"path": path, "key": keyPayload.Key, "confirmed": false,
	})
	assertErrorCode(t, unconfirmed, http.StatusBadRequest, apperror.CodeInvalidSetup)
	mismatched := setupJSON(t, h, http.MethodPost, "/api/setup/create", map[string]any{
		"path": path, "key": strings.Repeat("e", 64), "confirmed": true,
	})
	assertErrorCode(t, mismatched, http.StatusBadRequest, apperror.CodeSetupRequired)

	doneResponse := setupJSON(t, h, http.MethodPost, "/api/setup/create", map[string]any{
		"path": path, "key": keyPayload.Key, "confirmed": true,
	})
	var done server.SetupDone
	decodeSetup(t, doneResponse, http.StatusOK, &done)
	if done.Door != "create" || done.DatabasePath != path || done.ConfigPath == "" || done.APIToken == "" {
		t.Fatalf("create done = %#v", done)
	}
	if _, err := storage.Open(context.Background(), path, keyPayload.Key); err != nil {
		t.Fatalf("created database cannot be reopened: %v", err)
	}

	setupAfter := setupRequest(t, h, http.MethodGet, "/api/setup", nil)
	assertErrorCode(t, setupAfter, http.StatusUnauthorized, apperror.CodeUnauthorized)
	apiAfter := setupRequest(t, h, http.MethodGet, "/api/health", nil)
	assertErrorCode(t, apiAfter, http.StatusUnauthorized, apperror.CodeUnauthorized)
	apiWithToken := setupRequestWithToken(t, h, http.MethodGet, "/api/health", done.APIToken, nil)
	if apiWithToken.StatusCode != http.StatusOK {
		t.Fatalf("configured health = %d, want 200", apiWithToken.StatusCode)
	}
	apiWithToken.Body.Close()
}

func TestSetupOpenToleratesWhitespaceAndReturnsWrongKeyInline(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "existing.db")
	key := strings.Repeat("cd", storage.KeyBytes)
	store, err := storage.Open(context.Background(), databasePath, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Migrate(context.Background(), store.DB); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := service.New(store).UpsertEntry(context.Background(), "2026-08-01", service.EntryPatch{Text: service.OptionalString{Set: true, Value: "existing diary"}}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	h := newSetupHarness(t)
	wrong := setupJSON(t, h, http.MethodPost, "/api/setup/open", map[string]any{"path": databasePath, "key": strings.Repeat("e", 64)})
	assertErrorCode(t, wrong, http.StatusBadRequest, apperror.CodeWrongKey)

	pasted := strings.Join([]string{key[:16], key[16:32], key[32:48], key[48:]}, " \n")
	opened := setupJSON(t, h, http.MethodPost, "/api/setup/open", map[string]any{"path": databasePath, "key": pasted})
	var done server.SetupDone
	decodeSetup(t, opened, http.StatusOK, &done)
	if done.Door != "open" || done.EntryCount != 1 || done.FirstDate != "2026-08-01" || done.LastDate != "2026-08-01" || done.APIToken == "" {
		t.Fatalf("open done = %#v", done)
	}
}

func TestSetupOpenMissingPathIsNotReportedAsWrongKey(t *testing.T) {
	h := newSetupHarness(t)
	missing := setupJSON(t, h, http.MethodPost, "/api/setup/open", map[string]any{
		"path": filepath.Join(t.TempDir(), "missing.db"), "key": strings.Repeat("e", 64),
	})
	assertErrorCode(t, missing, http.StatusNotFound, apperror.CodeDatabaseNotFound)
}

func TestSetupKeyCanBeRegeneratedAfterReload(t *testing.T) {
	h := newSetupHarness(t)
	path := filepath.Join(t.TempDir(), "reloaded", "diary.db")
	firstResponse := setupJSON(t, h, http.MethodPost, "/api/setup/key", map[string]any{"path": path})
	var first struct {
		Key string `json:"key"`
	}
	decodeSetup(t, firstResponse, http.StatusOK, &first)

	reloaded := setupJSON(t, h, http.MethodPost, "/api/setup/key", map[string]any{"path": path})
	assertErrorCode(t, reloaded, http.StatusConflict, apperror.CodeSetupKeyShown)
	regeneratedResponse := setupJSON(t, h, http.MethodPost, "/api/setup/key", map[string]any{"path": path, "regenerate": true})
	var regenerated struct {
		Key string `json:"key"`
	}
	decodeSetup(t, regeneratedResponse, http.StatusOK, &regenerated)
	if regenerated.Key == first.Key || regenerated.Key == "" {
		t.Fatalf("regenerated key = %q, first key = %q", regenerated.Key, first.Key)
	}

	doneResponse := setupJSON(t, h, http.MethodPost, "/api/setup/create", map[string]any{
		"path": path, "key": regenerated.Key, "confirmed": true,
	})
	decodeSetup(t, doneResponse, http.StatusOK, &server.SetupDone{})
}

func TestSetupCreateThenOpenOnSecondMachine(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "shared", "diary.db")
	createHarness := newSetupHarness(t)
	keyResponse := setupJSON(t, createHarness, http.MethodPost, "/api/setup/key", map[string]any{"path": databasePath})
	var keyPayload struct {
		Key string `json:"key"`
	}
	decodeSetup(t, keyResponse, http.StatusOK, &keyPayload)
	createdResponse := setupJSON(t, createHarness, http.MethodPost, "/api/setup/create", map[string]any{
		"path": databasePath, "key": keyPayload.Key, "confirmed": true,
	})
	var created server.SetupDone
	decodeSetup(t, createdResponse, http.StatusOK, &created)

	for _, date := range []string{"2026-07-01", "2026-08-01"} {
		body := bytes.NewBufferString(`{"text":"shared diary"}`)
		response := setupRequestWithToken(t, createHarness, http.MethodPut, "/api/entries/"+date, created.APIToken, body)
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Fatalf("create-machine entry %s status = %d, body = %s", date, response.StatusCode, body)
		}
		response.Body.Close()
	}

	openHarness := newSetupHarness(t)
	openedResponse := setupJSON(t, openHarness, http.MethodPost, "/api/setup/open", map[string]any{
		"path": databasePath, "key": keyPayload.Key,
	})
	var opened server.SetupDone
	decodeSetup(t, openedResponse, http.StatusOK, &opened)
	if opened.Door != "open" || opened.EntryCount != 2 || opened.FirstDate != "2026-07-01" || opened.LastDate != "2026-08-01" {
		t.Fatalf("second-machine open done = %#v", opened)
	}
	if opened.APIToken == "" || opened.APIToken == created.APIToken {
		t.Fatalf("second-machine token = %q, create-machine token = %q", opened.APIToken, created.APIToken)
	}
}

func TestSetupFinalizesOnlyOnce(t *testing.T) {
	createPath := filepath.Join(t.TempDir(), "created.db")
	openPath := filepath.Join(t.TempDir(), "existing.db")
	key := strings.Repeat("ab", storage.KeyBytes)
	seed, err := storage.Open(context.Background(), openPath, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.MigrateStore(context.Background(), seed); err != nil {
		_ = seed.Close()
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var finalizeCalls int
	var stores []*storage.Store
	firstFinalizerStarted := make(chan struct{})
	releaseFirstFinalizer := make(chan struct{})
	var firstFinalizer sync.Once
	finalize := func(ctx context.Context, request server.SetupRequest) (server.SetupCompletion, error) {
		firstFinalizer.Do(func() { close(firstFinalizerStarted) })
		<-releaseFirstFinalizer
		time.Sleep(100 * time.Millisecond)
		opened, err := storage.Open(ctx, request.Path, request.Key)
		if err != nil {
			return server.SetupCompletion{}, err
		}
		mu.Lock()
		finalizeCalls++
		stores = append(stores, opened)
		mu.Unlock()
		return server.SetupCompletion{
			Done: server.SetupDone{Door: request.Door},
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "normal handler", http.StatusInternalServerError)
			}),
		}, nil
	}
	ts := httptest.NewServer(server.NewSetupHandler(finalize))
	t.Cleanup(func() {
		ts.Close()
		for _, store := range stores {
			_ = store.Close()
		}
	})

	keyResponse := setupJSON(t, &setupHarness{server: ts}, http.MethodPost, "/api/setup/key", map[string]any{"path": createPath})
	var keyPayload struct {
		Key string `json:"key"`
	}
	decodeSetup(t, keyResponse, http.StatusOK, &keyPayload)

	results := make(chan struct {
		response *http.Response
		err      error
	}, 2)
	post := func(path string, payload map[string]any) {
		go func() {
			body, marshalErr := json.Marshal(payload)
			if marshalErr != nil {
				results <- struct {
					response *http.Response
					err      error
				}{err: marshalErr}
				return
			}
			request, requestErr := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(body))
			if requestErr != nil {
				results <- struct {
					response *http.Response
					err      error
				}{err: requestErr}
				return
			}
			request.Header.Set("Content-Type", "application/json")
			response, requestErr := ts.Client().Do(request)
			results <- struct {
				response *http.Response
				err      error
			}{response: response, err: requestErr}
		}()
	}
	post("/api/setup/create", map[string]any{"path": createPath, "key": keyPayload.Key, "confirmed": true})
	select {
	case <-firstFinalizerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first setup finalizer did not start")
	}
	post("/api/setup/open", map[string]any{"path": openPath, "key": key})
	close(releaseFirstFinalizer)

	statusCounts := map[int]int{}
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		statusCounts[result.response.StatusCode]++
		if result.response.StatusCode == http.StatusBadRequest {
			assertErrorCode(t, result.response, http.StatusBadRequest, apperror.CodeInvalidSetup)
		} else {
			decodeSetup(t, result.response, http.StatusOK, &server.SetupDone{})
		}
	}
	if statusCounts[http.StatusOK] != 1 || statusCounts[http.StatusBadRequest] != 1 {
		t.Fatalf("concurrent finalize statuses = %#v, want one 200 and one 400", statusCounts)
	}
	mu.Lock()
	defer mu.Unlock()
	if finalizeCalls != 1 || len(stores) != 1 {
		t.Fatalf("finalize calls = %d, stores = %d; want one of each", finalizeCalls, len(stores))
	}
}

func setupJSON(t *testing.T, h *setupHarness, method, path string, value map[string]any) *http.Response {
	t.Helper()
	var body io.Reader
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, h.server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func setupRequest(t *testing.T, h *setupHarness, method, path string, body io.Reader) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, h.server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func setupRequestWithToken(t *testing.T, h *setupHarness, method, path, token string, body io.Reader) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, h.server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := h.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeSetup(t *testing.T, response *http.Response, status int, target any) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != status {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("setup status = %d, want %d, body = %s", response.StatusCode, status, body)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func configPath(t *testing.T) string {
	t.Helper()
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
