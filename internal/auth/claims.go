package auth

import (
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

// DefaultScopes keeps the historic Pocket ID behavior. Keycloak rejects
// unknown scopes (invalid_scope), so Keycloak deployments configure
// "scopes" explicitly, usually without "groups".
var DefaultScopes = []string{oidc.ScopeOpenID, "profile", "email", "groups"}

// DefaultGroupsClaims is used when no groups_claims are configured.
var DefaultGroupsClaims = []string{"groups"}

func scopesOrDefault(scopes []string) []string {
	if len(scopes) == 0 {
		return append([]string(nil), DefaultScopes...)
	}
	out := []string{oidc.ScopeOpenID}
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s != "" && s != oidc.ScopeOpenID {
			out = append(out, s)
		}
	}
	return out
}

// claimAt resolves a claim by its exact name first (so URI-style claim names
// containing dots keep working) and otherwise as a dot path into nested
// objects, e.g. "realm_access.roles" or "resource_access.sessionguard.roles"
// for Keycloak.
func claimAt(claims map[string]any, path string) (any, bool) {
	if v, ok := claims[path]; ok {
		return v, true
	}
	var cur any = claims
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// GroupsFromClaims merges the string values of all configured group/role
// claims, de-duplicated case-insensitively.
func GroupsFromClaims(claims map[string]any, paths []string) []string {
	if len(paths) == 0 {
		paths = DefaultGroupsClaims
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		for _, g := range claimStrings(claims, strings.TrimSpace(p)) {
			key := strings.ToLower(g)
			if !seen[key] {
				seen[key] = true
				out = append(out, g)
			}
		}
	}
	return out
}

// GroupMatches compares an IdP group with a configured group name
// case-insensitively. A single leading "/" is ignored on both sides because
// Keycloak emits full group paths ("/sessionguard-admins", "/it/admins") when
// "Full group path" is enabled; subgroups stay distinguishable by their path.
func GroupMatches(got, configured string) bool {
	return strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(got), "/"), strings.TrimPrefix(strings.TrimSpace(configured), "/"))
}
