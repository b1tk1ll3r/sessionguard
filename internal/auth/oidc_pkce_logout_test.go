package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/example/sessionguard/internal/model"
	"golang.org/x/oauth2"
)

func TestManagerLoginUsesPKCES256(t *testing.T) {
	m := &Manager{
		cfg: model.OIDCConfig{SecureCookie: true},
		oauth: oauth2.Config{
			ClientID:    "client-1",
			RedirectURL: "https://director.example/oidc/callback",
			Endpoint:    oauth2.Endpoint{AuthURL: "https://login.example/authorize"},
		},
		pending: map[string]pending{},
	}
	r := httptest.NewRequest("GET", "https://director.example/oidc/login", nil)
	w := httptest.NewRecorder()
	m.Login(w, r)

	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := loc.Query().Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method=%q, want S256", got)
	}
	if got := loc.Query().Get("code_challenge"); got == "" {
		t.Fatal("missing code_challenge")
	}
	state := loc.Query().Get("state")
	m.mu.Lock()
	p, ok := m.pending[state]
	m.mu.Unlock()
	if !ok || p.CodeVerifier == "" {
		t.Fatal("PKCE verifier was not retained for callback exchange")
	}
}

func TestManagerLogoutUsesIDTokenHintAndRegisteredRedirect(t *testing.T) {
	const session = "signed-session-cookie"
	m := &Manager{
		cfg: model.OIDCConfig{
			ClientID:          "director-client",
			LogoutRedirectURL: "https://director.example/",
			SecureCookie:      true,
		},
		endSession: "https://login.example/api/oidc/end-session",
		pending:    map[string]pending{},
		sessions: map[string]adminSession{
			hashAccessToken(session): {IDToken: "header.payload.signature", Exp: time.Now().Add(time.Hour)},
		},
	}
	r := httptest.NewRequest("GET", "https://director.example/logout", nil)
	r.AddCookie(&http.Cookie{Name: "sg_session", Value: session})
	w := httptest.NewRecorder()
	m.Logout(w, r)

	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	if q.Get("client_id") != "director-client" {
		t.Fatalf("client_id=%q", q.Get("client_id"))
	}
	if q.Get("id_token_hint") != "header.payload.signature" {
		t.Fatalf("id_token_hint=%q", q.Get("id_token_hint"))
	}
	if q.Get("post_logout_redirect_uri") != "https://director.example/" {
		t.Fatalf("post_logout_redirect_uri=%q", q.Get("post_logout_redirect_uri"))
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "sg_session=") {
		t.Fatal("local session cookie was not cleared")
	}
}
