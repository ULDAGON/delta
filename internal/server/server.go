// Package server exposes the authenticated REST boundary.
package server

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"net/url"
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
	lanAccess      bool
	settingsConfig *config.Config
	version        string
}

// WithFrontendFS replaces the embedded frontend filesystem. It is intended
// for tests and alternate server packaging.
func WithFrontendFS(files fs.FS) HandlerOption {
	return func(config *handlerConfig) {
		config.frontendFS = files
	}
}

// WithLANAccess opens the handler to peers on the local network. Only
// loopback and on-link private or link-local addresses are ever served, and
// only IP literals are accepted as Host names. Pages served to LAN peers
// carry no API token; their browsers log in with the encryption key instead.
func WithLANAccess(enabled bool) HandlerOption {
	return func(config *handlerConfig) {
		config.lanAccess = enabled
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

// WithVersion supplies the binary's version string to the frontend. The
// option is used by the real server; tests that omit it serve the page with
// its version placeholder untouched.
func WithVersion(version string) HandlerOption {
	return func(config *handlerConfig) {
		config.version = version
	}
}

type frontend struct {
	files      fs.FS
	fileServer http.Handler
	version    string
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

const (
	frontendTokenPlaceholder   = `<script>window.__DELTA_TOKEN__ = null;</script>`
	frontendVersionPlaceholder = `<script>window.__DELTA_VERSION__ = null;</script>`
)

func (f frontend) serveHTTP(w http.ResponseWriter, r *http.Request, token string) {
	if f.err != nil {
		http.Error(w, "frontend is unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")

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
	nonce, err := scriptNonce()
	if err != nil {
		http.Error(w, "frontend page is unavailable", http.StatusInternalServerError)
		return
	}
	if token != "" {
		index = injectFrontendToken(index, token, nonce)
	} else {
		// A page for another machine carries no token at all — not even the
		// null placeholder, which CSP would reject as an un-nonced script.
		index = bytes.Replace(index, []byte(frontendTokenPlaceholder), nil, 1)
	}
	if f.version != "" {
		index = injectFrontendVersion(index, f.version, nonce)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy(nonce))
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
}

func scriptNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(raw), nil
}

// contentSecurityPolicy locks the page to same-origin resources. Inline
// styles stay allowed because React writes pixel colours as style attributes;
// the two injected scripts are the only inline scripts and carry the nonce.
func contentSecurityPolicy(nonce string) string {
	return "default-src 'self'; script-src 'self' 'nonce-" + nonce + "'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
}

func injectFrontendToken(index []byte, token, nonce string) []byte {
	encoded, _ := json.Marshal(token)
	script := []byte(`<script nonce="` + nonce + `">window.__DELTA_TOKEN__ = ` + string(encoded) + `;</script>`)
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

func injectFrontendVersion(index []byte, version, nonce string) []byte {
	encoded, _ := json.Marshal(version)
	script := []byte(`<script nonce="` + nonce + `">window.__DELTA_VERSION__ = ` + string(encoded) + `;</script>`)
	if bytes.Contains(index, []byte(frontendVersionPlaceholder)) {
		return bytes.Replace(index, []byte(frontendVersionPlaceholder), script, 1)
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

// restartState marks a serving instance whose diary no longer lives in the
// file its Store has open. The running Store cannot be repointed, so every
// diary write after a database_path change would land in the abandoned file
// and disappear at the next start: writes are refused until DELTA restarts.
type restartState struct {
	mu      sync.RWMutex
	pending bool
	path    string
}

// require marks the instance write-blocked and returns the previous state so a
// path change that fails after this point can restore it.
func (s *restartState) require(path string) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, previousPath := s.pending, s.path
	s.pending, s.path = true, path
	return previous, previousPath
}

func (s *restartState) restore(pending bool, path string) {
	s.mu.Lock()
	s.pending, s.path = pending, path
	s.mu.Unlock()
}

func (s *restartState) required() (bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pending, s.path
}

// blocksDiaryWrite reports whether one request would write diary data. Reads
// stay available, and so do the config-only settings surfaces the user needs to
// correct or complete a move: /api/settings, token regeneration, and a manual
// backup of the file this instance still has open.
func blocksDiaryWrite(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	switch r.URL.Path {
	case "/api/settings", "/api/settings/token/regenerate", "/api/backup":
		return false
	}
	return true
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
	frontend.version = config.version
	auth := &authState{token: token}
	sessions := newSessionState()
	login := &loginState{}
	restart := &restartState{}
	settings := newSettingsState(svc, token, config.settingsConfig, auth, restart, config.lanAccess)

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
	registerColorRoutes(mux, svc)
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
		if config.lanAccess && !localNetworkPeer(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !hostAllowed(r.Host, config.lanAccess) {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api") {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if serveSessionRoute(w, r, svc, sessions, login, auth) {
				return
			}
			viaBearer := authorized(r, auth.current())
			if !viaBearer && !sessions.valid(sessionIDFromRequest(r), time.Now()) {
				writeError(w, http.StatusUnauthorized, apperror.New(apperror.CodeUnauthorized, "unauthorized"))
				return
			}
			// Only cookie requests need the Origin fence: a cross-site page can
			// make a browser send cookies, but never an Authorization header.
			if !viaBearer && !sameOriginRequest(r) {
				writeError(w, http.StatusForbidden, apperror.New(apperror.CodeUnauthorized, "cross-origin request refused"))
				return
			}
			if pending, path := restart.required(); pending && blocksDiaryWrite(r) {
				writeServiceError(w, apperror.New(apperror.CodeRestartRequired,
					"the database moved to "+path+"; restart DELTA before writing"))
				return
			}
			mux.ServeHTTP(w, r)
			return
		}
		// Only pages served to this machine embed the token: a local process
		// could read it from the config file anyway, while a LAN browser has
		// to log in for a session instead.
		token := ""
		if isLoopbackPeer(r.RemoteAddr) {
			token = auth.current()
		}
		frontend.serveHTTP(w, r, token)
	})
}

func hostAllowed(host string, lanAccess bool) bool {
	return validHost(host) || (lanAccess && localNetworkHost(host))
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

// localNetworkHost accepts an IP literal on the local network, with an
// optional port. Only literals qualify: accepting a name would hand any web
// page a DNS-rebinding route to the API.
func localNetworkHost(host string) bool {
	if name, port, err := net.SplitHostPort(host); err == nil && validPort(port) {
		return localNetworkIP(net.ParseIP(name))
	}
	return localNetworkIP(net.ParseIP(strings.Trim(host, "[]")))
}

// localNetworkIP reports whether an address sits on a network this machine is
// directly attached to. Private is not enough on its own: a VPN route makes
// every host on 10/8 look private while reaching it crosses the internet.
func localNetworkIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	if !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range onLinkPrefixes() {
		if prefix != nil && prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// onLinkPrefixes reports the prefixes of the networks this machine shares with
// its neighbours. It is a package variable so tests can pin the set instead of
// depending on the interfaces of the machine they run on.
var onLinkPrefixes = cachedOnLinkPrefixes

const onLinkTTL = 15 * time.Second

// onLinkCache re-reads the interface list on a short TTL so a laptop that
// joins another network converges without restarting the server.
var onLinkCache struct {
	mu       sync.Mutex
	prefixes []*net.IPNet
	read     time.Time
}

func cachedOnLinkPrefixes() []*net.IPNet {
	onLinkCache.mu.Lock()
	defer onLinkCache.mu.Unlock()
	if now := time.Now(); onLinkCache.read.IsZero() || now.Sub(onLinkCache.read) >= onLinkTTL {
		onLinkCache.prefixes = readOnLinkPrefixes()
		onLinkCache.read = now
	}
	return onLinkCache.prefixes
}

// readOnLinkPrefixes collects the prefixes of every interface that carries a
// shared local network. Point-to-point interfaces are skipped: a tunnel's
// routes lead across the internet, not to a neighbour on the same link.
func readOnLinkPrefixes() []*net.IPNet {
	devices, err := net.Interfaces()
	if err != nil {
		return nil
	}
	prefixes := []*net.IPNet{}
	for _, device := range devices {
		if device.Flags&net.FlagUp == 0 || device.Flags&net.FlagLoopback != 0 || device.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		addresses, err := device.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok && network.IP != nil {
				prefixes = append(prefixes, network)
			}
		}
	}
	return prefixes
}

// sameOriginRequest guards cookie-authenticated mutations against CSRF. The
// session cookie is SameSite=Strict, so this is a second fence: a mismatched
// Origin is refused, while an absent one (old browsers, same-origin GETs)
// falls back to the cookie's own SameSite protection.
func sameOriginRequest(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return parsed.Host == r.Host
}

// isLoopbackPeer reports whether the connection comes from this machine
// itself, as opposed to a LAN neighbour.
func isLoopbackPeer(remoteAddr string) bool {
	host := remoteAddr
	if name, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = name
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func localNetworkPeer(remoteAddr string) bool {
	host := remoteAddr
	if name, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = name
	}
	// A link-local peer arrives with an interface zone that net.ParseIP rejects.
	if zone := strings.IndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	return localNetworkIP(net.ParseIP(host))
}

// LANURLs lists the addresses a LAN client can reach this machine on. Only
// private IPv4 addresses of on-link interfaces are advertised, so neither a
// publicly routable nor a tunnelled address is ever offered as a way in.
func LANURLs(port string) []string {
	urls := []string{}
	if port == "" {
		return urls
	}
	for _, network := range onLinkPrefixes() {
		if network == nil {
			continue
		}
		ip := network.IP.To4()
		if ip == nil || ip.IsLoopback() || !ip.IsPrivate() {
			continue
		}
		urls = append(urls, "http://"+net.JoinHostPort(ip.String(), port))
	}
	return urls
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
	case apperror.CodeWrongKey, apperror.CodeInvalidDate, apperror.CodeInvalidEntry, apperror.CodeInvalidHabit, apperror.CodeHabitNotActive, apperror.CodeInvalidGrid, apperror.CodeInvalidStats, apperror.CodeInvalidSetup, apperror.CodeInvalidUIColors, apperror.CodeUpgrade:
		status = http.StatusBadRequest
	case apperror.CodeEntryNotFound, apperror.CodeHabitNotFound, apperror.CodeNotFound:
		status = http.StatusNotFound
	case apperror.CodeMethodNotAllowed:
		status = http.StatusMethodNotAllowed
	case apperror.CodeRestartRequired:
		status = http.StatusServiceUnavailable
	case apperror.CodeLoopbackOnly:
		status = http.StatusForbidden
	case apperror.CodeRateLimited:
		status = http.StatusTooManyRequests
	}
	writeError(w, status, err)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
