package master

import (
	"log"
	"sort"
	"strings"
	"time"

	"github.com/example/sessionguard/internal/auth"
	"github.com/example/sessionguard/internal/model"
)

func identityKey(username string) string { return strings.ToLower(strings.TrimSpace(username)) }

// BindIdentity pins username to subject on first use and rejects any other
// subject for that username, as well as a subject switching to a different
// username (IdP-side rename). Bindings are only released by an admin.
func (r authSessionStore) BindIdentity(username, subject, issuer string, now time.Time) error {
	key := identityKey(username)
	if key == "" || strings.TrimSpace(subject) == "" {
		return auth.ErrIdentityConflict
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if r.s.data.IdentityBindings == nil {
		r.s.data.IdentityBindings = map[string]model.IdentityBinding{}
	}
	if b, ok := r.s.data.IdentityBindings[key]; ok {
		if b.Subject != subject {
			details := "username is bound to another OIDC subject"
			if b.Issuer != "" && b.Issuer != issuer {
				// Typical after an IdP migration (e.g. Pocket ID -> Keycloak).
				details = "username is bound to a subject of another issuer (" + b.Issuer + "); reset identity bindings after an IdP migration"
			}
			r.s.appendAuditLocked(model.AuditEntry{Time: now, Actor: "access-auth", Action: "identity_binding_conflict", Target: username, Result: "denied", Details: details})
			_ = r.s.saveLocked()
			return auth.ErrIdentityConflict
		}
		b.LastSeen = now
		r.s.data.IdentityBindings[key] = b
		// LastSeen is informational; avoid a state write on every login.
		return nil
	}
	for otherKey, b := range r.s.data.IdentityBindings {
		if b.Subject == subject && otherKey != key && (b.Issuer == "" || b.Issuer == issuer) {
			r.s.appendAuditLocked(model.AuditEntry{Time: now, Actor: "access-auth", Action: "identity_binding_conflict", Target: username, Result: "denied", Details: "OIDC subject is already bound to username " + b.Username})
			_ = r.s.saveLocked()
			return auth.ErrIdentityConflict
		}
	}
	r.s.data.IdentityBindings[key] = model.IdentityBinding{Username: username, Subject: subject, Issuer: issuer, CreatedAt: now, LastSeen: now}
	r.s.appendAuditLocked(model.AuditEntry{Time: now, Actor: "access-auth", Action: "identity_bind", Target: username, Result: "success"})
	return r.s.saveLocked()
}

// seedIdentityBindings pins identities of existing access sessions when
// upgrading from a version without bindings, oldest session first.
func seedIdentityBindings(d *data) {
	sessions := make([]model.AuthSession, 0, len(d.AuthSessions))
	for _, s := range d.AuthSessions {
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].CreatedAt.Before(sessions[j].CreatedAt) })
	bound := map[string]bool{}
	for _, b := range d.IdentityBindings {
		bound[b.Subject] = true
	}
	n := 0
	for _, s := range sessions {
		key := identityKey(s.Username)
		if key == "" || s.Subject == "" || bound[s.Subject] {
			continue
		}
		if _, exists := d.IdentityBindings[key]; exists {
			continue
		}
		d.IdentityBindings[key] = model.IdentityBinding{Username: s.Username, Subject: s.Subject, CreatedAt: s.CreatedAt, LastSeen: s.CreatedAt}
		bound[s.Subject] = true
		n++
	}
	if n > 0 {
		log.Printf("seeded %d identity binding(s) from existing access sessions", n)
	}
}
