package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ferriskleier/delta/internal/api"
	"github.com/ferriskleier/delta/internal/server"
	"github.com/ferriskleier/delta/internal/service"
)

func loginRequest(t *testing.T, h *api.Harness, key string) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]string{"key": key})
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.Server.Client().Post(h.Server.URL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func sessionCookie(t *testing.T, response *http.Response) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == "delta_session" {
			return cookie
		}
	}
	t.Fatal("response carries no delta_session cookie")
	return nil
}

func cookieRequest(t *testing.T, h *api.Harness, method, path string, body []byte, cookie *http.Cookie, origin string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, h.Server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := h.Server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestLoginIssuesASessionTheBrowserCanUse(t *testing.T) {
	h := api.NewTestHarness(t)

	probe := cookieRequest(t, h, http.MethodGet, "/api/session", nil, nil, "")
	var status struct {
		Authenticated bool `json:"authenticated"`
	}
	decodeJSON(t, probe, &status)
	if status.Authenticated {
		t.Fatal("fresh browser reports authenticated = true")
	}

	// Keys are pasted from password managers, so grouping whitespace must
	// normalize away exactly as it does at setup.
	spaced := strings.Join([]string{h.Key[:16], h.Key[16:32], h.Key[32:]}, " ")
	login := loginRequest(t, h, spaced)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", login.StatusCode)
	}
	cookie := sessionCookie(t, login)
	login.Body.Close()
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %#v, want HttpOnly SameSite=Strict", cookie)
	}

	grid := cookieRequest(t, h, http.MethodGet, "/api/grid", nil, cookie, "")
	if grid.StatusCode != http.StatusOK {
		t.Fatalf("cookie grid status = %d, want 200", grid.StatusCode)
	}
	grid.Body.Close()

	authed := cookieRequest(t, h, http.MethodGet, "/api/session", nil, cookie, "")
	decodeJSON(t, authed, &status)
	if !status.Authenticated {
		t.Fatal("session probe with cookie reports authenticated = false")
	}

	logout := cookieRequest(t, h, http.MethodPost, "/api/logout", []byte("{}"), cookie, "")
	if logout.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", logout.StatusCode)
	}
	logout.Body.Close()
	afterLogout := cookieRequest(t, h, http.MethodGet, "/api/grid", nil, cookie, "")
	if afterLogout.StatusCode != http.StatusUnauthorized {
		t.Fatalf("grid after logout = %d, want 401", afterLogout.StatusCode)
	}
	afterLogout.Body.Close()
}

func TestLoginRejectsWrongKeysAndThrottlesAfterFive(t *testing.T) {
	h := api.NewTestHarness(t)
	wrong := strings.Repeat("b2", 32)
	for attempt := 1; attempt <= 5; attempt++ {
		response := loginRequest(t, h, wrong)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt, response.StatusCode)
		}
		response.Body.Close()
	}
	blocked := loginRequest(t, h, wrong)
	if blocked.StatusCode != http.StatusTooManyRequests || blocked.Header.Get("Retry-After") == "" {
		t.Fatalf("sixth attempt = %d (Retry-After %q), want 429 with Retry-After", blocked.StatusCode, blocked.Header.Get("Retry-After"))
	}
	blocked.Body.Close()
	// The block holds even for the right key, so guessing cannot be raced
	// against a legitimate login.
	correct := loginRequest(t, h, h.Key)
	if correct.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("correct key while blocked = %d, want 429", correct.StatusCode)
	}
	correct.Body.Close()
}

func TestCookieMutationsRequireASameOriginRequest(t *testing.T) {
	h := api.NewTestHarness(t)
	login := loginRequest(t, h, h.Key)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", login.StatusCode)
	}
	cookie := sessionCookie(t, login)
	login.Body.Close()
	entry := []byte(`{"text":"origin fence"}`)

	crossSite := cookieRequest(t, h, http.MethodPut, "/api/entries/2026-01-05", entry, cookie, "http://evil.example")
	if crossSite.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin cookie write = %d, want 403", crossSite.StatusCode)
	}
	crossSite.Body.Close()

	sameOrigin := cookieRequest(t, h, http.MethodPut, "/api/entries/2026-01-05", entry, cookie, h.Server.URL)
	if sameOrigin.StatusCode != http.StatusOK {
		t.Fatalf("same-origin cookie write = %d, want 200", sameOrigin.StatusCode)
	}
	sameOrigin.Body.Close()

	// Bearer clients are exempt: a cross-site page cannot attach an
	// Authorization header, so the fence would only break CLI/MCP callers.
	request, err := http.NewRequest(http.MethodPut, h.Server.URL+"/api/entries/2026-01-06", bytes.NewReader(entry))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+h.Token)
	request.Header.Set("Origin", "http://evil.example")
	bearer, err := h.Server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if bearer.StatusCode != http.StatusOK {
		t.Fatalf("bearer write with foreign origin = %d, want 200", bearer.StatusCode)
	}
	bearer.Body.Close()
}

