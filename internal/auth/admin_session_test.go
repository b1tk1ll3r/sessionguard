package auth

import (
	"testing"
	"time"

	"github.com/example/sessionguard/internal/model"
)

func adminTestManager() *Manager {
	return &Manager{
		cfg:        model.OIDCConfig{AdminGroups: []string{"sg-admins"}, IdleTimeoutMinutes: 30},
		pending:    map[string]pending{},
		sessions:   map[string]adminSession{},
		logoutSeen: map[string]time.Time{},
	}
}

func addAdminSession(m *Manager, cookie, id, sub, sid string, lastSeen time.Time) {
	u := User{Sub: sub, Groups: []string{"SG-Admins"}}
	m.sessions[hashAccessToken(cookie)] = adminSession{ID: id, User: u, SID: sid, Created: lastSeen, LastSeen: lastSeen, Exp: time.Now().Add(time.Hour)}
}

func TestAdminSessionServerSideRevocation(t *testing.T) {
	m := adminTestManager()
	now := time.Now().UTC()
	addAdminSession(m, "c1", "s1", "alice", "idp-1", now)
	addAdminSession(m, "c2", "s2", "alice", "idp-2", now)
	addAdminSession(m, "c3", "s3", "bob", "idp-3", now)

	if _, ok := m.session("c1"); !ok {
		t.Fatal("valid session rejected")
	}
	if _, ok := m.session("forged"); ok {
		t.Fatal("unknown cookie accepted")
	}
	if _, ok := m.RevokeSession("s1"); !ok {
		t.Fatal("RevokeSession failed")
	}
	if _, ok := m.session("c1"); ok {
		t.Fatal("revoked session still valid")
	}
	if n := m.RevokeSubject("alice"); n != 1 {
		t.Fatalf("RevokeSubject revoked %d, want 1", n)
	}
	if _, ok := m.session("c3"); !ok {
		t.Fatal("other user's session must survive")
	}
	if got := len(m.Sessions()); got != 1 {
		t.Fatalf("Sessions()=%d, want 1", got)
	}
}

func TestAdminSessionIdleAndGroupRecheck(t *testing.T) {
	m := adminTestManager()
	addAdminSession(m, "idle", "s1", "alice", "", time.Now().Add(-31*time.Minute))
	if _, ok := m.session("idle"); ok {
		t.Fatal("idle session must expire")
	}
	addAdminSession(m, "c2", "s2", "bob", "", time.Now())
	m.cfg.AdminGroups = []string{"other-group"}
	if _, ok := m.session("c2"); ok {
		t.Fatal("session must end when its groups are no longer admitted")
	}
}

func TestAdminBackchannelRevokesBySIDThenSub(t *testing.T) {
	m := adminTestManager()
	now := time.Now().UTC()
	addAdminSession(m, "c1", "s1", "alice", "idp-1", now)
	addAdminSession(m, "c2", "s2", "alice", "idp-2", now)
	m.mu.Lock()
	n := m.revokeLocked(func(s adminSession) bool { return s.SID == "idp-1" })
	m.mu.Unlock()
	if n != 1 {
		t.Fatalf("sid revocation removed %d", n)
	}
	if !acceptJTI(m.logoutSeen, "jti-1", now) || acceptJTI(m.logoutSeen, "jti-1", now) {
		t.Fatal("logout token replay protection failed")
	}
}
