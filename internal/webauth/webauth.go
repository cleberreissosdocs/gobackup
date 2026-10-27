// Package webauth implements the simple login/session mechanism shared by
// gobackup-central and gobackup-agent's web UIs: bcrypt-hashed passwords,
// an in-memory session token carried in an httponly cookie, a per-session
// CSRF token, and brute-force throttling on login attempts.
package webauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/cleber/gobackup/internal/core"
	"github.com/cleber/gobackup/internal/db"
)

const CookieName = "gobackup_session"
const sessionTTL = 24 * time.Hour

// Brute-force throttling: after maxFailedAttempts failed logins for the
// same identifier (email, case-insensitive) within the window, further
// attempts are rejected until lockDuration has passed.
const maxFailedAttempts = 5
const failWindow = 1 * time.Minute
const lockDuration = 1 * time.Minute

type session struct {
	userID    uuid.UUID
	email     string
	csrfToken string
	expiresAt time.Time
}

type loginAttempts struct {
	count       int
	firstFailAt time.Time
	lockedUntil time.Time
}

// Manager issues/validates sessions and checks credentials against the
// users table.
type Manager struct {
	Store *db.Store

	mu       sync.Mutex
	sessions map[string]session
	attempts map[string]*loginAttempts
}

func NewManager(store *db.Store) *Manager {
	return &Manager{
		Store:    store,
		sessions: make(map[string]session),
		attempts: make(map[string]*loginAttempts),
	}
}

// Login verifies email/password and, on success, sets the session cookie.
// It throttles repeated failures per identifier to blunt password guessing.
func (m *Manager) Login(ctx context.Context, w http.ResponseWriter, r *http.Request, email, password string) error {
	if locked, wait := m.isLocked(email); locked {
		return fmt.Errorf("muitas tentativas falhas; tente novamente em %s", wait.Round(time.Second))
	}

	userID, hash, err := m.Store.GetUserPasswordHash(ctx, email)
	if err != nil {
		m.recordFailure(email)
		return fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		m.recordFailure(email)
		return fmt.Errorf("invalid credentials")
	}
	m.clearFailures(email)

	token, err := randomToken()
	if err != nil {
		return err
	}
	csrfToken, err := randomToken()
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.sessions[token] = session{userID: userID, email: email, csrfToken: csrfToken, expiresAt: time.Now().Add(sessionTTL)}
	m.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return nil
}

func (m *Manager) isLocked(email string) (bool, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.attempts[normalizeEmail(email)]
	if !ok {
		return false, 0
	}
	if time.Now().Before(a.lockedUntil) {
		return true, time.Until(a.lockedUntil)
	}
	return false, 0
}

func (m *Manager) recordFailure(email string) {
	key := normalizeEmail(email)
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.attempts[key]
	now := time.Now()
	if !ok || now.Sub(a.firstFailAt) > failWindow {
		a = &loginAttempts{firstFailAt: now}
		m.attempts[key] = a
	}
	a.count++
	if a.count >= maxFailedAttempts {
		a.lockedUntil = now.Add(lockDuration)
	}
}

func (m *Manager) clearFailures(email string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.attempts, normalizeEmail(email))
}

func normalizeEmail(email string) string {
	out := make([]byte, len(email))
	for i := 0; i < len(email); i++ {
		c := email[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		m.mu.Lock()
		delete(m.sessions, c.Value)
		m.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1})
}

// CurrentUser returns the logged-in user's email, or "" if not authenticated.
func (m *Manager) CurrentUser(r *http.Request) string {
	s, ok := m.currentSession(r)
	if !ok {
		return ""
	}
	return s.email
}

// CurrentUserID returns the logged-in user's ID, or uuid.Nil + false if not
// authenticated — used by the "Usuários" screen to stop a user deleting or
// demoting their own account.
func (m *Manager) CurrentUserID(r *http.Request) (uuid.UUID, bool) {
	s, ok := m.currentSession(r)
	if !ok {
		return uuid.Nil, false
	}
	return s.userID, true
}

// CSRFToken returns the current session's CSRF token, to be embedded as a
// hidden field in every state-changing form.
func (m *Manager) CSRFToken(r *http.Request) string {
	s, ok := m.currentSession(r)
	if !ok {
		return ""
	}
	return s.csrfToken
}

// ValidateCSRF checks the "csrf_token" form value against the session's
// token using a constant-time comparison.
func (m *Manager) ValidateCSRF(r *http.Request) bool {
	s, ok := m.currentSession(r)
	if !ok {
		return false
	}
	submitted := r.FormValue("csrf_token")
	return subtle.ConstantTimeCompare([]byte(submitted), []byte(s.csrfToken)) == 1
}

func (m *Manager) currentSession(r *http.Request) (session, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return session{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[c.Value]
	if !ok || time.Now().After(s.expiresAt) {
		return session{}, false
	}
	return s, true
}

// RequireLogin is middleware that redirects to /login when unauthenticated.
func (m *Manager) RequireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.CurrentUser(r) == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// RequireLoginAndCSRF additionally validates the CSRF token on POST/PUT/
// DELETE/PATCH requests — use for every form-submitting handler.
func (m *Manager) RequireLoginAndCSRF(next http.HandlerFunc) http.HandlerFunc {
	return m.RequireLogin(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.ParseForm()
			if !m.ValidateCSRF(r) {
				http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
				return
			}
		}
		next(w, r)
	})
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashPassword is used by the `create-user` CLI bootstrap commands.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

var _ = core.RoleAdmin // keep core imported for the role constants callers use
