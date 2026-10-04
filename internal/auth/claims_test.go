package auth

import (
	"encoding/json"
	"reflect"
	"testing"
)

const keycloakIDToken = `{
  "sub": "f3c0e1d2-0000-4000-8000-000000000001",
  "preferred_username": "j.bergner",
  "email": "j.bergner@example.org",
  "sid": "5c1d0000-aaaa-bbbb-cccc-000000000001",
  "groups": ["/sessionguard-admins", "/it/rds-multimonitor"],
  "realm_access": {"roles": ["default-roles-hilden", "offline_access"]},
  "resource_access": {"sessionguard": {"roles": ["operator"]}},
  "https://example.org/claims/groups": ["uri-style"]
}`

func keycloakClaims(t *testing.T) map[string]any {
	t.Helper()
	var claims map[string]any
	if err := json.Unmarshal([]byte(keycloakIDToken), &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestGroupsFromKeycloakClaims(t *testing.T) {
	claims := keycloakClaims(t)
	got := GroupsFromClaims(claims, []string{"groups", "resource_access.sessionguard.roles", "realm_access.roles", "groups"})
	want := []string{"/sessionguard-admins", "/it/rds-multimonitor", "operator", "default-roles-hilden", "offline_access"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := GroupsFromClaims(claims, []string{"https://example.org/claims/groups"}); len(got) != 1 || got[0] != "uri-style" {
		t.Fatalf("URI-style claim names with dots must resolve exactly: %v", got)
	}
	if got := GroupsFromClaims(claims, []string{"realm_access.missing", "nope"}); len(got) != 0 {
		t.Fatalf("missing claims must yield nothing: %v", got)
	}
	if got := claimString(claims, "preferred_username"); got != "j.bergner" {
		t.Fatalf("username claim: %q", got)
	}
}

func TestGroupMatchesKeycloakPaths(t *testing.T) {
	cases := []struct {
		got, configured string
		want            bool
	}{
		{"/sessionguard-admins", "sessionguard-admins", true},
		{"sessionguard-admins", "/SessionGuard-Admins", true},
		{"/it/rds-multimonitor", "it/rds-multimonitor", true},
		{"/it/rds-multimonitor", "rds-multimonitor", false},
		{"/other/admins", "/it/admins", false},
	}
	for _, c := range cases {
		if got := GroupMatches(c.got, c.configured); got != c.want {
			t.Errorf("GroupMatches(%q,%q)=%v want %v", c.got, c.configured, got, c.want)
		}
	}
}

func TestScopesOrDefault(t *testing.T) {
	if got := scopesOrDefault(nil); !reflect.DeepEqual(got, DefaultScopes) {
		t.Fatalf("default scopes: %v", got)
	}
	if got := scopesOrDefault([]string{"profile", "email", "openid", " "}); !reflect.DeepEqual(got, []string{"openid", "profile", "email"}) {
		t.Fatalf("openid must be first and not duplicated: %v", got)
	}
}
