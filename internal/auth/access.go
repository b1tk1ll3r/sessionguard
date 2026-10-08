package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/example/sessionguard/internal/model"
	"golang.org/x/oauth2"
)

const backchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"

type AccessSessionStore interface {
	PutAuthSession(model.AuthSession) error
	GetAuthSession(hash string) (model.AuthSession, bool)
	DeleteAuthSession(hash string) error
	RevokeAuthSessions(sid, sub string) (int, error)
	CleanupAuthSessions(time.Time) error
	// BindIdentity pins a username to an OIDC subject; it returns
	// ErrIdentityConflict if either is already bound differently.
	BindIdentity(username, subject, issuer string, now time.Time) error
}

// ErrIdentityConflict is returned when a username is bound to a different
// OIDC subject (or the subject to a different username).
var ErrIdentityConflict = errors.New("identity is bound to a different account")

// DefaultUsernamePattern accepts Pocket ID style usernames and rejects
// Unicode look-alikes, whitespace and control characters.
const DefaultUsernamePattern = `^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`

type accessPending struct {
	Nonce        string
	CodeVerifier string
	ReturnURL    string
	Exp          time.Time
}

type AccessManager struct {
	cfg        model.AccessAuthConfig
	provider   *oidc.Provider
	verifier   *oidc.IDTokenVerifier
	logoutVer  *oidc.IDTokenVerifier
	oauth      oauth2.Config
	sessions   AccessSessionStore
	endSession string
	mu         sync.Mutex
	pending    map[string]accessPending
	logoutSeen map[string]time.Time
	usernameRE *regexp.Regexp
	// lastCleanup throttles expired-session cleanup triggered by logins.
	lastCleanup time.Time
}

// maxPendingLogins bounds unauthenticated login state. When full, an
// arbitrary entry is evicted: under a flood these are almost always the
// attacker's own, while a legitimate user simply restarts the login.
const maxPendingLogins = 10000

func evictOnePendingIfFull[V any](m map[string]V) {
	if len(m) < maxPendingLogins {
		return
	}
	for k := range m {
		delete(m, k)
		return
	}
}

func NewAccess(ctx context.Context, cfg model.AccessAuthConfig, sessions AccessSessionStore) (*AccessManager, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if sessions == nil {
		return nil, errors.New("access auth session store is required")
	}
	p, err := oidc.NewProvider(ctx, strings.TrimRight(cfg.Issuer, "/"))
	if err != nil {
		return nil, err
	}
	var discovery struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&discovery)
	pattern := cfg.UsernamePattern
	if pattern == "" {
		pattern = DefaultUsernamePattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return &AccessManager{
		usernameRE: re,
		cfg:        cfg,
		provider:   p,
		verifier:   p.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		// Back-channel logout tokens are not ID tokens and may omit exp. We
		// still verify issuer, audience and signature, then validate the
		// logout-specific claims below.
		logoutVer: p.Verifier(&oidc.Config{ClientID: cfg.ClientID, SkipExpiryCheck: true}),
		oauth: oauth2.Config{
			ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
			Endpoint: p.Endpoint(), RedirectURL: cfg.RedirectURL,
			Scopes: scopesOrDefault(cfg.Scopes),
		},
		sessions:   sessions,
		endSession: discovery.EndSessionEndpoint,
		pending:    map[string]accessPending{},
		logoutSeen: map[string]time.Time{},
	}, nil
}

func (m *AccessManager) Register(mux *http.ServeMux) {
	if m == nil {
		return
	}
	mux.HandleFunc("/auth/verify", m.Verify)
	mux.HandleFunc("GET /auth/login", m.Login)
	mux.HandleFunc("GET /auth/oidc/callback", m.Callback)
	mux.HandleFunc("GET /auth/logout", m.Logout)
	mux.HandleFunc("POST /auth/logout", m.Logout)
	mux.HandleFunc("POST /auth/backchannel-logout", m.BackchannelLogout)
	mux.HandleFunc("GET /auth/status", m.Status)
}

