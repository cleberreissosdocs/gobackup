// Package webauth implements the simple login/session mechanism shared by
// gobackup-central and gobackup-agent's web UIs: bcrypt-hashed passwords
// and an in-memory session token carried in an httponly cookie.
package webauth

import (
	"context"
	"crypto/rand"
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

type session struct {
	userID    uuid.UUID
	email     string
	expiresAt time.Time
}

// Manager issues/validates sessions and checks credentials against the
// users table.
type Manager struct {
	Store *db.Store

	mu       sync.Mutex
	sessions map[string]session
}

func NewManager(store *db.Store) *Manager {
	return &Manager{Store: store, sessions: make(map[string]session)}
}

// Login verifies email/password and, on success, sets the session cookie.
func (m *Manager) Login(ctx context.Context, w http.ResponseWriter, email, password string) error {
	userID, hash, err := m.Store.GetUserPasswordHash(ctx, email)
	if err != nil {
		return fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return fmt.Errorf("invalid credentials")
	}

	token, err := randomToken()
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.sessions[token] = session{userID: userID, email: email, expiresAt: time.Now().Add(sessionTTL)}
	m.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return nil
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
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[c.Value]
	if !ok || time.Now().After(s.expiresAt) {
		return ""
	}
	return s.email
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
