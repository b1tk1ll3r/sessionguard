package auth

import (
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/example/sessionguard/internal/model"
)

func TestGuacamoleGroupsMapping(t *testing.T) {
	groups := []string{"/guac-sage-users", "guac-Office", "/sessionguard-admins", "/it/rds-admins", "guac-office", "", "evil\ngroup", strings.Repeat("x", 129)}

	if got := GuacamoleGroups(groups, model.GuacamoleGroupMapping{}); got != nil {
		t.Fatalf("disabled mapping must yield nothing: %v", got)
	}
	all := GuacamoleGroups(groups, model.GuacamoleGroupMapping{Enabled: true})
	want := []string{"guac-Office", "guac-sage-users", "it/rds-admins", "sessionguard-admins"}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("all groups: got %v want %v", all, want)
	}
	pre := GuacamoleGroups(groups, model.GuacamoleGroupMapping{Enabled: true, Prefix: "guac-", StripPrefix: true, Map: map[string]string{"/it/rds-admins": "RDS Admins"}, Exclude: []string{"guac-office"}})
	if want := []string{"RDS Admins", "sage-users"}; !reflect.DeepEqual(pre, want) {
		t.Fatalf("prefix/map/exclude: got %v want %v", pre, want)
	}
}

func TestEncodeGuacamoleGroupsRoundTrip(t *testing.T) {
	in := []string{"Vertrieb-Süd", "a,b", "RDS Admins", "x+y", "it/rds"}
	enc := EncodeGuacamoleGroups(in)
	if strings.ContainsAny(enc, " +/ü") {
		t.Fatalf("unsafe characters in header value: %q", enc)
	}
	parts := strings.Split(enc, ",")
	if len(parts) != len(in) {
		t.Fatalf("comma inside a name must be encoded: %q", enc)
	}
	for i, p := range parts {
		dec, err := url.PathUnescape(p)
		if err != nil || dec != in[i] {
			t.Fatalf("round trip %q -> %q -> %q (%v)", in[i], p, dec, err)
		}
	}
}

type stubSessions struct{ sess model.AuthSession }

func (s stubSessions) PutAuthSession(model.AuthSession) error { return nil }
func (s stubSessions) GetAuthSession(string) (model.AuthSession, bool) {
	return s.sess, true
}
func (s stubSessions) DeleteAuthSession(string) error                       { return nil }
func (s stubSessions) RevokeAuthSessions(string, string) (int, error)       { return 0, nil }
func (s stubSessions) CleanupAuthSessions(time.Time) error                  { return nil }
func (s stubSessions) BindIdentity(string, string, string, time.Time) error { return nil }

func TestVerifyEmitsGuacamoleGroups(t *testing.T) {
	sess := model.AuthSession{Username: "mmuster", Groups: []string{"/guac-sage", "staff"}, ExpiresAt: time.Now().Add(time.Hour)}
	m := &AccessManager{cfg: model.AccessAuthConfig{CookieName: "c", GuacamoleGroups: model.GuacamoleGroupMapping{Enabled: true, Prefix: "guac-", StripPrefix: true}}, sessions: stubSessions{sess}}
	r := httptest.NewRequest("GET", "/auth/verify", nil)
	r.Header.Set("Cookie", "c=token")
	w := httptest.NewRecorder()
	m.Verify(w, r)
	if w.Code != 200 || w.Header().Get("X-Guacamole-User") != "mmuster" || w.Header().Get(GuacamoleGroupsHeader) != "sage" {
		t.Fatalf("code=%d user=%q groups=%q", w.Code, w.Header().Get("X-Guacamole-User"), w.Header().Get(GuacamoleGroupsHeader))
	}
	// No matching group: header must still be present (empty) to override
	// client-supplied values at the proxy.
	m.sessions = stubSessions{model.AuthSession{Username: "mmuster", Groups: []string{"staff"}}}
	w = httptest.NewRecorder()
	m.Verify(w, r)
	if _, ok := w.Header()[GuacamoleGroupsHeader]; !ok {
		t.Fatal("empty groups header must still be emitted")
	}
}
