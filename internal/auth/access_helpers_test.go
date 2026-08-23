package auth

import (
	"testing"
	"time"

	"github.com/example/sessionguard/internal/model"
)

func TestExternalCallbackPathAndLoginURL(t *testing.T) {
	m := &AccessManager{cfg: model.AccessAuthConfig{
		RedirectURL:       "https://ts.hilden.info/_sessionguard/auth/oidc/callback",
		LogoutRedirectURL: "https://ts.hilden.info/",
		AllowedHosts:      []string{"ts.hilden.info"},
	}}
	if got := m.externalCallbackPath(); got != "/_sessionguard/auth/oidc/callback" {
		t.Fatalf("callback path = %q", got)
	}
	want := "https://ts.hilden.info/_sessionguard/auth/login?return=https%3A%2F%2Fts.hilden.info%2F"
	if got := m.loginURL("https://ts.hilden.info/"); got != want {
		t.Fatalf("login URL = %q, want %q", got, want)
	}
}

func TestValidReturnURL(t *testing.T) {
	m := &AccessManager{cfg: model.AccessAuthConfig{
		LogoutRedirectURL: "https://ts.hilden.info/",
		AllowedHosts:      []string{"ts.hilden.info"},
	}}
	if got := m.validReturnURL("https://ts.hilden.info/#/client/1"); got != "https://ts.hilden.info/#/client/1" {
		t.Fatalf("allowed return URL changed to %q", got)
	}
	if got := m.validReturnURL("https://evil.example/"); got != "https://ts.hilden.info/" {
		t.Fatalf("open redirect was accepted: %q", got)
	}
	if got := m.validReturnURL("javascript:alert(1)"); got != "https://ts.hilden.info/" {
		t.Fatalf("non-https redirect was accepted: %q", got)
	}
}

func TestLogoutJTIReplayProtection(t *testing.T) {
	m := &AccessManager{logoutSeen: map[string]time.Time{}}
	now := time.Now()
	if !m.acceptLogoutJTI("abc", now) {
		t.Fatal("first jti rejected")
	}
	if m.acceptLogoutJTI("abc", now.Add(time.Second)) {
		t.Fatal("replayed jti accepted")
	}
	if !m.acceptLogoutJTI("abc", now.Add(16*time.Minute)) {
		t.Fatal("expired replay marker was not pruned")
	}
}

func TestAllowedGroupsCaseInsensitive(t *testing.T) {
	if !allowedGroups([]string{"SessionGuard-Users"}, []string{"sessionguard-users"}) {
		t.Fatal("case-insensitive group match failed")
	}
	if allowedGroups([]string{"other"}, []string{"sessionguard-users"}) {
		t.Fatal("unexpected group match")
	}
}