func (m *AccessManager) Login(w http.ResponseWriter, r *http.Request) {
	target := m.validReturnURL(r.URL.Query().Get("return"))
	state, nonce := randomAccessToken(24), randomAccessToken(24)
	verifier := oauth2.GenerateVerifier()
	now := time.Now()
	m.mu.Lock()
	// Session cleanup takes the store write lock and may persist the whole
	// state; unauthenticated login hits must not trigger it every time.
	cleanup := now.Sub(m.lastCleanup) > time.Minute
	if cleanup {
		m.lastCleanup = now
	}
	m.prunePendingLocked(now)
	evictOnePendingIfFull(m.pending)
	m.pending[state] = accessPending{Nonce: nonce, CodeVerifier: verifier, ReturnURL: target, Exp: time.Now().Add(5 * time.Minute)}
	m.mu.Unlock()
	if cleanup {
		_ = m.sessions.CleanupAuthSessions(now.UTC())
	}

	// One state cookie per login flow avoids the common multi-tab race where a
	// second login overwrites the first flow's single state cookie.
	http.SetCookie(w, &http.Cookie{
		Name: stateCookieName(state), Value: state, Path: m.externalCallbackPath(),
		HttpOnly: true, Secure: m.cfg.SecureCookie, SameSite: http.SameSiteLaxMode,
		MaxAge: 300,
	})
	http.Redirect(w, r, m.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (m *AccessManager) Callback(w http.ResponseWriter, r *http.Request) {
	if e := r.URL.Query().Get("error"); e != "" {
		http.Error(w, "OIDC: "+e, http.StatusUnauthorized)
		return
	}
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		http.Error(w, "missing OIDC state", http.StatusUnauthorized)
		return
	}
	cookieName := stateCookieName(state)
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value != state {
		http.Error(w, "OIDC state is not bound to this browser", http.StatusUnauthorized)
		return
	}
	clearCookie(w, cookieName, "", m.externalCallbackPath(), m.cfg.SecureCookie)

	m.mu.Lock()
	p, ok := m.pending[state]
	delete(m.pending, state)
	m.mu.Unlock()
	if !ok || time.Now().After(p.Exp) {
		http.Error(w, "invalid or expired OIDC state", http.StatusUnauthorized)
		return
	}

	tok, err := m.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(p.CodeVerifier))
	if err != nil {
		http.Error(w, "OIDC token exchange failed", http.StatusUnauthorized)
		return
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || strings.TrimSpace(rawIDToken) == "" {
		http.Error(w, "missing id_token", http.StatusUnauthorized)
		return
	}
	idToken, err := m.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		http.Error(w, "invalid id_token", http.StatusUnauthorized)
		return
	}
	if idToken.Nonce != p.Nonce {
		http.Error(w, "invalid OIDC nonce", http.StatusUnauthorized)
		return
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "invalid OIDC claims", http.StatusUnauthorized)
		return
	}
	username := claimString(claims, m.cfg.UsernameClaim)
	if username == "" {
		// Pocket ID documents preferred_username and it is a safer Guacamole
		// identity than display-name. Never invent a fallback identity.
		username = claimString(claims, "preferred_username")
	}
	if username == "" {
		http.Error(w, "OIDC token has no usable username claim", http.StatusForbidden)
		return
	}
	if err := m.checkUsername(username); err != nil {
		log.Printf("access login denied for subject %q: %v", idToken.Subject, err)
		http.Error(w, "this account cannot be used for Guacamole access; please contact your administrator", http.StatusForbidden)
		return
	}
	groups := GroupsFromClaims(claims, m.cfg.GroupsClaims)
	if !allowedGroups(groups, m.cfg.AllowedGroups) {
		http.Error(w, "user is not in an allowed access group", http.StatusForbidden)
		return
	}
	// The username becomes the Guacamole identity (X-Guacamole-User). Pin it
	// to the immutable subject so that renaming an IdP account cannot take
	// over another user's Guacamole account.
	if err := m.sessions.BindIdentity(username, idToken.Subject, idToken.Issuer, time.Now().UTC()); err != nil {
		log.Printf("access login denied for subject %q as %q: %v", idToken.Subject, username, err)
		http.Error(w, "this username is bound to a different account; please contact your administrator", http.StatusForbidden)
		return
	}

	browserToken := randomAccessToken(32)
	now := time.Now().UTC()
	sess := model.AuthSession{
		ID: randomAccessToken(12), TokenHash: hashAccessToken(browserToken), Subject: idToken.Subject,
		SID: claimString(claims, "sid"), Username: username,
		Email: claimString(claims, "email"), Name: claimString(claims, "name"),
		Groups: groups, IDToken: rawIDToken, CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(m.cfg.SessionHours) * time.Hour),
	}
	if err := m.sessions.PutAuthSession(sess); err != nil {
		http.Error(w, "could not create access session", http.StatusInternalServerError)
		return
	}
	m.setSessionCookie(w, browserToken, int(time.Until(sess.ExpiresAt).Seconds()))
	http.Redirect(w, r, p.ReturnURL, http.StatusFound)
}

