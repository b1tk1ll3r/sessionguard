package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/example/sessionguard/internal/model"
	jose "github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

const testIssuer = "https://sso.example.test"

// fakeIdP is a token endpoint with refresh-token rotation and reuse
// detection like Caddy Sentinel: every refresh token is valid exactly once,
// a reused one ends the whole grant.
type fakeIdP struct {
	t      *testing.T
	key    *rsa.PrivateKey
	srv    *httptest.Server
	mu     sync.Mutex
	valid  map[string]bool
	used   map[string]bool
	n      int
	sub    string
	groups []string
	user   string
	down   bool
	calls  int
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, key: k, valid: map[string]bool{"rt-0": true}, used: map[string]bool{}, sub: "u-alice", user: "alice", groups: []string{"guac-users"}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.token))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.down {
		http.Error(w, "unavailable", http.StatusBadGateway)
		return
	}
	_ = r.ParseForm()
	rt := r.Form.Get("refresh_token")
	fail := func() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}
	if r.Form.Get("grant_type") != "refresh_token" || !f.valid[rt] {
		if f.used[rt] {
			f.valid = map[string]bool{} // reuse: the whole family ends
		}
		fail()
		return
	}
	delete(f.valid, rt)
	f.used[rt] = true
	f.n++
	next := "rt-" + string(rune('0'+f.n))
	f.valid[next] = true
	idt := f.sign(map[string]any{"iss": testIssuer, "aud": "sg-access", "sub": f.sub, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"preferred_username": f.user, "email": f.user + "@example.test", "groups": f.groups, "sid": "sid-1"})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "refresh_token": next, "id_token": idt})
}

func (f *fakeIdP) sign(claims map[string]any) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key}, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	b, _ := json.Marshal(claims)
	obj, err := signer.Sign(b)
	if err != nil {
		f.t.Fatal(err)
	}
	s, err := obj.CompactSerialize()
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *fakeIdP) revalidator(minutes int) *revalidator {
	cfg := oauth2.Config{ClientID: "sg-access", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: f.srv.URL, AuthStyle: oauth2.AuthStyleInHeader}}
	ver := oidc.NewVerifier(testIssuer, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&f.key.PublicKey}}, &oidc.Config{ClientID: "sg-access"})
	return newRevalidator(minutes, cfg, ver, f.srv.Client())
}

// memSessions is an in-memory AccessSessionStore.
type memSessions struct {
	mu sync.Mutex
	m  map[string]model.AuthSession
}

func (s *memSessions) PutAuthSession(x model.AuthSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[x.TokenHash] = x
	return nil
}
func (s *memSessions) GetAuthSession(h string) (model.AuthSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.m[h]
	return x, ok
}
func (s *memSessions) DeleteAuthSession(h string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, h)
	return nil
}
func (s *memSessions) RevokeAuthSessions(string, string) (int, error)       { return 0, nil }
func (s *memSessions) CleanupAuthSessions(time.Time) error                  { return nil }
func (s *memSessions) BindIdentity(string, string, string, time.Time) error { return nil }

const testCookie = "browser-token"

func accessWithSession(f *fakeIdP, allowed []string, validated time.Time) (*AccessManager, *memSessions) {
	store := &memSessions{m: map[string]model.AuthSession{}}
	h := hashAccessToken(testCookie)
	store.m[h] = model.AuthSession{ID: "s1", TokenHash: h, Subject: "u-alice", Username: "alice", Groups: []string{"guac-users"},
		RefreshToken: "rt-0", ValidatedAt: validated, ExpiresAt: time.Now().Add(8 * time.Hour)}
	m := &AccessManager{cfg: model.AccessAuthConfig{CookieName: "sg_access_session", AllowedGroups: allowed}, sessions: store, reval: f.revalidator(5)}
	return m, store
}

func accessRequest() *http.Request {
	r := httptest.NewRequest("GET", "https://guac.example.test/", nil)
	r.AddCookie(&http.Cookie{Name: "sg_access_session", Value: testCookie})
	return r
}

func TestRevalidationNotDueSkipsIdP(t *testing.T) {
	f := newFakeIdP(t)
	m, _ := accessWithSession(f, nil, time.Now())
	if _, ok := m.sessionFromRequest(accessRequest()); !ok {
		t.Fatal("fresh session rejected")
	}
	if f.calls != 0 {
		t.Fatalf("IdP called %d times for a fresh session", f.calls)
	}
}

func TestRevalidationRotatesTokenAndUpdatesGroups(t *testing.T) {
	f := newFakeIdP(t)
	f.groups = []string{"guac-users", "guac-admins"}
	m, store := accessWithSession(f, []string{"guac-users"}, time.Now().Add(-6*time.Minute))
	sess, ok := m.sessionFromRequest(accessRequest())
	if !ok {
		t.Fatal("revalidation failed")
	}
	if len(sess.Groups) != 2 || sess.RefreshToken != "rt-1" || time.Since(sess.ValidatedAt) > time.Minute {
		t.Fatalf("session not updated: %+v", sess)
	}
	stored, _ := store.GetAuthSession(hashAccessToken(testCookie))
	if stored.RefreshToken != "rt-1" {
		t.Fatalf("rotated refresh token not persisted: %q", stored.RefreshToken)
	}
	// a second request must not refresh again (and must not reuse rt-0)
	if _, ok := m.sessionFromRequest(accessRequest()); !ok || f.calls != 1 {
		t.Fatalf("second request: ok=%v calls=%d", ok, f.calls)
	}
}

