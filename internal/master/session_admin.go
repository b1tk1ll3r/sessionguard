package master

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/example/sessionguard/internal/httpx"
	"github.com/example/sessionguard/internal/model"
)

func (a *App) adminSessions(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, 200, map[string]any{"sessions": a.auth.Sessions()})
}

func (a *App) adminSessionRevoke(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	info, ok := a.auth.RevokeSession(id)
	if !ok {
		httpx.Error(w, 404, "admin session not found")
		return
	}
	target := info.Email
	if target == "" {
		target = info.Subject
	}
	a.store.mu.Lock()
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: requestActor(r), Action: "admin_session_revoke", Target: target, Result: "success", Details: id})
	err := a.store.saveLocked()
	a.store.mu.Unlock()
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) identityBindings(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	out := make([]model.IdentityBinding, 0, len(a.store.data.IdentityBindings))
	for _, b := range a.store.data.IdentityBindings {
		out = append(out, b)
	}
	a.store.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Username) < strings.ToLower(out[j].Username) })
	httpx.JSON(w, 200, map[string]any{"bindings": out})
}

// identityBindingRelease removes a username binding, e.g. after a legitimate
// rename in the IdP. All access sessions using that username are revoked so
// the next login re-binds from scratch.
func (a *App) identityBindingRelease(w http.ResponseWriter, r *http.Request) {
	key := identityKey(r.PathValue("username"))
	a.store.mu.Lock()
	b, ok := a.store.data.IdentityBindings[key]
	if !ok {
		a.store.mu.Unlock()
		httpx.Error(w, 404, "identity binding not found")
		return
	}
	delete(a.store.data.IdentityBindings, key)
	revoked := 0
	for h, sess := range a.store.data.AuthSessions {
		if identityKey(sess.Username) == key || sess.Subject == b.Subject {
			delete(a.store.data.AuthSessions, h)
			revoked++
		}
	}
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: requestActor(r), Action: "identity_binding_release", Target: b.Username, Result: "success", Details: "subject=" + b.Subject})
	err := a.store.saveLocked()
	a.store.mu.Unlock()
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"released": b.Username, "revoked_sessions": revoked})
}

// identityBindingsReset removes all bindings and access sessions. It is meant
// for an IdP migration (new issuer => new subjects) and requires ?confirm=all.
// Until every user has logged in again, existing Guacamole usernames can be
// claimed by whoever logs in first with that name at the new IdP, so run it
// only once the new IdP's usernames are administratively controlled.
func (a *App) identityBindingsReset(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("confirm") != "all" {
		httpx.Error(w, 400, "add ?confirm=all to reset all identity bindings")
		return
	}
	a.store.mu.Lock()
	n, s := len(a.store.data.IdentityBindings), len(a.store.data.AuthSessions)
	a.store.data.IdentityBindings = map[string]model.IdentityBinding{}
	a.store.data.AuthSessions = map[string]model.AuthSession{}
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: requestActor(r), Action: "identity_bindings_reset", Target: "all", Result: "success", Details: fmt.Sprintf("bindings=%d access_sessions=%d", n, s)})
	err := a.store.saveLocked()
	a.store.mu.Unlock()
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"released": n, "revoked_sessions": s})
}