func (m *AccessManager) Verify(w http.ResponseWriter, r *http.Request) {
	sess, ok := m.sessionFromRequest(r)
	if !ok {
		target := m.forwardedTarget(r)
		http.Redirect(w, r, m.loginURL(target), http.StatusFound)
		return
	}
	// These are the only identity headers Traefik should copy to Guacamole.
	// authResponseHeaders replaces conflicting client-provided values.
	w.Header().Set("X-Guacamole-User", sess.Username)
	w.Header().Set("X-SessionGuard-User", sess.Username)
	if sess.Email != "" {
		w.Header().Set("X-SessionGuard-Email", sess.Email)
	}
	if len(sess.Groups) > 0 {
		w.Header().Set("X-SessionGuard-Groups", strings.Join(sess.Groups, ","))
	}
	// Always emitted when group sync is enabled (even empty) so the proxy
	// overwrites any client-supplied value. Guacamole applies the permissions
	// of user groups with these names.
	if m.cfg.GuacamoleGroups.Enabled {
		w.Header().Set(GuacamoleGroupsHeader, EncodeGuacamoleGroups(GuacamoleGroups(sess.Groups, m.cfg.GuacamoleGroups)))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

func (m *AccessManager) Status(w http.ResponseWriter, r *http.Request) {
	sess, ok := m.sessionFromRequest(r)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"authenticated":false}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"authenticated": true, "username": sess.Username, "email": sess.Email,
		"groups": sess.Groups, "expires_at": sess.ExpiresAt,
		"guacamole_groups": GuacamoleGroups(sess.Groups, m.cfg.GuacamoleGroups),
	})
}

