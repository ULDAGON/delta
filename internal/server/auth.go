package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/internal/storage"
)

const (
	sessionCookieName = "delta_session"
	sessionIdleTTL    = 30 * 24 * time.Hour
	sessionLimit      = 32

	loginFailureAllowance = 5
	loginFailureDelay     = 30 * time.Second
)

// sessionState owns the browser login sessions. They live in memory only: a
// restart logs every browser out, which suits an instance whose diary may
// just have moved or re-keyed.
type sessionState struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
}

func newSessionState() *sessionState {
	return &sessionState{lastSeen: make(map[string]time.Time)}
}

func (s *sessionState) create(now time.Time) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	s.lastSeen[id] = now
	return id, nil
}

// valid refreshes a live session's idle clock, so a browser in regular use
// never has to log in again until the instance restarts.
func (s *sessionState) valid(id string, now time.Time) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen, ok := s.lastSeen[id]
	if !ok {
		return false
	}
	if now.Sub(seen) > sessionIdleTTL {
		delete(s.lastSeen, id)
		return false
	}
	s.lastSeen[id] = now
	return true
}

func (s *sessionState) drop(id string) {
	s.mu.Lock()
	delete(s.lastSeen, id)
	s.mu.Unlock()
}

// pruneLocked expires idle sessions and, at the cap, evicts the stalest one
// so the store cannot grow without bound.
func (s *sessionState) pruneLocked(now time.Time) {
	for id, seen := range s.lastSeen {
		if now.Sub(seen) > sessionIdleTTL {
			delete(s.lastSeen, id)
		}
	}
	for len(s.lastSeen) >= sessionLimit {
		oldestID, oldest := "", now
		for id, seen := range s.lastSeen {
			if !seen.After(oldest) {
				oldestID, oldest = id, seen
			}
		}
		if oldestID == "" {
			return
		}
		delete(s.lastSeen, oldestID)
	}
}

func sessionIDFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// loginState throttles failed logins. The budget is global, not per-peer: a
// single-user server has no honest reason for parallel guessing, and a
// per-address budget would only hand an attacker more attempts.
type loginState struct {
	mu           sync.Mutex
	failures     int
	blockedUntil time.Time
}

func (l *loginState) allowed(now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Before(l.blockedUntil) {
		return false, l.blockedUntil.Sub(now)
	}
	return true, 0
}

func (l *loginState) fail(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures++
	if l.failures >= loginFailureAllowance {
		l.blockedUntil = now.Add(loginFailureDelay)
	}
}

func (l *loginState) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures = 0
	l.blockedUntil = time.Time{}
}

// serveSessionRoute dispatches the three endpoints that sit outside the
// bearer/session gate: login must be reachable logged-out, and session and
// logout answer harmlessly either way. It reports whether it handled r.
func serveSessionRoute(w http.ResponseWriter, r *http.Request, svc *service.Service, sessions *sessionState, login *loginState, auth *authState) bool {
	switch r.URL.Path {
	case "/api/login":
		if r.Method != http.MethodPost {
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
			return true
		}
		handleLogin(w, r, svc, sessions, login)
		return true
	case "/api/session":
		if r.Method != http.MethodGet {
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
			return true
		}
		authenticated := authorized(r, auth.current()) || sessions.valid(sessionIDFromRequest(r), time.Now())
		writeJSON(w, http.StatusOK, struct {
			Authenticated bool `json:"authenticated"`
		}{Authenticated: authenticated})
		return true
	case "/api/logout":
		if r.Method != http.MethodPost {
			writeServiceError(w, apperror.New(apperror.CodeMethodNotAllowed, "method not allowed"))
			return true
		}
		if id := sessionIDFromRequest(r); id != "" {
			sessions.drop(id)
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		writeJSON(w, http.StatusOK, okEnvelope{OK: true})
		return true
	}
	return false
}

// handleLogin exchanges the diary's encryption key for a session cookie. The
// key is the one credential that already exists, and the server holds it, so
// verification is a constant-time compare against the open store's key.
func handleLogin(w http.ResponseWriter, r *http.Request, svc *service.Service, sessions *sessionState, login *loginState) {
	if svc == nil || svc.Store == nil {
		writeServiceError(w, apperror.New(apperror.CodeInternalError, "server is not initialized"))
		return
	}
	now := time.Now()
	if ok, wait := login.allowed(now); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeServiceError(w, apperror.New(apperror.CodeRateLimited, "too many failed logins — try again shortly"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeServiceError(w, apperror.Wrap(apperror.CodeInvalidSetup, "invalid login JSON", err))
		return
	}
	var payload struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeServiceError(w, apperror.Wrap(apperror.CodeInvalidSetup, "login requires a JSON body with a key", err))
		return
	}
	key := storage.NormalizeKey(payload.Key)
	if subtle.ConstantTimeCompare([]byte(key), []byte(svc.Store.Key)) != 1 {
		login.fail(now)
		writeError(w, http.StatusUnauthorized, apperror.New(apperror.CodeUnauthorized, "wrong key"))
		return
	}
	login.reset()
	id, err := sessions.create(now)
	if err != nil {
		writeServiceError(w, apperror.Wrap(apperror.CodeInternalError, "create session", err))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionIdleTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, okEnvelope{OK: true})
}