func TestRevalidationRefusedEndsSession(t *testing.T) {
	f := newFakeIdP(t)
	f.valid = map[string]bool{} // grant revoked at the IdP
	m, store := accessWithSession(f, nil, time.Now().Add(-6*time.Minute))
	if _, ok := m.sessionFromRequest(accessRequest()); ok {
		t.Fatal("session survived a refused refresh")
	}
	if _, ok := store.GetAuthSession(hashAccessToken(testCookie)); ok {
		t.Fatal("refused session was not deleted")
	}
}

func TestRevalidationRemovedGroupEndsSession(t *testing.T) {
	f := newFakeIdP(t)
	f.groups = []string{"other"}
	m, _ := accessWithSession(f, []string{"guac-users"}, time.Now().Add(-6*time.Minute))
	if _, ok := m.sessionFromRequest(accessRequest()); ok {
		t.Fatal("session survived although its group was removed")
	}
}

func TestRevalidationSubjectOrUsernameChangeEndsSession(t *testing.T) {
	f := newFakeIdP(t)
	f.user = "mallory"
	m, _ := accessWithSession(f, nil, time.Now().Add(-6*time.Minute))
	if _, ok := m.sessionFromRequest(accessRequest()); ok {
		t.Fatal("session survived a username change")
	}
	f2 := newFakeIdP(t)
	f2.sub = "u-other"
	m2, _ := accessWithSession(f2, nil, time.Now().Add(-6*time.Minute))
	if _, ok := m2.sessionFromRequest(accessRequest()); ok {
		t.Fatal("session survived a subject change")
	}
}

func TestRevalidationGraceWhileIdPUnavailable(t *testing.T) {
	f := newFakeIdP(t)
	f.down = true
	m, store := accessWithSession(f, nil, time.Now().Add(-6*time.Minute))
	if _, ok := m.sessionFromRequest(accessRequest()); !ok {
		t.Fatal("session must survive an unreachable IdP within the grace period")
	}
	// retries are throttled: no IdP call per request
	if _, ok := m.sessionFromRequest(accessRequest()); !ok || f.calls != 1 {
		t.Fatalf("throttle: ok=%v calls=%d", ok, f.calls)
	}
	// beyond twice the interval the session is no longer accepted
	h := hashAccessToken(testCookie)
	s := store.m[h]
	s.ValidatedAt = time.Now().Add(-11 * time.Minute)
	store.m[h] = s
	if _, ok := m.sessionFromRequest(accessRequest()); ok {
		t.Fatal("session accepted beyond the grace period")
	}
	if _, ok := store.GetAuthSession(h); !ok {
		t.Fatal("a transient IdP outage must not delete the session")
	}
}

func TestRevalidationConcurrentRequestsRefreshOnce(t *testing.T) {
	f := newFakeIdP(t)
	m, _ := accessWithSession(f, nil, time.Now().Add(-6*time.Minute))
	var wg sync.WaitGroup
	fails := 0
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := m.sessionFromRequest(accessRequest()); !ok {
				mu.Lock()
				fails++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if fails != 0 || f.calls != 1 {
		t.Fatalf("concurrent revalidation: fails=%d calls=%d (rotation must not be raced)", fails, f.calls)
	}
}

func TestLegacySessionWithoutRefreshTokenEnds(t *testing.T) {
	f := newFakeIdP(t)
	m, store := accessWithSession(f, nil, time.Time{})
	h := hashAccessToken(testCookie)
	s := store.m[h]
	s.RefreshToken = ""
	store.m[h] = s
	if _, ok := m.sessionFromRequest(accessRequest()); ok {
		t.Fatal("session without refresh token survived revalidation")
	}
}

func TestAdminRevalidation(t *testing.T) {
	f := newFakeIdP(t)
	f.groups = []string{"sg-admins"}
	m := adminTestManager()
	m.reval = f.revalidator(5)
	key := hashAccessToken("admin-cookie")
	m.sessions[key] = adminSession{ID: "a1", User: User{Sub: "u-alice", Groups: []string{"sg-admins"}}, RefreshToken: "rt-0",
		Validated: time.Now().Add(-6 * time.Minute), LastSeen: time.Now(), Exp: time.Now().Add(time.Hour)}
	if _, ok := m.revalidate(context.Background(), key); !ok {
		t.Fatal("admin revalidation failed")
	}
	if m.sessions[key].RefreshToken != "rt-1" {
		t.Fatal("admin refresh token not rotated")
	}
	f.groups = []string{"users"}
	s := m.sessions[key]
	s.Validated = time.Now().Add(-6 * time.Minute)
	m.sessions[key] = s
	if _, ok := m.revalidate(context.Background(), key); ok {
		t.Fatal("admin session survived removal from the admin group")
	}
	if _, ok := m.sessions[key]; ok {
		t.Fatal("ended admin session not removed")
	}
}

func TestWithOfflineAccess(t *testing.T) {
	got := withOfflineAccess([]string{"openid", "profile"}, true)
	if len(got) != 3 || got[2] != "offline_access" {
		t.Fatalf("got %v", got)
	}
	if got := withOfflineAccess([]string{"openid"}, false); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}
