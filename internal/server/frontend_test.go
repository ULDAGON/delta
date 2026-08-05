package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/server"
	"github.com/ferriskleier/delta/internal/service"
)

func TestServeFrontendUsesAssetsAndSPAFallback(t *testing.T) {
	files := fstest.MapFS{
		"dist/index.html":    &fstest.MapFile{Data: []byte("<title>DELTA</title>")},
		"dist/assets/app.js": &fstest.MapFile{Data: []byte("console.log('delta')")},
	}
	handler := server.NewHandler(nil, "test-token", server.WithFrontendFS(files))

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
		wantJSON   bool
	}{
		{name: "root", path: "/", wantStatus: http.StatusOK, wantBody: "<title>DELTA</title>"},
		{name: "placeholder route", path: "/stats", wantStatus: http.StatusOK, wantBody: "<title>DELTA</title>"},
		{name: "asset", path: "/assets/app.js", wantStatus: http.StatusOK, wantBody: "console.log('delta')"},
		{name: "api route", path: "/api/unknown", wantStatus: http.StatusNotFound, wantJSON: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+tt.path, nil)
			if strings.HasPrefix(tt.path, "/api/") {
				request.Header.Set("Authorization", "Bearer test-token")
			}
			handler.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if tt.wantJSON {
				var envelope struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error.Code != "not_found" || envelope.Error.Message != "not found" {
					t.Fatalf("error envelope = %#v", envelope.Error)
				}
				return
			}
			if !strings.Contains(recorder.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want it to contain %q", recorder.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestBrowserTokenAuthenticatesGridAndRejectsRebindingHost(t *testing.T) {
	h := api.NewTestHarness(t)
	files := fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: []byte(`<!doctype html><html><head><script>window.__DELTA_TOKEN__ = null;</script></head><body></body></html>`)},
	}
	ts := httptest.NewServer(server.NewHandler(service.New(h.Store), h.Token, server.WithFrontendFS(files)))
	defer ts.Close()

	page, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	pageBody, err := io.ReadAll(page.Body)
	page.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if page.StatusCode != http.StatusOK {
		t.Fatalf("page status = %d, want 200", page.StatusCode)
	}
	tokenMatch := regexp.MustCompile(`window\.__DELTA_TOKEN__ = "([^"]+)"`).FindSubmatch(pageBody)
	if len(tokenMatch) != 2 || string(tokenMatch[1]) != h.Token {
		t.Fatalf("injected page token = %q, want server token", tokenMatch)
	}

	withoutToken, err := ts.Client().Get(ts.URL + "/api/grid")
	if err != nil {
		t.Fatal(err)
	}
	withoutBody, _ := io.ReadAll(withoutToken.Body)
	withoutToken.Body.Close()
	if withoutToken.StatusCode != http.StatusUnauthorized || strings.Contains(string(withoutBody), h.Token) {
		t.Fatalf("without token = status %d body %q, want unauthorized without token disclosure", withoutToken.StatusCode, withoutBody)
	}

	withTokenRequest, err := http.NewRequest(http.MethodGet, ts.URL+"/api/grid", nil)
	if err != nil {
		t.Fatal(err)
	}
	withTokenRequest.Header.Set("Authorization", "Bearer "+string(tokenMatch[1]))
	withToken, err := ts.Client().Do(withTokenRequest)
	if err != nil {
		t.Fatal(err)
	}
	withBody, _ := io.ReadAll(withToken.Body)
	withToken.Body.Close()
	if withToken.StatusCode != http.StatusOK || !strings.Contains(string(withBody), `"days"`) {
		t.Fatalf("with injected token = status %d body prefix %q, want grid data", withToken.StatusCode, withBody)
	}

	regenerateRequest, err := http.NewRequest(http.MethodPost, ts.URL+"/api/settings/token/regenerate", nil)
	if err != nil {
		t.Fatal(err)
	}
	regenerateRequest.Header.Set("Authorization", "Bearer "+h.Token)
	regenerated, err := ts.Client().Do(regenerateRequest)
	if err != nil {
		t.Fatal(err)
	}
	var regeneratedBody struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(regenerated.Body).Decode(&regeneratedBody); err != nil {
		regenerated.Body.Close()
		t.Fatal(err)
	}
	regenerated.Body.Close()
	if regenerated.StatusCode != http.StatusOK || regeneratedBody.Token == "" {
		t.Fatalf("regenerate response = %d %#v", regenerated.StatusCode, regeneratedBody)
	}
	oldRequest, err := http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	oldRequest.Header.Set("Authorization", "Bearer "+h.Token)
	oldAfterRegeneration, err := ts.Client().Do(oldRequest)
	if err != nil {
		t.Fatal(err)
	}
	oldAfterRegeneration.Body.Close()
	if oldAfterRegeneration.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after regeneration = %d, want 401", oldAfterRegeneration.StatusCode)
	}
	pageAfterRegeneration, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	pageAfterBody, err := io.ReadAll(pageAfterRegeneration.Body)
	pageAfterRegeneration.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	newTokenMatch := regexp.MustCompile(`window\.__DELTA_TOKEN__ = "([^"]+)"`).FindSubmatch(pageAfterBody)
	if len(newTokenMatch) != 2 || string(newTokenMatch[1]) != regeneratedBody.Token {
		t.Fatalf("injected token after regeneration = %q, want %q", newTokenMatch, regeneratedBody.Token)
	}

	badHostRequest, err := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	badHostRequest.Host = "evil.example"
	badHost, err := ts.Client().Do(badHostRequest)
	if err != nil {
		t.Fatal(err)
	}
	badHost.Body.Close()
	if badHost.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad host status = %d, want 400", badHost.StatusCode)
	}
}

func TestEmbeddedFrontendContainsDistPlaceholder(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "web", "index.html"))
	if err != nil {
		t.Fatalf("read frontend source: %v", err)
	}
	const placeholder = `<script>window.__DELTA_TOKEN__ = null;</script>`
	if !bytes.Contains(source, []byte(placeholder)) {
		t.Fatal("web/index.html is missing the frontend token placeholder")
	}

	files := fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: source},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	server.NewHandler(nil, "test-token", server.WithFrontendFS(files)).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("frontend status = %d, want 200", recorder.Code)
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`window.__DELTA_TOKEN__ = "test-token"`)) {
		t.Fatalf("frontend token placeholder was not replaced: %s", recorder.Body.String())
	}
}
