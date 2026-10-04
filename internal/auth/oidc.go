package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/example/sessionguard/internal/model"
	"golang.org/x/oauth2"
)

type User struct {
	Sub    string   `json:"sub"`
	Email  string   `json:"email,omitempty"`
	Name   string   `json:"name,omitempty"`
	Groups []string `json:"groups,omitempty"`
	Exp    int64    `json:"exp"`
}

type pending struct {
	Nonce        string
	CodeVerifier string
	Exp          time.Time
}

// adminSession is the server-side state behind an opaque sg_session cookie.
// Only the SHA-256 of the cookie value is used as map key, so a memory dump of
// the map does not yield usable cookies.
type adminSession struct {
	ID       string
	User     User
	SID      string
	IDToken  string
	Created  time.Time
	LastSeen time.Time
	Exp      time.Time
}

// SessionInfo is the public view of an admin console session.
type SessionInfo struct {
	ID        string    `json:"id"`
	Subject   string    `json:"subject"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name,omitempty"`
	Groups    []string  `json:"groups,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	ExpiresAt time.Time `json:"expires_at"`
}

const (
	defaultSessionHours       = 8
	defaultIdleTimeoutMinutes = 60
	maxAdminSessions          = 10000
)

type Manager struct {
	cfg        model.OIDCConfig
	provider   *oidc.Provider
	verifier   *oidc.IDTokenVerifier
	logoutVer  *oidc.IDTokenVerifier
	oauth      oauth2.Config
	endSession string
	mu         sync.Mutex
	pending    map[string]pending
	sessions   map[string]adminSession
	logoutSeen map[string]time.Time
}

func New(ctx context.Context, cfg model.OIDCConfig) (*Manager, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("OIDC is not configured")
	}
	p, err := oidc.NewProvider(ctx, strings.TrimRight(cfg.Issuer, "/"))
	if err != nil {
		return nil, err
	}
	var discovery struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&discovery)
	return &Manager{
		cfg:        cfg,
		provider:   p,
		verifier:   p.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		logoutVer:  p.Verifier(&oidc.Config{ClientID: cfg.ClientID, SkipExpiryCheck: true}),
		oauth:      oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, Endpoint: p.Endpoint(), RedirectURL: cfg.RedirectURL, Scopes: scopesOrDefault(cfg.Scopes)},
		endSession: discovery.EndSessionEndpoint,
		pending:    map[string]pending{},
		sessions:   map[string]adminSession{},
		logoutSeen: map[string]time.Time{},
	}, nil
}

func randomURLSafe(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (m *Manager) sessionLifetime() time.Duration {
	if m.cfg.SessionHours > 0 {
		return time.Duration(m.cfg.SessionHours) * time.Hour
	}
	return defaultSessionHours * time.Hour
}

func (m *Manager) idleTimeout() time.Duration {
	if m.cfg.IdleTimeoutMinutes > 0 {
		return time.Duration(m.cfg.IdleTimeoutMinutes) * time.Minute
	}
	return defaultIdleTimeoutMinutes * time.Minute
}

func (m *Manager) Login(w http.ResponseWriter, r *http.Request) {
	state, nonce := randomURLSafe(24), randomURLSafe(24)
	verifier := oauth2.GenerateVerifier()
	m.mu.Lock()
	m.pruneLocked(time.Now())
	evictOnePendingIfFull(m.pending)
	m.pending[state] = pending{Nonce: nonce, CodeVerifier: verifier, Exp: time.Now().Add(5 * time.Minute)}
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "sg_oidc_state", Value: state, Path: "/oidc/callback", HttpOnly: true, Secure: m.cfg.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	http.Redirect(w, r, m.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (m *Manager) Callback(w http.ResponseWriter, r *http.Request) error {
	if e := r.URL.Query().Get("error"); e != "" {
		return fmt.Errorf("oidc error: %s", e)
	}
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie("sg_oidc_state")
	if err != nil || cookie.Value != state {
		return errors.New("OIDC state is not bound to this browser")
	}
	http.SetCookie(w, &http.Cookie{Name: "sg_oidc_state", Value: "", Path: "/oidc/callback", HttpOnly: true, Secure: m.cfg.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	m.mu.Lock()
	p, ok := m.pending[state]
	delete(m.pending, state)
	m.mu.Unlock()
	if !ok || time.Now().After(p.Exp) {
		return errors.New("invalid or expired OIDC state")
	}
	tok, err := m.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(p.CodeVerifier))
	if err != nil {
		return err
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return errors.New("missing id_token")
	}
	idToken, err := m.verifier.Verify(r.Context(), raw)
	if err != nil {
		return err
	}
	if idToken.Nonce != p.Nonce {
		return errors.New("invalid OIDC nonce")
	}
	var claims struct {
		Sub, Email, Name string
		SID              string `json:"sid"`
	}
	var all map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return err
	}
	if err := idToken.Claims(&all); err != nil {
		return err
	}
	now := time.Now().UTC()
	exp := now.Add(m.sessionLifetime())
	u := User{Sub: claims.Sub, Email: claims.Email, Name: claims.Name, Groups: GroupsFromClaims(all, m.cfg.GroupsClaims), Exp: exp.Unix()}
	if !m.allowed(u) {
		return errors.New("user is not in an allowed admin group")
	}
	value := randomURLSafe(32)
	m.mu.Lock()
	m.pruneLocked(now)
	evictOnePendingIfFull(m.sessions)
	m.sessions[hashAccessToken(value)] = adminSession{ID: randomURLSafe(12), User: u, SID: claims.SID, IDToken: raw, Created: now, LastSeen: now, Exp: exp}
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "sg_session", Value: value, Path: "/", HttpOnly: true, Secure: m.cfg.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: int(m.sessionLifetime().Seconds())})
	return nil
}

func (m *Manager) allowed(u User) bool {
	if len(m.cfg.AdminGroups) == 0 {
		// Fail closed: an IdP usually serves far more users than the
		// administrators of this console.
		return m.cfg.AllowAllAuthenticatedUsers
	}
	return allowedGroups(u.Groups, m.cfg.AdminGroups)
}

func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	var idToken string
	if c, err := r.Cookie("sg_session"); err == nil && c.Value != "" {
		key := hashAccessToken(c.Value)
		m.mu.Lock()
		if sess, ok := m.sessions[key]; ok {
			idToken = sess.IDToken
			delete(m.sessions, key)
		}
		m.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "sg_session", Value: "", Path: "/", HttpOnly: true, Secure: m.cfg.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1})

	target := strings.TrimSpace(m.cfg.LogoutRedirectURL)
	if target == "" {
		target = "/"
	}
	if m.endSession == "" {
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	u, err := url.Parse(m.endSession)
	if err != nil {
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	q := u.Query()
	q.Set("client_id", m.cfg.ClientID)
	if idToken != "" {
		q.Set("id_token_hint", idToken)
	}
	if strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "http://") {
		q.Set("post_logout_redirect_uri", target)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// BackchannelLogout implements OIDC Back-Channel Logout for the admin
// console: Pocket ID can end all console sessions of a user (or IdP session)
// immediately, e.g. after the user was disabled.
func (m *Manager) BackchannelLogout(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	raw := strings.TrimSpace(r.Form.Get("logout_token"))
	if raw == "" {
		http.Error(w, "missing logout_token", http.StatusBadRequest)
		return
	}
	tok, err := m.logoutVer.Verify(r.Context(), raw)
	if err != nil {
		http.Error(w, "invalid logout_token", http.StatusBadRequest)
		return
	}
	now := time.Now()
	claims, err := validateLogoutToken(tok, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	fresh := acceptJTI(m.logoutSeen, claims.JTI, now)
	n := 0
	if fresh {
		n = m.revokeLocked(func(s adminSession) bool {
			return (claims.SID != "" && s.SID == claims.SID) || (claims.SID == "" && claims.Sub != "" && s.User.Sub == claims.Sub)
		})
	}
	m.mu.Unlock()
	if !fresh {
		http.Error(w, "logout_token replayed", http.StatusBadRequest)
		return
	}
	log.Printf("OIDC back-channel logout revoked %d admin session(s)", n)
	w.WriteHeader(http.StatusOK)
}

// Sessions lists the currently valid admin console sessions.
func (m *Manager) Sessions() []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(time.Now())
	out := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, SessionInfo{ID: s.ID, Subject: s.User.Sub, Email: s.User.Email, Name: s.User.Name, Groups: s.User.Groups, CreatedAt: s.Created, LastSeen: s.LastSeen, ExpiresAt: s.Exp})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

// RevokeSession ends one admin console session by its public ID.
func (m *Manager) RevokeSession(id string) (SessionInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, s := range m.sessions {
		if s.ID == id {
			delete(m.sessions, k)
			return SessionInfo{ID: s.ID, Subject: s.User.Sub, Email: s.User.Email, Name: s.User.Name}, true
		}
	}
	return SessionInfo{}, false
}

// RevokeSubject ends all admin console sessions of an IdP subject.
func (m *Manager) RevokeSubject(sub string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revokeLocked(func(s adminSession) bool { return s.User.Sub == sub })
}

func (m *Manager) revokeLocked(match func(adminSession) bool) int {
	n := 0
	for k, s := range m.sessions {
		if match(s) {
			delete(m.sessions, k)
			n++
		}
	}
	return n
}

func (m *Manager) pruneLocked(now time.Time) {
	for state, p := range m.pending {
		if !now.Before(p.Exp) {
			delete(m.pending, state)
		}
	}
	idle := m.idleTimeout()
	for k, s := range m.sessions {
		if !now.Before(s.Exp) || now.Sub(s.LastSeen) > idle {
			delete(m.sessions, k)
		}
	}
}

// session resolves and refreshes the server-side session of a cookie value.
func (m *Manager) session(value string) (User, bool) {
	if value == "" {
		return User{}, false
	}
	key := hashAccessToken(value)
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	if !ok {
		return User{}, false
	}
	if !now.Before(s.Exp) || now.Sub(s.LastSeen) > m.idleTimeout() || !m.allowed(s.User) {
		delete(m.sessions, key)
		return User{}, false
	}
	s.LastSeen = now
	m.sessions[key] = s
	return s.User, true
}

type ctxKey int

const userKey ctxKey = 1

func UserFrom(r *http.Request) (User, bool) { u, ok := r.Context().Value(userKey).(User); return u, ok }

func (m *Manager) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("sg_session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		u, ok := m.session(c.Value)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}