func (m *AccessManager) Logout(w http.ResponseWriter, r *http.Request) {
	sess, _ := m.sessionFromRequest(r)
	if c, err := r.Cookie(m.cfg.CookieName); err == nil {
		_ = m.sessions.DeleteAuthSession(hashAccessToken(c.Value))
	}
	clearCookie(w, m.cfg.CookieName, m.cfg.CookieDomain, "/", m.cfg.SecureCookie)

	target := strings.TrimSpace(m.cfg.LogoutRedirectURL)
	if target == "" {
		target = m.validReturnURL(r.URL.Query().Get("return"))
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
	if sess.IDToken != "" {
		q.Set("id_token_hint", sess.IDToken)
	}
	if target != "" {
		q.Set("post_logout_redirect_uri", target)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (m *AccessManager) BackchannelLogout(w http.ResponseWriter, r *http.Request) {
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
	claims, err := validateLogoutToken(tok, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !m.acceptLogoutJTI(claims.JTI, time.Now()) {
		http.Error(w, "logout_token replayed", http.StatusBadRequest)
		return
	}
	if _, err := m.sessions.RevokeAuthSessions(claims.SID, claims.Sub); err != nil {
		http.Error(w, "could not revoke access session", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// SessionFromRequest returns the valid (unexpired, unrevoked) access session
// referenced by the request cookie. It is used by Master endpoints which are
// published below the Guacamole access-auth prefix.
func (m *AccessManager) SessionFromRequest(r *http.Request) (model.AuthSession, bool) {
	if m == nil {
		return model.AuthSession{}, false
	}
	return m.sessionFromRequest(r)
}

func (m *AccessManager) sessionFromRequest(r *http.Request) (model.AuthSession, bool) {
	c, err := r.Cookie(m.cfg.CookieName)
	if err != nil || strings.TrimSpace(c.Value) == "" {
		return model.AuthSession{}, false
	}
	sess, ok := m.sessions.GetAuthSession(hashAccessToken(c.Value))
	// Re-check on every request so sessions created before a reserved name
	// or a stricter pattern was configured stop working immediately.
	if !ok || m.checkUsername(sess.Username) != nil {
		return model.AuthSession{}, false
	}
	return sess, true
}

func (m *AccessManager) setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: m.cfg.CookieName, Value: token, Path: "/", Domain: m.cfg.CookieDomain,
		HttpOnly: true, Secure: m.cfg.SecureCookie, SameSite: http.SameSiteLaxMode,
		MaxAge: maxAge,
	})
}

func (m *AccessManager) forwardedTarget(r *http.Request) string {
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	uri := strings.TrimSpace(r.Header.Get("X-Forwarded-Uri"))
	if proto == "" {
		proto = "https"
	}
	if uri == "" {
		uri = "/"
	}
	if host == "" {
		return m.cfg.LogoutRedirectURL
	}
	return m.validReturnURL(proto + "://" + host + uri)
}

func (m *AccessManager) externalCallbackPath() string {
	u, err := url.Parse(m.cfg.RedirectURL)
	if err != nil || strings.TrimSpace(u.Path) == "" {
		return "/auth/oidc/callback"
	}
	return u.Path
}

func (m *AccessManager) loginURL(target string) string {
	u, _ := url.Parse(m.cfg.RedirectURL)
	// Preserve an external Traefik prefix such as /_sessionguard. The Master
	// itself sees /auth/* after StripPrefix, while the browser must be sent to
	// the externally routable prefixed URL.
	base := strings.TrimSuffix(u.Path, "/oidc/callback")
	if base == u.Path {
		base = strings.TrimSuffix(u.Path, "/")
	}
	u.Path = base + "/login"
	u.RawQuery = url.Values{"return": []string{target}}.Encode()
	return u.String()
}

func (m *AccessManager) validReturnURL(raw string) string {
	fallback := strings.TrimSpace(m.cfg.LogoutRedirectURL)
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return fallback
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range m.cfg.AllowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == host {
			return u.String()
		}
		if strings.HasPrefix(allowed, "*.") && strings.HasSuffix(host, allowed[1:]) {
			return u.String()
		}
	}
	// If no allow-list was provided, constrain redirects to the configured
	// cookie domain or, for host-only cookies, to logout_redirect_url. This is
	// still closed against arbitrary open redirects.
	if len(m.cfg.AllowedHosts) == 0 {
		if m.cfg.CookieDomain != "" {
			d := strings.TrimPrefix(strings.ToLower(m.cfg.CookieDomain), ".")
			if host == d || strings.HasSuffix(host, "."+d) {
				return u.String()
			}
		}
		if f, err := url.Parse(fallback); err == nil && strings.EqualFold(f.Hostname(), host) {
			return u.String()
		}
	}
	return fallback
}

func (m *AccessManager) acceptLogoutJTI(jti string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return acceptJTI(m.logoutSeen, jti, now)
}

func (m *AccessManager) prunePendingLocked(now time.Time) {
	for k, p := range m.pending {
		if now.After(p.Exp) {
			delete(m.pending, k)
		}
	}
}

// checkUsername rejects reserved and malformed Guacamole identities.
func (m *AccessManager) checkUsername(username string) error {
	re := m.usernameRE
	if re == nil {
		re = regexp.MustCompile(DefaultUsernamePattern)
	}
	if !re.MatchString(username) {
		return errors.New("username does not match access_auth.username_pattern")
	}
	for _, reserved := range m.cfg.ReservedUsernames {
		if strings.EqualFold(strings.TrimSpace(reserved), username) {
			return errors.New("username is reserved")
		}
	}
	return nil
}

func allowedGroups(got, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, g := range got {
		for _, a := range allowed {
			if GroupMatches(g, a) {
				return true
			}
		}
	}
	return false
}

func claimString(claims map[string]any, key string) string {
	v, ok := claimAt(claims, key)
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func claimStrings(claims map[string]any, key string) []string {
	v, ok := claimAt(claims, key)
	if !ok {
		return nil
	}
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		return append([]string(nil), x...)
	case string:
		if strings.TrimSpace(x) != "" {
			return []string{strings.TrimSpace(x)}
		}
	}
	return nil
}

func randomAccessToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hashAccessToken(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func stateCookieName(state string) string {
	if len(state) > 16 {
		state = state[:16]
	}
	return "sg_access_state_" + state
}
func clearCookie(w http.ResponseWriter, name, domain, path string, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Domain: domain, Path: path, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}
