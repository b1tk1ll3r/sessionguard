package master

import (
	"net/http"
	"time"

	"github.com/example/sessionguard/internal/httpx"
	"github.com/example/sessionguard/internal/model"
)

// globalPolicy returns the global default policy (null if none is set).
func (a *App) globalPolicy(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	p := a.store.data.GlobalPolicy
	a.store.mu.RUnlock()
	httpx.JSON(w, 200, map[string]any{"policy": p})
}

// effectivePolicy shows which policy an agent receives and its origin, so the
// UI can make overrides visible ("agent", "farm:<id>", "global", "none").
func (a *App) effectivePolicy(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	rec, ok := a.store.data.Agents[r.PathValue("id")]
	if !ok {
		httpx.Error(w, 404, "agent not found")
		return
	}
	source, p := a.effectivePolicySourceLocked(rec)
	out := map[string]any{"source": source, "policy": p, "reported_revision": rec.Snapshot.PolicyRevision}
	if len(source) > 5 && source[:5] == "farm:" {
		if f, ok := a.store.data.Farms[source[5:]]; ok {
			out["farm_name"] = f.Name
		}
	}
	httpx.JSON(w, 200, out)
}

// agentPolicyClear removes a server-specific override so the server inherits
// its farm or the global policy again.
func (a *App) agentPolicyClear(w http.ResponseWriter, r *http.Request) {
	id, actor := r.PathValue("id"), requestActor(r)
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	rec, ok := a.store.data.Agents[id]
	if !ok {
		httpx.Error(w, 404, "agent not found")
		return
	}
	if rec.DesiredPolicy == nil {
		httpx.Error(w, 409, "server has no policy override")
		return
	}
	rec.DesiredPolicy = nil
	a.store.data.Agents[id] = rec
	source, _ := a.effectivePolicySourceLocked(rec)
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: actor, Action: "agent_policy_override_clear", Target: rec.Name, Result: "queued", Details: "now inherits " + source})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"source": source})
}

// farmPolicyClear removes a farm policy; member servers fall back to the next
// matching farm or the global policy.
func (a *App) farmPolicyClear(w http.ResponseWriter, r *http.Request) {
	id, actor := r.PathValue("id"), requestActor(r)
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	f, ok := a.store.data.Farms[id]
	if !ok {
		httpx.Error(w, 404, "farm not found")
		return
	}
	if f.Policy == nil {
		httpx.Error(w, 409, "farm has no policy")
		return
	}
	f.Policy = nil
	a.store.data.Farms[id] = f
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: actor, Action: "farm_policy_clear", Target: f.Name, Result: "queued"})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