func TestPagesForOtherMachinesCarryNoToken(t *testing.T) {
	files := fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: []byte(`<!doctype html><html><head><script>window.__DELTA_TOKEN__ = null;</script></head><body></body></html>`)},
	}
	handler := server.NewHandler(nil, "secret-token", server.WithFrontendFS(files), server.WithVersion("v9.9.9"))

	lan := httptest.NewRecorder()
	lanRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	lanRequest.RemoteAddr = "192.168.1.50:40000"
	handler.ServeHTTP(lan, lanRequest)
	if lan.Code != http.StatusOK {
		t.Fatalf("LAN page status = %d, want 200", lan.Code)
	}
	lanBody := lan.Body.String()
	if strings.Contains(lanBody, "secret-token") || strings.Contains(lanBody, "__DELTA_TOKEN__") {
		t.Fatalf("LAN page leaks the token surface: %s", lanBody)
	}
	if !strings.Contains(lanBody, `window.__DELTA_VERSION__ = "v9.9.9"`) {
		t.Fatalf("LAN page is missing the version: %s", lanBody)
	}
	csp := lan.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "'nonce-") || !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("LAN page CSP = %q, want a nonce'd same-origin policy", csp)
	}
	nonce := strings.TrimSuffix(strings.SplitAfter(csp, "'nonce-")[1], "'")
	nonce = nonce[:strings.Index(nonce, "'")]
	if !strings.Contains(lanBody, `nonce="`+nonce+`"`) {
		t.Fatalf("injected script does not carry the CSP nonce %q: %s", nonce, lanBody)
	}

	loopback := httptest.NewRecorder()
	loopbackRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	loopbackRequest.RemoteAddr = "127.0.0.1:40001"
	handler.ServeHTTP(loopback, loopbackRequest)
	if !strings.Contains(loopback.Body.String(), `window.__DELTA_TOKEN__ = "secret-token"`) {
		t.Fatalf("loopback page is missing the token: %s", loopback.Body.String())
	}
}

func TestSettingsAdminSurfacesAreLoopbackOnly(t *testing.T) {
	h := api.NewTestHarness(t)
	handler := server.NewHandler(service.New(h.Store), h.Token, server.WithFrontendFS(fstest.MapFS{"dist/index.html": &fstest.MapFile{}}))

	remote := func(method, path string, body []byte) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, "http://127.0.0.1"+path, bytes.NewReader(body))
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		request.Header.Set("Authorization", "Bearer "+h.Token)
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	tests := []struct {
		name   string
		method string
		path   string
		body   []byte
		want   int
	}{
		{name: "reveal key", method: http.MethodGet, path: "/api/settings?reveal=key", want: http.StatusForbidden},
		{name: "reveal token", method: http.MethodGet, path: "/api/settings?reveal=token", want: http.StatusForbidden},
		{name: "patch", method: http.MethodPatch, path: "/api/settings", body: []byte(`{"lan":true}`), want: http.StatusForbidden},
		{name: "regenerate", method: http.MethodPost, path: "/api/settings/token/regenerate", want: http.StatusForbidden},
		{name: "plain settings read stays open", method: http.MethodGet, path: "/api/settings", want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// httptest.NewRequest's default peer is a public TEST-NET address,
			// i.e. not this machine.
			recorder := remote(tt.method, tt.path, tt.body)
			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, tt.want, recorder.Body.String())
			}
			if tt.want == http.StatusForbidden && !strings.Contains(recorder.Body.String(), "loopback_only") {
				t.Fatalf("body = %s, want a loopback_only error", recorder.Body.String())
			}
		})
	}
}
