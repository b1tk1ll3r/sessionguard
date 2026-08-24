package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

type logoutSession struct {
	IDToken string
	Exp     time.Time
}

type Manager struct {
	cfg        model.OIDCConfig
	provider   *oidc.Provider
	verifier   *oidc.IDTokenVerifier
	oauth      oauth2.Config
	endSession string
	key        []byte
	mu         sync.Mutex
	pending    map[string]pending
	logout     map[string]logoutSession
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
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &Manager{
		cfg:        cfg,
		provider:   p,
		verifier:   p.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth:      oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, Endpoint: p.Endpoint(), RedirectURL: cfg.RedirectURL, Scopes: []string{oidc.ScopeOpenID, "profile", "email", "groups"}},
		endSession: discovery.EndSessionEndpoint,
		key:        key,
		pending:    map[string]pending{},
		logout:     map[string]logoutSession{},
	}, nil
}

func randomURLSafe(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (m *Manager) Login(w http.ResponseWriter, r *http.Request) {
	state, nonce := randomURLSafe(24), randomURLSafe(24)
	verifier := oauth2.GenerateVerifier()
	m.mu.Lock()
	m.pruneLocked(time.Now())
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
		Groups           []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return err
	}
	u := User{Sub: claims.Sub, Email: claims.Email, Name: claims.Name, Groups: claims.Groups, Exp: time.Now().Add(8 * time.Hour).Unix()}
	if !m.allowed(u) {
		return errors.New("user is not in an allowed admin group")
	}
	value, err := m.sign(u)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.pruneLocked(time.Now())
	m.logout[value] = logoutSession{IDToken: raw, Exp: time.Unix(u.Exp, 0)}
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "sg_session", Value: value, Path: "/", HttpOnly: true, Secure: m.cfg.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: 8 * 3600})
	return nil
}

func (m *Manager) allowed(u User) bool {
	if len(m.cfg.AdminGroups) == 0 {
		return true
	}
	set := map[string]struct{}{}
	for _, g := range u.Groups {
		set[strings.ToLower(g)] = struct{}{}
	}
	for _, g := range m.cfg.AdminGroups {
		if _, ok := set[strings.ToLower(g)]; ok {
			return true
		}
	}
	return false
}

func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	var idToken string
	if c, err := r.Cookie("sg_session"); err == nil && c.Value != "" {
		m.mu.Lock()
		m.pruneLocked(time.Now())
		if sess, ok := m.logout[c.Value]; ok {
			idToken = sess.IDToken
			delete(m.logout, c.Value)
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

func (m *Manager) pruneLocked(now time.Time) {
	for state, p := range m.pending {
		if !now.Before(p.Exp) {
			delete(m.pending, state)
		}
	}
	for session, p := range m.logout {
		if !now.Before(p.Exp) {
			delete(m.logout, session)
		}
	}
}

func (m *Manager) sign(u User) (string, error) {
	b, err := json.Marshal(u)
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (m *Manager) parse(v string) (User, bool) {
	var u User
	parts := strings.Split(v, ".")
	if len(parts) != 2 {
		return u, false
	}
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte(parts[0]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return u, false
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(b, &u) != nil || time.Now().Unix() >= u.Exp {
		return User{}, false
	}
	if !m.allowed(u) {
		return User{}, false
	}
	return u, true
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
		u, ok := m.parse(c.Value)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}
