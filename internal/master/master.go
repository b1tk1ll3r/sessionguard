package master

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/example/sessionguard/internal/auth"
	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/httpx"
	"github.com/example/sessionguard/internal/model"
	"github.com/example/sessionguard/internal/webui"
)

const Version = "0.6.0"

type App struct {
	cfg    config.Master
	store  *store
	auth   *auth.Manager
	access *auth.AccessManager
	http   *http.Client
}

func New(ctx context.Context, cfg config.Master) (*App, error) {
	s, err := newStore(ctx, cfg)
	if err != nil {
		return nil, err
	}
	a, err := auth.New(ctx, cfg.OIDC)
	if err != nil {
		_ = s.close()
		return nil, fmt.Errorf("OIDC: %w", err)
	}
	access, err := auth.NewAccess(ctx, cfg.AccessAuth, authSessionStore{s: s})
	if err != nil {
		_ = s.close()
		return nil, fmt.Errorf("access auth: %w", err)
	}
	return &App{cfg: cfg, store: s, auth: a, access: access, http: &http.Client{Timeout: 8 * time.Second}}, nil
}

func (a *App) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	a.auth.Register(mux)
	if a.access != nil {
		a.access.Register(mux)
		mux.HandleFunc("GET /auth/display-policy", a.displayPolicy)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, 200, map[string]any{"ok": true, "version": Version, "store": a.store.kind()})
	})
	mux.HandleFunc("GET /metrics", a.metrics)
	mux.HandleFunc("POST /api/v1/agents/enroll", a.enroll)
	mux.HandleFunc("POST /api/v1/agents/heartbeat", a.heartbeat)
	mux.HandleFunc("POST /api/v1/broker/resolve", a.brokerResolve)
	mux.HandleFunc("POST /api/v1/broker/tokens", a.brokerTokens)
	webui.Register(mux)
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		_, _ = fmt.Fprint(w, masterJS)
	})
	mux.Handle("GET /{$}", a.auth.Require(http.HandlerFunc(a.masterPage)))
	mux.Handle("GET /api/v1/me", a.auth.Require(http.HandlerFunc(a.me)))
	mux.Handle("GET /api/v1/dashboard", a.auth.Require(a.require("view", http.HandlerFunc(a.dashboard))))
	mux.Handle("GET /api/v1/agents/{id}", a.auth.Require(a.require("view", http.HandlerFunc(a.agentDetail))))
	mux.Handle("PUT /api/v1/agents/{id}/policy", a.auth.Require(a.require("policy", http.HandlerFunc(a.policy))))
	mux.Handle("POST /api/v1/agents/{id}/policy/rollback/{revision}", a.auth.Require(a.require("policy", http.HandlerFunc(a.policyRollback))))
	mux.Handle("PUT /api/v1/policy/all", a.auth.Require(a.require("policy", http.HandlerFunc(a.policyAll))))
	mux.Handle("GET /api/v1/policy/global", a.auth.Require(a.require("view", http.HandlerFunc(a.globalPolicy))))
	mux.Handle("GET /api/v1/agents/{id}/policy/effective", a.auth.Require(a.require("view", http.HandlerFunc(a.effectivePolicy))))
	mux.Handle("DELETE /api/v1/agents/{id}/policy", a.auth.Require(a.require("policy", http.HandlerFunc(a.agentPolicyClear))))
	mux.Handle("DELETE /api/v1/farms/{id}/policy", a.auth.Require(a.require("policy", http.HandlerFunc(a.farmPolicyClear))))
	mux.Handle("POST /api/v1/policy/global/rollback/{revision}", a.auth.Require(a.require("policy", http.HandlerFunc(a.globalPolicyRollback))))
	mux.Handle("PUT /api/v1/farms/{id}/policy", a.auth.Require(a.require("policy", http.HandlerFunc(a.farmPolicy))))
	mux.Handle("POST /api/v1/farms/{id}/policy/rollback/{revision}", a.auth.Require(a.require("policy", http.HandlerFunc(a.farmPolicyRollback))))
	mux.Handle("PATCH /api/v1/agents/{id}/control", a.auth.Require(a.require("maintenance", http.HandlerFunc(a.agentControl))))
	mux.Handle("POST /api/v1/agents/{id}/sessions/{session}/action", a.auth.Require(a.require("session", http.HandlerFunc(a.sessionAction))))
	mux.Handle("POST /api/v1/agents/{id}/sessions/bulk", a.auth.Require(a.require("session", http.HandlerFunc(a.sessionBulkAction))))
	mux.Handle("POST /api/v1/agents/{id}/processes/{pid}/kill", a.auth.Require(a.require("process", http.HandlerFunc(a.processKill))))
	mux.Handle("GET /api/v1/audit", a.auth.Require(a.require("audit", http.HandlerFunc(a.audit))))
	mux.Handle("GET /api/v1/history", a.auth.Require(a.require("view", http.HandlerFunc(a.history))))
	mux.Handle("GET /api/v1/policy/history", a.auth.Require(a.require("policy", http.HandlerFunc(a.policyHistory))))
	mux.Handle("GET /api/v1/farms", a.auth.Require(a.require("view", http.HandlerFunc(a.farms))))
	mux.Handle("POST /api/v1/farms", a.auth.Require(a.require("manage", http.HandlerFunc(a.farmCreate))))
	mux.Handle("PUT /api/v1/farms/{id}", a.auth.Require(a.require("manage", http.HandlerFunc(a.farmUpdate))))
	mux.Handle("DELETE /api/v1/farms/{id}", a.auth.Require(a.require("manage", http.HandlerFunc(a.farmDelete))))
	mux.Handle("GET /api/v1/resources", a.auth.Require(a.require("view", http.HandlerFunc(a.resources))))
	mux.Handle("POST /api/v1/resources", a.auth.Require(a.require("manage", http.HandlerFunc(a.resourceCreate))))
	mux.Handle("PUT /api/v1/resources/{id}", a.auth.Require(a.require("manage", http.HandlerFunc(a.resourceUpdate))))
	mux.Handle("DELETE /api/v1/resources/{id}", a.auth.Require(a.require("manage", http.HandlerFunc(a.resourceDelete))))
	mux.Handle("GET /api/v1/alerts", a.auth.Require(a.require("view", http.HandlerFunc(a.alerts))))
	mux.Handle("GET /api/v1/leases", a.auth.Require(a.require("view", http.HandlerFunc(a.leases))))
	mux.Handle("GET /api/v1/access/sessions", a.auth.Require(a.require("manage", http.HandlerFunc(a.accessSessions))))
	mux.Handle("DELETE /api/v1/access/sessions/{id}", a.auth.Require(a.require("manage", http.HandlerFunc(a.accessSessionRevoke))))
	mux.Handle("GET /api/v1/access/identities", a.auth.Require(a.require("manage", http.HandlerFunc(a.identityBindings))))
	mux.Handle("DELETE /api/v1/access/identities", a.auth.Require(a.require("manage", http.HandlerFunc(a.identityBindingsReset))))
	mux.Handle("DELETE /api/v1/access/identities/{username}", a.auth.Require(a.require("manage", http.HandlerFunc(a.identityBindingRelease))))
	mux.Handle("GET /api/v1/admin/sessions", a.auth.Require(a.require("manage", http.HandlerFunc(a.adminSessions))))
	mux.Handle("DELETE /api/v1/admin/sessions/{id}", a.auth.Require(a.require("manage", http.HandlerFunc(a.adminSessionRevoke))))
	server := &http.Server{Addr: a.cfg.Listen, Handler: securityHeaders(csrfGuard(mux)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second}
	go a.monitor(ctx)
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(c)
		_ = a.store.close()
	}()
	log.Printf("master %s listening on %s (store=%s)", Version, a.cfg.Listen, a.store.kind())
	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (a *App) enroll(w http.ResponseWriter, r *http.Request) {
	var req model.EnrollRequest
	if err := httpx.DecodeJSON(r, &req, 64<<10); err != nil {
		httpx.Fail(w, r, 400, "invalid request body", err)
		return
	}
	if !constantEqual(req.EnrollmentToken, a.cfg.EnrollmentToken) || strings.TrimSpace(req.MachineID) == "" {
		httpx.Error(w, 401, "invalid enrollment")
		return
	}
	now := time.Now().UTC()
	token, id := randomToken(32), randomToken(16)
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	for oldID, rec := range a.store.data.Agents {
		if rec.MachineID == req.MachineID {
			id = oldID
			break
		}
	}
	rec := a.store.data.Agents[id]
	newEnrollment := rec.EnrolledAt.IsZero()
	rec.ID = id
	rec.Name = req.Name
	rec.MachineID = req.MachineID
	rec.TokenHash = hashToken(token)
	if newEnrollment {
		rec.EnrolledAt = now
	}
	if rec.Tags == nil {
		rec.Tags = map[string]string{}
	}
	if rec.MaintenanceMode == "" {
		rec.MaintenanceMode = "online"
	}
	action := "agent_reenroll"
	if newEnrollment {
		action = "agent_enroll"
	} else {
		// MachineGuid is readable by local users and the enrollment token is
		// shared, so a re-enrollment may be an impersonation attempt. Keep
		// the record but quarantine it from brokering until an admin sets the
		// server back online.
		rec.MaintenanceMode = "maintenance"
		action = "agent_reenroll_quarantined"
		log.Printf("agent %s (%s) re-enrolled; set to maintenance until approved by an admin", id, req.Name)
	}
	a.store.data.Agents[id] = rec
	a.store.appendAuditLocked(model.AuditEntry{Time: now, Actor: "agent-bootstrap", Action: action, Target: req.Name, Result: "success", Details: req.MachineID})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 200, model.EnrollResponse{AgentID: id, Token: token})
}

func (a *App) heartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-Agent-ID")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if id == "" || token == "" {
		httpx.Error(w, 401, "missing agent credentials")
		return
	}
	var snap model.AgentSnapshot
	if err := httpx.DecodeJSON(r, &snap, 8<<20); err != nil {
		httpx.Fail(w, r, 400, "invalid request body", err)
		return
	}
	if snap.ProtocolVersion != model.ProtocolVersion {
		httpx.Error(w, 409, "protocol version mismatch")
		return
	}
	now := time.Now().UTC()
	var notify []model.Alert
	a.store.mu.Lock()
	rec, ok := a.store.data.Agents[id]
	if !ok || !constantEqual(hashToken(token), rec.TokenHash) {
		a.store.mu.Unlock()
		httpx.Error(w, 401, "invalid agent credentials")
		return
	}
	previous := rec.Snapshot
	snap.AgentID = id
	rec.LastSeen = now
	rec.Snapshot = snap
	// The reported hostname becomes ${SESSIONGUARD_HOST}. A changed hostname
	// would silently redirect RDP connections, so quarantine the host first.
	if previous.Server.Hostname != "" && snap.Server.Hostname != "" && !strings.EqualFold(previous.Server.Hostname, snap.Server.Hostname) && rec.MaintenanceMode != "maintenance" {
		rec.MaintenanceMode = "maintenance"
		a.store.appendAuditLocked(model.AuditEntry{Time: now, Actor: "agent:" + rec.Name, Action: "agent_hostname_changed_quarantined", Target: rec.Name, Result: "warning", Details: previous.Server.Hostname + " -> " + snap.Server.Hostname})
		log.Printf("agent %s hostname changed %q -> %q; set to maintenance until approved by an admin", id, previous.Server.Hostname, snap.Server.Hostname)
	}
	if snap.Server.Hostname != "" {
		rec.Name = snap.Server.Hostname
	}
	a.recordSessionHistoryLocked(rec, previous, snap, now)
	results := map[string]model.CommandResult{}
	for _, res := range snap.CommandResults {
		if res.ID != "" {
			results[res.ID] = res
		}
	}
	pending := make([]model.SessionCommand, 0, len(rec.PendingCommands))
	for _, cmd := range rec.PendingCommands {
		if result, found := results[cmd.ID]; found {
			status, details := "success", ""
			if !result.Success {
				status = "error"
				details = result.Error
			}
			a.store.appendAuditLocked(model.AuditEntry{Time: now, Actor: "agent:" + rec.Name, Action: "command_result:" + cmd.Action, Target: commandTarget(rec, cmd), Result: status, Details: details})
			continue
		}
		if !cmd.ExpiresAt.IsZero() && now.After(cmd.ExpiresAt) {
			a.store.appendAuditLocked(model.AuditEntry{Time: now, Actor: "system", Action: "command_expired:" + cmd.Action, Target: commandTarget(rec, cmd), Result: "expired"})
			continue
		}
		pending = append(pending, cmd)
	}
	rec.PendingCommands = pending
	if rec.RestartWhenDrained && userSessionCount(snap.Sessions) == 0 && !hasPendingAction(rec.PendingCommands, "restart_server") {
		cmd := model.SessionCommand{ID: randomToken(12), Action: "restart_server", RequestedBy: "system:drain", Message: "SessionGuard restart after drain", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
		rec.PendingCommands = append(rec.PendingCommands, cmd)
		rec.RestartWhenDrained = false
		a.store.appendAuditLocked(model.AuditEntry{Time: now, Actor: "system", Action: "restart_when_drained", Target: rec.Name, Result: "queued"})
	}
	a.store.data.Agents[id] = rec
	notify = a.evaluateAgentAlertsLocked(rec, now)
	desired := a.effectivePolicyLocked(rec)
	var sendPolicy *model.Policy
	if desired != nil && desired.Revision != snap.PolicyRevision {
		cp := *desired
		sendPolicy = &cp
	}
	desiredRemoteApps := a.desiredRemoteAppsLocked(id, rec)
	commands := append([]model.SessionCommand(nil), rec.PendingCommands...)
	if err := a.store.saveLocked(); err != nil {
		a.store.mu.Unlock()
		httpx.InternalError(w, r, err)
		return
	}
	a.store.mu.Unlock()
	for _, al := range notify {
		a.notifyAlert(al)
	}
	httpx.JSON(w, 200, model.HeartbeatResponse{DesiredPolicy: sendPolicy, DesiredRemoteApps: desiredRemoteApps, Commands: commands, ServerTime: now})
}

func (a *App) recordSessionHistoryLocked(rec model.AgentRecord, old, new model.AgentSnapshot, now time.Time) {
	om := map[uint32]model.Session{}
	nm := map[uint32]model.Session{}
	for _, s := range old.Sessions {
		if s.User != "" {
			om[s.ID] = s
		}
	}
	for _, s := range new.Sessions {
		if s.User != "" {
			nm[s.ID] = s
		}
	}
	for id, s := range nm {
		prev, exists := om[id]
		event := ""
		if !exists {
			event = "logon"
		} else if prev.State != s.State {
			event = strings.ToLower(s.State)
		}
		if event != "" {
			a.store.appendHistoryLocked(model.SessionHistoryEvent{Time: now, AgentID: rec.ID, Hostname: rec.Name, SessionID: id, User: displaySessionUser(s), SID: s.SID, Event: event, State: s.State, ClientName: s.ClientName}, a.cfg.HistoryLimit)
		}
	}
	for id, s := range om {
		if _, exists := nm[id]; !exists {
			a.store.appendHistoryLocked(model.SessionHistoryEvent{Time: now, AgentID: rec.ID, Hostname: rec.Name, SessionID: id, User: displaySessionUser(s), SID: s.SID, Event: "logoff", State: s.State, ClientName: s.ClientName}, a.cfg.HistoryLimit)
		}
	}
	for id, telemetry := range new.Telemetry {
		oldTelemetry := old.Telemetry[id]
		if telemetry.ObservedLogonMS > 0 && oldTelemetry.ObservedLogonMS == 0 {
			details := fmt.Sprintf("logon=%dms restore=%dms", telemetry.ObservedLogonMS, telemetry.RestoreDurationMS)
			a.store.appendHistoryLocked(model.SessionHistoryEvent{Time: now, AgentID: rec.ID, Hostname: rec.Name, SessionID: id, User: telemetry.User, SID: telemetry.SID, Event: "logon_ready", State: "Active", Details: details}, a.cfg.HistoryLimit)
		}
	}
}

func (a *App) effectivePolicyLocked(rec model.AgentRecord) *model.Policy {
	_, p := a.effectivePolicySourceLocked(rec)
	return p
}

// effectivePolicySourceLocked returns the policy an agent receives and where it
// comes from: "agent" (server override), "farm:<id>", "global" or "none".
func (a *App) effectivePolicySourceLocked(rec model.AgentRecord) (string, *model.Policy) {
	if rec.DesiredPolicy != nil {
		return "agent", rec.DesiredPolicy
	}

	// Explicit per-agent farm assignments have precedence and keep their
	// configured order. This makes policy precedence predictable when an agent
	// intentionally belongs to multiple farms.
	for _, fid := range rec.FarmIDs {
		if f, ok := a.store.data.Farms[fid]; ok && f.Enabled && f.Policy != nil {
			return "farm:" + fid, f.Policy
		}
	}

	// Farms may also select agents centrally through AgentIDs or RequiredTags.
	// Evaluate these deterministically so a server receives the same policy
	// after every master restart.
	ids := make([]string, 0, len(a.store.data.Farms))
	for id := range a.store.data.Farms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, fid := range ids {
		f := a.store.data.Farms[fid]
		if !f.Enabled || f.Policy == nil {
			continue
		}
		if contains(f.AgentIDs, rec.ID) || (len(f.RequiredTags) > 0 && tagsMatch(rec.Tags, f.RequiredTags)) {
			return "farm:" + fid, f.Policy
		}
	}
	if a.store.data.GlobalPolicy != nil {
		return "global", a.store.data.GlobalPolicy
	}
	return "none", nil
}

func (a *App) brokerResolve(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.Broker.Enabled {
		httpx.Error(w, 404, "broker disabled")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !constantEqual(token, a.cfg.Broker.APIKey) {
		httpx.Error(w, 401, "invalid broker credential")
		return
	}
	var req model.BrokerRequest
	if err := httpx.DecodeJSON(r, &req, 64<<10); err != nil {
		httpx.Fail(w, r, 400, "invalid request body", err)
		return
	}
	if strings.TrimSpace(req.Username) == "" {
		httpx.Error(w, 400, "username is required")
		return
	}
	resp, err := a.resolveBroker(req)
	if err != nil {
		httpx.Fail(w, r, 503, "no session host is currently available", err)
		return
	}
	httpx.JSON(w, 200, resp)
}

func (a *App) brokerTokens(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.Broker.Enabled {
		httpx.Error(w, 404, "broker disabled")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !constantEqual(token, a.cfg.Broker.APIKey) {
		httpx.Error(w, 401, "invalid broker credential")
		return
	}
	if err := r.ParseForm(); err != nil {
		httpx.Error(w, 400, "invalid form")
		return
	}
	req := model.BrokerRequest{
		Username:       r.FormValue("username"),
		ConnectionID:   r.FormValue("connection_id"),
		ConnectionName: r.FormValue("connection_name"),
		ResourceID:     r.FormValue("resource_id"),
		FarmID:         r.FormValue("farm_id"),
	}
	if strings.TrimSpace(req.Username) == "" {
		httpx.Error(w, 400, "username is required")
		return
	}
	// The Guacamole extension decorates every connection. Only connections
	// explicitly mapped as SessionGuard Resources should therefore invoke the
	// broker. Returning an empty token set leaves unrelated/static Guacamole
	// connections untouched and prevents a broker outage from breaking them.
	if !a.hasMappedResource(req) {
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	resp, err := a.resolveBroker(req)
	if err != nil {
		httpx.Fail(w, r, 503, "no session host is currently available", err)
		return
	}
	values := url.Values{}
	for k, v := range resp.Tokens {
		values.Set(k, v)
	}
	values.Set("SESSIONGUARD_REASON", resp.Reason)
	values.Set("SESSIONGUARD_FARM_ID", resp.FarmID)
	w.Header().Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	_, _ = fmt.Fprint(w, values.Encode())
}

func (a *App) hasMappedResource(req model.BrokerRequest) bool {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	_, ok := a.findResourceLocked(req)
	return ok
}

func (a *App) findResourceLocked(req model.BrokerRequest) (*model.Resource, bool) {
	// Prefer stable explicit identifiers over names. Guacamole sends both
	// connection ID and name; deterministic precedence prevents a bad mapping
	// from being selected according to Go map iteration order.
	if req.ResourceID != "" {
		if res, ok := a.store.data.Resources[req.ResourceID]; ok && res.Enabled {
			cp := res
			return &cp, true
		}
	}
	if req.ConnectionID != "" {
		for _, res := range a.store.data.Resources {
			if res.Enabled && res.GuacamoleConnectionID != "" && res.GuacamoleConnectionID == req.ConnectionID {
				cp := res
				return &cp, true
			}
		}
	}
	if req.ConnectionName != "" {
		for _, res := range a.store.data.Resources {
			if res.Enabled && res.GuacamoleConnectionName != "" && strings.EqualFold(res.GuacamoleConnectionName, req.ConnectionName) {
				cp := res
				return &cp, true
			}
		}
	}
	return nil, false
}

func (a *App) resolveBroker(req model.BrokerRequest) (model.BrokerResponse, error) {
	now := time.Now().UTC()
	userKey := normalizeUser(req.Username)
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	resource, _ := a.findResourceLocked(req)
	farmID := req.FarmID
	if resource != nil {
		farmID = resource.FarmID
	}
	if farmID == "" {
		farmID = a.cfg.Broker.DefaultFarmID
	}
	if farmID != "" {
		if f, ok := a.store.data.Farms[farmID]; !ok || !f.Enabled {
			return model.BrokerResponse{}, fmt.Errorf("farm %q is not available", farmID)
		}
	}
	leaseKey := a.brokerLeaseKey(userKey, farmID, resourceID(resource))

	// Existing RDS session wins for the requested farm, including on a draining host.
	// Maintenance hosts are never selected. This provides Citrix-like reconnect affinity.
	if a.cfg.Broker.ReconnectExisting {
		for id, rec := range a.store.data.Agents {
			if rec.MaintenanceMode == "maintenance" || !agentOnline(rec, now, a.cfg.OfflineAfterSeconds) || !a.agentInFarmLocked(id, rec, farmID) || !agentResourceReady(rec, resource) {
				continue
			}
			for _, sess := range rec.Snapshot.Sessions {
				if brokerSessionState(sess.State) && sessionMatchesUser(sess, userKey) {
					lease := a.putLeaseLocked(leaseKey, userKey, id, farmID, resourceID(resource), "existing-session", now)
					if err := a.store.saveLocked(); err != nil {
						return model.BrokerResponse{}, err
					}
					return brokerResponse(rec, farmID, resource, true, "existing-session", lease), nil
				}
			}
		}
	}
	if lease, ok := a.store.data.Leases[leaseKey]; ok && now.Before(lease.ExpiresAt) {
		if rec, found := a.store.data.Agents[lease.AgentID]; found && rec.MaintenanceMode != "maintenance" && agentOnline(rec, now, a.cfg.OfflineAfterSeconds) && a.agentInFarmLocked(lease.AgentID, rec, farmID) && agentResourceReady(rec, resource) {
			lease.ExpiresAt = now.Add(time.Duration(a.cfg.Broker.LeaseSeconds) * time.Second)
			a.store.data.Leases[leaseKey] = lease
			if err := a.store.saveLocked(); err != nil {
				return model.BrokerResponse{}, err
			}
			return brokerResponse(rec, farmID, resource, true, "existing-lease", lease), nil
		}
	}
	candidates := a.farmCandidatesLocked(farmID, resource, now)
	if len(candidates) == 0 {
		return model.BrokerResponse{}, fmt.Errorf("no healthy online server is available for farm %q", farmID)
	}
	sort.Slice(candidates, func(i, j int) bool { return brokerScore(candidates[i]) > brokerScore(candidates[j]) })
	chosen := candidates[0]
	lease := a.putLeaseLocked(leaseKey, userKey, chosen.ID, farmID, resourceID(resource), "load-balance", now)
	if err := a.store.saveLocked(); err != nil {
		return model.BrokerResponse{}, err
	}
	return brokerResponse(chosen, farmID, resource, false, "load-balance", lease), nil
}

func (a *App) brokerLeaseKey(user, farm, resource string) string {
	if a.cfg.Broker.SingleSession {
		return user
	}
	return user + "|" + farm + "|" + resource
}

func brokerSessionState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "active", "connected", "disconnected":
		return true
	default:
		return false
	}
}

func (a *App) agentInFarmLocked(id string, rec model.AgentRecord, farmID string) bool {
	if farmID == "" {
		return true
	}
	f, ok := a.store.data.Farms[farmID]
	if !ok || !f.Enabled {
		return false
	}
	if contains(f.AgentIDs, id) || contains(rec.FarmIDs, farmID) {
		return true
	}
	return len(f.RequiredTags) > 0 && tagsMatch(rec.Tags, f.RequiredTags)
}

func (a *App) farmCandidatesLocked(farmID string, resource *model.Resource, now time.Time) []model.AgentRecord {
	if farmID != "" {
		if f, ok := a.store.data.Farms[farmID]; !ok || !f.Enabled {
			return nil
		}
	}
	out := []model.AgentRecord{}
	for id, rec := range a.store.data.Agents {
		if rec.MaintenanceMode != "online" || !agentOnline(rec, now, a.cfg.OfflineAfterSeconds) || rec.Snapshot.Health.Score < a.cfg.Broker.MinHealthScore {
			continue
		}
		if !a.agentInFarmLocked(id, rec, farmID) {
			continue
		}
		if !agentResourceReady(rec, resource) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func (a *App) putLeaseLocked(key, user, agent, farm, res, reason string, now time.Time) model.UserLease {
	l := model.UserLease{UserKey: user, AgentID: agent, FarmID: farm, ResourceID: res, CreatedAt: now, ExpiresAt: now.Add(time.Duration(a.cfg.Broker.LeaseSeconds) * time.Second), Reason: reason}
	a.store.data.Leases[key] = l
	return l
}

func brokerResponse(rec model.AgentRecord, farm string, res *model.Resource, reconnect bool, reason string, lease model.UserLease) model.BrokerResponse {
	tokens := map[string]string{"SESSIONGUARD_HOST": rec.Snapshot.Server.Hostname, "SESSIONGUARD_AGENT_ID": rec.ID}
	rid := ""
	if res != nil {
		rid = res.ID
		tokens["SESSIONGUARD_RESOURCE_ID"] = res.ID
		tokens["SESSIONGUARD_REMOTE_APP"] = res.RemoteApp
		tokens["SESSIONGUARD_REMOTE_APP_DIR"] = res.RemoteAppDir
		remoteArgs := res.RemoteAppArgs
		if res.Kind == "remoteapp" && res.ManageRemoteApp {
			switch res.RemoteAppCommandLine {
			case 0:
				remoteArgs = ""
			case 2:
				remoteArgs = res.RemoteAppRequiredArgs
			}
		}
		tokens["SESSIONGUARD_REMOTE_APP_ARGS"] = remoteArgs
	}
	return model.BrokerResponse{AgentID: rec.ID, Hostname: rec.Snapshot.Server.Hostname, FarmID: farm, ResourceID: rid, Reconnect: reconnect, Reason: reason, HealthScore: rec.Snapshot.Health.Score, Tokens: tokens, LeaseExpires: lease.ExpiresAt}
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	recs := a.store.all()
	sort.Slice(recs, func(i, j int) bool { return strings.ToLower(recs[i].Name) < strings.ToLower(recs[j].Name) })
	now := time.Now().UTC()
	type row struct {
		model.AgentRecord
		Online        bool    `json:"online"`
		Active        int     `json:"active_sessions"`
		Disconnected  int     `json:"disconnected_sessions"`
		Total         int     `json:"total_sessions"`
		MemoryPercent float64 `json:"memory_percent"`
		BrokerScore   float64 `json:"broker_score"`
	}
	out := make([]row, 0, len(recs))
	for _, rec := range recs {
		active, disc := sessionCounts(rec.Snapshot.Sessions)
		mem := 0.0
		if rec.Snapshot.Server.MemoryTotal > 0 {
			mem = float64(rec.Snapshot.Server.MemoryTotal-rec.Snapshot.Server.MemoryAvailable) * 100 / float64(rec.Snapshot.Server.MemoryTotal)
		}
		out = append(out, row{AgentRecord: rec, Online: agentOnline(rec, now, a.cfg.OfflineAfterSeconds), Active: active, Disconnected: disc, Total: active + disc, MemoryPercent: mem, BrokerScore: brokerScore(rec)})
	}
	a.store.mu.RLock()
	farms := len(a.store.data.Farms)
	resources := len(a.store.data.Resources)
	alerts := 0
	accessSessions := 0
	for _, x := range a.store.data.Alerts {
		if x.Active {
			alerts++
		}
	}
	for _, sess := range a.store.data.AuthSessions {
		if sess.ExpiresAt.IsZero() || now.Before(sess.ExpiresAt) {
			accessSessions++
		}
	}
	a.store.mu.RUnlock()
	httpx.JSON(w, 200, map[string]any{"agents": out, "server_time": now, "farms": farms, "resources": resources, "active_alerts": alerts, "access_sessions": accessSessions, "store": a.store.kind()})
}
func (a *App) agentDetail(w http.ResponseWriter, r *http.Request) {
	rec, ok := a.store.get(r.PathValue("id"))
	if !ok {
		httpx.Error(w, 404, "agent not found")
		return
	}
	httpx.JSON(w, 200, rec)
}

func (a *App) policy(w http.ResponseWriter, r *http.Request) {
	if !httpx.SameOrigin(r) {
		httpx.Error(w, 403, "cross-origin request rejected")
		return
	}
	id := r.PathValue("id")
	var p model.Policy
	if err := httpx.DecodeJSON(r, &p, 2<<20); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	config.NormalizePolicy(&p)
	if err := config.ValidatePolicy(p); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	p.Revision = randomToken(12)
	p.UpdatedAt = time.Now().UTC()
	actor := requestActor(r)
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	rec, ok := a.store.data.Agents[id]
	if !ok {
		httpx.Error(w, 404, "agent not found")
		return
	}
	rec.DesiredPolicy = &p
	a.store.data.Agents[id] = rec
	a.recordPolicyVersionLocked("agent:"+id, p, actor)
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: actor, Action: "policy_update", Target: rec.Name, Result: "queued", Details: p.Revision})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 200, p)
}
func (a *App) policyAll(w http.ResponseWriter, r *http.Request) {
	if !httpx.SameOrigin(r) {
		httpx.Error(w, 403, "cross-origin request rejected")
		return
	}
	var p model.Policy
	if err := httpx.DecodeJSON(r, &p, 2<<20); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	config.NormalizePolicy(&p)
	if err := config.ValidatePolicy(p); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	p.Revision = randomToken(12)
	p.UpdatedAt = time.Now().UTC()
	actor := requestActor(r)
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	a.store.data.GlobalPolicy = &p
	a.recordPolicyVersionLocked("global", p, actor)
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: actor, Action: "global_policy_update", Target: "all agents", Result: "queued", Details: p.Revision})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 200, p)
}
func (a *App) farmPolicy(w http.ResponseWriter, r *http.Request) {
	if !httpx.SameOrigin(r) {
		httpx.Error(w, 403, "cross-origin request rejected")
		return
	}
	var p model.Policy
	if err := httpx.DecodeJSON(r, &p, 2<<20); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	config.NormalizePolicy(&p)
	if err := config.ValidatePolicy(p); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	p.Revision = randomToken(12)
	p.UpdatedAt = time.Now().UTC()
	id, actor := r.PathValue("id"), requestActor(r)
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	f, ok := a.store.data.Farms[id]
	if !ok {
		httpx.Error(w, 404, "farm not found")
		return
	}
	f.Policy = &p
	a.store.data.Farms[id] = f
	a.recordPolicyVersionLocked("farm:"+id, p, actor)
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: actor, Action: "farm_policy_update", Target: f.Name, Result: "queued", Details: p.Revision})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 200, p)
}

func (a *App) globalPolicyRollback(w http.ResponseWriter, r *http.Request) {
	a.rollbackPolicyTarget(w, r, "global", "")
}
func (a *App) farmPolicyRollback(w http.ResponseWriter, r *http.Request) {
	a.rollbackPolicyTarget(w, r, "farm:"+r.PathValue("id"), r.PathValue("id"))
}
func (a *App) rollbackPolicyTarget(w http.ResponseWriter, r *http.Request, target, farmID string) {
	if !httpx.SameOrigin(r) {
		httpx.Error(w, 403, "cross-origin request rejected")
		return
	}
	rev, actor := r.PathValue("revision"), requestActor(r)
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	var found *model.Policy
	for i := len(a.store.data.PolicyHistory) - 1; i >= 0; i-- {
		v := a.store.data.PolicyHistory[i]
		if v.Target == target && v.Revision == rev {
			p := v.Policy
			found = &p
			break
		}
	}
	if found == nil {
		httpx.Error(w, 404, "revision not found")
		return
	}
	found.Revision = randomToken(12)
	found.UpdatedAt = time.Now().UTC()
	name := "global"
	if target == "global" {
		a.store.data.GlobalPolicy = found
	} else {
		f, ok := a.store.data.Farms[farmID]
		if !ok {
			httpx.Error(w, 404, "farm not found")
			return
		}
		f.Policy = found
		a.store.data.Farms[farmID] = f
		name = f.Name
	}
	a.recordPolicyVersionLocked(target, *found, actor)
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: actor, Action: "policy_rollback", Target: name, Result: "queued", Details: rev + " -> " + found.Revision})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 200, found)
}

func (a *App) recordPolicyVersionLocked(target string, p model.Policy, actor string) {
	a.store.data.PolicyHistory = append(a.store.data.PolicyHistory, model.PolicyVersion{ID: randomToken(10), Target: target, Revision: p.Revision, CreatedAt: time.Now().UTC(), Actor: actor, Policy: p})
	if len(a.store.data.PolicyHistory) > 500 {
		a.store.data.PolicyHistory = append([]model.PolicyVersion(nil), a.store.data.PolicyHistory[len(a.store.data.PolicyHistory)-500:]...)
	}
}
func (a *App) policyRollback(w http.ResponseWriter, r *http.Request) {
	id, rev := r.PathValue("id"), r.PathValue("revision")
	actor := requestActor(r)
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	rec, ok := a.store.data.Agents[id]
	if !ok {
		httpx.Error(w, 404, "agent not found")
		return
	}
	var found *model.Policy
	for i := len(a.store.data.PolicyHistory) - 1; i >= 0; i-- {
		v := a.store.data.PolicyHistory[i]
		if v.Target == "agent:"+id && v.Revision == rev {
			p := v.Policy
			found = &p
			break
		}
	}
	if found == nil {
		httpx.Error(w, 404, "revision not found")
		return
	}
	found.Revision = randomToken(12)
	found.UpdatedAt = time.Now().UTC()
	rec.DesiredPolicy = found
	a.store.data.Agents[id] = rec
	a.recordPolicyVersionLocked("agent:"+id, *found, actor)
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: actor, Action: "policy_rollback", Target: rec.Name, Result: "queued", Details: rev + " -> " + found.Revision})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 200, found)
}
func (a *App) policyHistory(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	httpx.JSON(w, 200, map[string]any{"history": append([]model.PolicyVersion(nil), a.store.data.PolicyHistory...)})
}

func (a *App) agentControl(w http.ResponseWriter, r *http.Request) {
	if !httpx.SameOrigin(r) {
		httpx.Error(w, 403, "cross-origin request rejected")
		return
	}
	var req struct {
		Mode               string            `json:"mode"`
		RestartWhenDrained bool              `json:"restart_when_drained"`
		Tags               map[string]string `json:"tags"`
		FarmIDs            []string          `json:"farm_ids"`
	}
	if err := httpx.DecodeJSON(r, &req, 128<<10); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode != "" && mode != "online" && mode != "drain" && mode != "maintenance" {
		httpx.Error(w, 400, "mode must be online, drain or maintenance")
		return
	}
	id := r.PathValue("id")
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	rec, ok := a.store.data.Agents[id]
	if !ok {
		httpx.Error(w, 404, "agent not found")
		return
	}
	if mode != "" {
		rec.MaintenanceMode = mode
	}
	rec.RestartWhenDrained = req.RestartWhenDrained
	if req.Tags != nil {
		rec.Tags = req.Tags
	}
	if req.FarmIDs != nil {
		rec.FarmIDs = req.FarmIDs
	}
	a.store.data.Agents[id] = rec
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: requestActor(r), Action: "agent_control", Target: rec.Name, Result: "success", Details: fmt.Sprintf("mode=%s restart_when_drained=%v", rec.MaintenanceMode, rec.RestartWhenDrained)})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	rec.TokenHash = ""
	httpx.JSON(w, 200, rec)
}

func (a *App) sessionAction(w http.ResponseWriter, r *http.Request) {
	if !httpx.SameOrigin(r) {
		httpx.Error(w, 403, "cross-origin request rejected")
		return
	}
	id := r.PathValue("id")
	session64, err := strconv.ParseUint(r.PathValue("session"), 10, 32)
	if err != nil {
		httpx.Error(w, 400, "invalid session id")
		return
	}
	var req model.SessionActionRequest
	if err := httpx.DecodeJSON(r, &req, 64<<10); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "logoff" && action != "disconnect" && action != "message" {
		httpx.Error(w, 400, "invalid action")
		return
	}
	if action == "logoff" && !a.hasPermission(r, "session_logoff") {
		httpx.Error(w, 403, "role may not log off sessions")
		return
	}
	if action == "message" && strings.TrimSpace(req.Message) == "" {
		httpx.Error(w, 400, "message is required")
		return
	}
	cmd := model.SessionCommand{ID: randomToken(12), Action: action, SessionID: uint32(session64), Title: req.Title, Message: req.Message, RequestedBy: requestActor(r), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(2 * time.Minute)}
	a.queueCommand(w, r, id, cmd)
}
func (a *App) sessionBulkAction(w http.ResponseWriter, r *http.Request) {
	if !httpx.SameOrigin(r) {
		httpx.Error(w, 403, "cross-origin request rejected")
		return
	}
	var req struct {
		Action  string `json:"action"`
		Scope   string `json:"scope"`
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	if err := httpx.DecodeJSON(r, &req, 64<<10); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	scope := strings.ToLower(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = "all"
	}
	if action != "logoff" && action != "disconnect" && action != "message" {
		httpx.Error(w, 400, "invalid action")
		return
	}
	if scope != "all" && scope != "disconnected" && scope != "active" {
		httpx.Error(w, 400, "scope must be all, active or disconnected")
		return
	}
	if action == "logoff" && !a.hasPermission(r, "session_logoff") {
		httpx.Error(w, 403, "role may not log off sessions")
		return
	}
	if action == "message" && strings.TrimSpace(req.Message) == "" {
		httpx.Error(w, 400, "message is required")
		return
	}
	id := r.PathValue("id")
	now := time.Now().UTC()
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	rec, ok := a.store.data.Agents[id]
	if !ok {
		httpx.Error(w, 404, "agent not found")
		return
	}
	policy := rec.Snapshot.Policy
	if p := a.effectivePolicyLocked(rec); p != nil {
		policy = *p
	}
	if !policy.Sessions.ControlEnabled {
		httpx.Error(w, 409, "session control is disabled by policy")
		return
	}
	added := 0
	for _, sess := range rec.Snapshot.Sessions {
		if sess.User == "" {
			continue
		}
		state := strings.ToLower(sess.State)
		if scope == "disconnected" && state != "disconnected" {
			continue
		}
		if scope == "active" && state != "active" && state != "connected" {
			continue
		}
		if len(rec.PendingCommands) >= 100 {
			break
		}
		rec.PendingCommands = append(rec.PendingCommands, model.SessionCommand{ID: randomToken(12), Action: action, SessionID: sess.ID, Title: req.Title, Message: req.Message, RequestedBy: requestActor(r), CreatedAt: now, ExpiresAt: now.Add(2 * time.Minute)})
		added++
	}
	a.store.data.Agents[id] = rec
	a.store.appendAuditLocked(model.AuditEntry{Time: now, Actor: requestActor(r), Action: "bulk:" + action, Target: rec.Name, Result: "queued", Details: fmt.Sprintf("scope=%s sessions=%d", scope, added)})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 202, map[string]any{"queued": added})
}

func (a *App) processKill(w http.ResponseWriter, r *http.Request) {
	if !httpx.SameOrigin(r) {
		httpx.Error(w, 403, "cross-origin request rejected")
		return
	}
	pid64, err := strconv.ParseUint(r.PathValue("pid"), 10, 32)
	if err != nil {
		httpx.Error(w, 400, "invalid pid")
		return
	}
	agentID := r.PathValue("id")
	rec, ok := a.store.get(agentID)
	if !ok {
		httpx.Error(w, 404, "agent not found")
		return
	}
	pid := uint32(pid64)
	var sessionID uint32
	for _, process := range rec.Snapshot.Processes {
		if process.PID == pid {
			sessionID = process.SessionID
			break
		}
	}
	if sessionID == 0 {
		httpx.Error(w, 404, "process is no longer present in a user session")
		return
	}
	cmd := model.SessionCommand{ID: randomToken(12), Action: "kill_process", SessionID: sessionID, PID: pid, RequestedBy: requestActor(r), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(2 * time.Minute)}
	a.queueCommand(w, r, agentID, cmd)
}
func (a *App) queueCommand(w http.ResponseWriter, r *http.Request, id string, cmd model.SessionCommand) {
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	rec, ok := a.store.data.Agents[id]
	if !ok {
		httpx.Error(w, 404, "agent not found")
		return
	}
	policy := rec.Snapshot.Policy
	if p := a.effectivePolicyLocked(rec); p != nil {
		policy = *p
	}
	if !policy.Sessions.ControlEnabled {
		httpx.Error(w, 409, "session control is disabled by policy")
		return
	}
	if len(rec.PendingCommands) >= 100 {
		httpx.Error(w, 429, "too many pending commands")
		return
	}
	rec.PendingCommands = append(rec.PendingCommands, cmd)
	a.store.data.Agents[id] = rec
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: cmd.RequestedBy, Action: "command:" + cmd.Action, Target: commandTarget(rec, cmd), Result: "queued"})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 202, cmd)
}

func (a *App) farms(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	out := make([]model.Farm, 0, len(a.store.data.Farms))
	for _, f := range a.store.data.Farms {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	httpx.JSON(w, 200, map[string]any{"farms": out})
}
func (a *App) farmCreate(w http.ResponseWriter, r *http.Request) {
	var f model.Farm
	if err := httpx.DecodeJSON(r, &f, 256<<10); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	if f.ID == "" {
		f.ID = randomToken(8)
	}
	a.saveFarm(w, r, f, false)
}
func (a *App) farmUpdate(w http.ResponseWriter, r *http.Request) {
	var f model.Farm
	if err := httpx.DecodeJSON(r, &f, 256<<10); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	f.ID = r.PathValue("id")
	a.saveFarm(w, r, f, true)
}
func (a *App) saveFarm(w http.ResponseWriter, r *http.Request, f model.Farm, mustExist bool) {
	if strings.TrimSpace(f.Name) == "" {
		httpx.Error(w, 400, "farm name is required")
		return
	}
	if f.RequiredTags == nil {
		f.RequiredTags = map[string]string{}
	}
	if f.Policy != nil {
		config.NormalizePolicy(f.Policy)
		if err := config.ValidatePolicy(*f.Policy); err != nil {
			httpx.Error(w, 400, err.Error())
			return
		}
		if f.Policy.Revision == "" {
			f.Policy.Revision = randomToken(12)
			f.Policy.UpdatedAt = time.Now().UTC()
		}
	}
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	old, exists := a.store.data.Farms[f.ID]
	if mustExist && !exists {
		httpx.Error(w, 404, "farm not found")
		return
	}
	// Farm policies are managed via PUT/DELETE /api/v1/farms/{id}/policy (with
	// version history). An update without a policy must not silently drop it.
	if exists && f.Policy == nil {
		f.Policy = old.Policy
	}
	a.store.data.Farms[f.ID] = f
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: requestActor(r), Action: "farm_save", Target: f.Name, Result: "success"})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 200, f)
}
func (a *App) farmDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	f, ok := a.store.data.Farms[id]
	if !ok {
		httpx.Error(w, 404, "farm not found")
		return
	}
	delete(a.store.data.Farms, id)
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: requestActor(r), Action: "farm_delete", Target: f.Name, Result: "success"})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (a *App) resources(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	out := make([]model.Resource, 0, len(a.store.data.Resources))
	for _, x := range a.store.data.Resources {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	httpx.JSON(w, 200, map[string]any{"resources": out})
}
func (a *App) resourceCreate(w http.ResponseWriter, r *http.Request) {
	var x model.Resource
	if err := httpx.DecodeJSON(r, &x, 128<<10); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	if x.ID == "" {
		x.ID = randomToken(8)
	}
	a.saveResource(w, r, x, false)
}
func (a *App) resourceUpdate(w http.ResponseWriter, r *http.Request) {
	var x model.Resource
	if err := httpx.DecodeJSON(r, &x, 128<<10); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	x.ID = r.PathValue("id")
	a.saveResource(w, r, x, true)
}
func (a *App) saveResource(w http.ResponseWriter, r *http.Request, x model.Resource, mustExist bool) {
	if strings.TrimSpace(x.Name) == "" || strings.TrimSpace(x.FarmID) == "" {
		httpx.Error(w, 400, "resource name and farm_id are required")
		return
	}
	if x.Kind != "desktop" && x.Kind != "remoteapp" {
		httpx.Error(w, 400, "kind must be desktop or remoteapp")
		return
	}
	if x.Kind == "remoteapp" && strings.TrimSpace(x.RemoteApp) == "" {
		httpx.Error(w, 400, "remote_app is required for remoteapp resources")
		return
	}
	if x.Kind == "desktop" {
		x.RemoteApp = ""
		x.RemoteAppDir = ""
		x.RemoteAppArgs = ""
		x.ManageRemoteApp = false
		x.RemoteAppPath = ""
		x.RemoteAppIconPath = ""
		x.RemoteAppIconIndex = 0
		x.RemoteAppCommandLine = 0
		x.RemoteAppRequiredArgs = ""
		x.RemoteAppShowInPortal = false
	}
	if err := normalizeMultiMonitor(&x); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	if x.Kind == "remoteapp" {
		alias := remoteAppAlias(x.RemoteApp)
		if !validRemoteAppAlias(alias) {
			httpx.Error(w, 400, "remote_app alias must contain only letters, numbers, dot, dash or underscore")
			return
		}
		x.RemoteApp = "||" + alias
		if x.ManageRemoteApp && strings.TrimSpace(x.RemoteAppPath) == "" {
			httpx.Error(w, 400, "remote_app_path is required when agent RemoteApp management is enabled")
			return
		}
		if x.RemoteAppCommandLine > 2 {
			httpx.Error(w, 400, "remote_app_command_line_setting must be 0, 1 or 2")
			return
		}
	}
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	if _, ok := a.store.data.Farms[x.FarmID]; !ok {
		httpx.Error(w, 400, "farm does not exist")
		return
	}
	_, exists := a.store.data.Resources[x.ID]
	if mustExist && !exists {
		httpx.Error(w, 404, "resource not found")
		return
	}
	if x.Enabled {
		for id, existing := range a.store.data.Resources {
			if id == x.ID || !existing.Enabled {
				continue
			}
			if x.GuacamoleConnectionID != "" && existing.GuacamoleConnectionID == x.GuacamoleConnectionID {
				httpx.Error(w, 409, "guacamole_connection_id is already mapped by another enabled resource")
				return
			}
			if x.GuacamoleConnectionName != "" && strings.EqualFold(existing.GuacamoleConnectionName, x.GuacamoleConnectionName) {
				httpx.Error(w, 409, "guacamole_connection_name is already mapped by another enabled resource")
				return
			}
			if x.Kind == "remoteapp" && x.ManageRemoteApp && existing.Kind == "remoteapp" && existing.ManageRemoteApp && strings.EqualFold(remoteAppAlias(existing.RemoteApp), remoteAppAlias(x.RemoteApp)) {
				httpx.Error(w, 409, "managed remote_app alias is already used by another enabled resource")
				return
			}
		}
	}
	a.store.data.Resources[x.ID] = x
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: requestActor(r), Action: "resource_save", Target: x.Name, Result: "success"})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	httpx.JSON(w, 200, x)
}
func (a *App) resourceDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	x, ok := a.store.data.Resources[id]
	if !ok {
		httpx.Error(w, 404, "resource not found")
		return
	}
	delete(a.store.data.Resources, id)
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: requestActor(r), Action: "resource_delete", Target: x.Name, Result: "success"})
	if err := a.store.saveLocked(); err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (a *App) history(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items := a.store.history(limit)
	user := normalizeUser(r.URL.Query().Get("user"))
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	if user != "" || agent != "" {
		filtered := make([]model.SessionHistoryEvent, 0, len(items))
		for _, x := range items {
			if user != "" && normalizeUser(x.User) != user {
				continue
			}
			if agent != "" && x.AgentID != agent {
				continue
			}
			filtered = append(filtered, x)
		}
		items = filtered
	}
	httpx.JSON(w, 200, map[string]any{"history": items})
}
func (a *App) audit(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, 200, map[string]any{"audit": a.store.audit(500)})
}
func (a *App) alerts(w http.ResponseWriter, r *http.Request) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	out := make([]model.Alert, 0, len(a.store.data.Alerts))
	for _, x := range a.store.data.Alerts {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].LastSeenAt.After(out[j].LastSeenAt)
	})
	httpx.JSON(w, 200, map[string]any{"alerts": out})
}

func (a *App) accessSessions(w http.ResponseWriter, r *http.Request) {
	type publicSession struct {
		ID       string   `json:"id"`
		Subject  string   `json:"subject"`
		SID      string   `json:"sid,omitempty"`
		Username string   `json:"username"`
		Email    string   `json:"email,omitempty"`
		Name     string   `json:"name,omitempty"`
		Groups   []string `json:"groups,omitempty"`
		// GuacamoleGroups are the effective Guacamole user groups derived
		// from Groups via access_auth.guacamole_groups.
		GuacamoleGroups []string  `json:"guacamole_groups,omitempty"`
		CreatedAt       time.Time `json:"created_at"`
		ExpiresAt       time.Time `json:"expires_at"`
	}
	now := time.Now().UTC()
	a.store.mu.RLock()
	out := make([]publicSession, 0, len(a.store.data.AuthSessions))
	for _, sess := range a.store.data.AuthSessions {
		if !sess.ExpiresAt.IsZero() && !now.Before(sess.ExpiresAt) {
			continue
		}
		out = append(out, publicSession{ID: sess.ID, Subject: sess.Subject, SID: sess.SID, Username: sess.Username, Email: sess.Email, Name: sess.Name, Groups: append([]string(nil), sess.Groups...), GuacamoleGroups: auth.GuacamoleGroups(sess.Groups, a.cfg.AccessAuth.GuacamoleGroups), CreatedAt: sess.CreatedAt, ExpiresAt: sess.ExpiresAt})
	}
	a.store.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	httpx.JSON(w, 200, map[string]any{"sessions": out})
}

func (a *App) accessSessionRevoke(w http.ResponseWriter, r *http.Request) {
	if !httpx.SameOrigin(r) {
		httpx.Error(w, 403, "cross-origin request rejected")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		httpx.Error(w, 400, "session id is required")
		return
	}
	a.store.mu.Lock()
	var username string
	var hash string
	for h, sess := range a.store.data.AuthSessions {
		if sess.ID == id {
			hash, username = h, sess.Username
			break
		}
	}
	if hash == "" {
		a.store.mu.Unlock()
		httpx.Error(w, 404, "access session not found")
		return
	}
	delete(a.store.data.AuthSessions, hash)
	a.store.appendAuditLocked(model.AuditEntry{Time: time.Now().UTC(), Actor: requestActor(r), Action: "access_session_revoke", Target: username, Result: "success", Details: id})
	err := a.store.saveLocked()
	a.store.mu.Unlock()
	if err != nil {
		httpx.InternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) leases(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	a.store.mu.RLock()
	out := make([]model.UserLease, 0, len(a.store.data.Leases))
	for _, l := range a.store.data.Leases {
		if now.Before(l.ExpiresAt) {
			out = append(out, l)
		}
	}
	a.store.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	httpx.JSON(w, 200, map[string]any{"leases": out})
}

func (a *App) monitor(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			a.store.mu.Lock()
			leasesChanged := false
			for key, lease := range a.store.data.Leases {
				if now.After(lease.ExpiresAt) {
					delete(a.store.data.Leases, key)
					leasesChanged = true
				}
			}
			if !a.cfg.Alerts.Enabled {
				if leasesChanged {
					if err := a.store.saveLocked(); err != nil {
						log.Printf("master monitor persistence: %v", err)
					}
				}
				a.store.mu.Unlock()
				continue
			}
			changed := []model.Alert{}
			for id, rec := range a.store.data.Agents {
				if now.Sub(rec.LastSeen) > time.Duration(a.cfg.Alerts.OfflineSeconds)*time.Second {
					changed = append(changed, a.setAlertLocked("offline:"+id, rec, "critical", "agent_offline", fmt.Sprintf("Agent seit %s nicht erreichbar", now.Sub(rec.LastSeen).Round(time.Second)), true, now)...)
				} else {
					changed = append(changed, a.setAlertLocked("offline:"+id, rec, "critical", "agent_offline", "", false, now)...)
				}
			}
			if err := a.store.saveLocked(); err != nil {
				log.Printf("master monitor persistence: %v", err)
			}
			a.store.mu.Unlock()
			for _, al := range changed {
				a.notifyAlert(al)
			}
		}
	}
}
func (a *App) evaluateAgentAlertsLocked(rec model.AgentRecord, now time.Time) []model.Alert {
	if !a.cfg.Alerts.Enabled {
		return nil
	}
	changed := []model.Alert{}
	s := rec.Snapshot.Server
	mem := 0.0
	if s.MemoryTotal > 0 {
		mem = float64(s.MemoryTotal-s.MemoryAvailable) * 100 / float64(s.MemoryTotal)
	}
	checks := []struct {
		key, sev, typ, msg string
		active             bool
	}{{"cpu:" + rec.ID, "warning", "cpu_high", fmt.Sprintf("CPU %.1f%%", s.CPUPercent), s.CPUPercent >= float64(a.cfg.Alerts.CPUPercent)}, {"memory:" + rec.ID, "warning", "memory_high", fmt.Sprintf("RAM %.1f%%", mem), mem >= float64(a.cfg.Alerts.MemoryPercent)}, {"disk:" + rec.ID, "critical", "disk_low", fmt.Sprintf("Systemdisk %d GiB frei", s.DiskFree>>30), s.DiskTotal > 0 && s.DiskFree < uint64(a.cfg.Alerts.DiskFreeGB)<<30}, {"health:" + rec.ID, "critical", "health_low", fmt.Sprintf("Health Score %d", rec.Snapshot.Health.Score), rec.Snapshot.Health.Score < a.cfg.Alerts.HealthScore}}
	_, disc := sessionCounts(rec.Snapshot.Sessions)
	checks = append(checks, struct {
		key, sev, typ, msg string
		active             bool
	}{"disc:" + rec.ID, "warning", "disconnected_sessions", fmt.Sprintf("%d getrennte Sitzungen", disc), disc >= a.cfg.Alerts.DisconnectedSessions})
	maxLogonMS := int64(0)
	for _, telemetry := range rec.Snapshot.Telemetry {
		if telemetry.ObservedLogonMS > maxLogonMS {
			maxLogonMS = telemetry.ObservedLogonMS
		}
	}
	checks = append(checks, struct {
		key, sev, typ, msg string
		active             bool
	}{"logon:" + rec.ID, "warning", "logon_slow", fmt.Sprintf("Letzte gemessene Logon-Pipeline %.1f s", float64(maxLogonMS)/1000), maxLogonMS >= int64(a.cfg.Alerts.LogonDurationSeconds)*1000})
	fail := 0
	for _, st := range rec.Snapshot.ProfileStatus {
		if st.LastBackupError != "" || st.LastRestoreError != "" {
			fail++
		}
	}
	checks = append(checks, struct {
		key, sev, typ, msg string
		active             bool
	}{"profiles:" + rec.ID, "critical", "profile_failures", fmt.Sprintf("%d Profile mit Fehlerstatus", fail), fail >= a.cfg.Alerts.ProfileFailures})
	for _, c := range checks {
		changed = append(changed, a.setAlertLocked(c.key, rec, c.sev, c.typ, c.msg, c.active, now)...)
	}
	return changed
}
func (a *App) setAlertLocked(key string, rec model.AgentRecord, severity, typ, msg string, active bool, now time.Time) []model.Alert {
	al, exists := a.store.data.Alerts[key]
	if !exists {
		al = model.Alert{ID: randomToken(8), Key: key, AgentID: rec.ID, Hostname: rec.Name, Severity: severity, Type: typ, FirstSeenAt: now}
	}
	if active {
		was := al.Active
		al.Active = true
		al.LastSeenAt = now
		al.Message = msg
		al.Severity = severity
		if !was {
			al.FirstSeenAt = now
		}
		a.store.data.Alerts[key] = al
		if !was || now.Sub(al.LastNotifiedAt) >= time.Duration(a.cfg.Alerts.NotificationMinInterval)*time.Second {
			al.LastNotifiedAt = now
			a.store.data.Alerts[key] = al
			return []model.Alert{al}
		}
		return nil
	}
	if exists && al.Active {
		al.Active = false
		al.ResolvedAt = now
		al.LastSeenAt = now
		al.LastNotifiedAt = now
		a.store.data.Alerts[key] = al
		return []model.Alert{al}
	}
	return nil
}
func (a *App) notifyAlert(al model.Alert) {
	if !a.cfg.Alerts.Enabled || strings.TrimSpace(a.cfg.Alerts.WebhookURL) == "" {
		return
	}
	body, _ := json.Marshal(al)
	req, err := http.NewRequest(http.MethodPost, a.cfg.Alerts.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		log.Printf("alert webhook: %v", err)
		return
	}
	_ = resp.Body.Close()
}

func (a *App) metrics(w http.ResponseWriter, r *http.Request) {
	recs := a.store.all()
	now := time.Now().UTC()
	online, active, disconnected, profileJobs, cleanupJobs := 0, 0, 0, 0, 0
	healthTotal := 0
	for _, rec := range recs {
		if agentOnline(rec, now, a.cfg.OfflineAfterSeconds) {
			online++
		}
		aa, dd := sessionCounts(rec.Snapshot.Sessions)
		active += aa
		disconnected += dd
		profileJobs += len(rec.Snapshot.ProfileJobs)
		cleanupJobs += len(rec.Snapshot.PendingCleanup)
		healthTotal += rec.Snapshot.Health.Score
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "sessionguard_master_up 1\nsessionguard_agents_total %d\nsessionguard_agents_online %d\nsessionguard_sessions_active %d\nsessionguard_sessions_disconnected %d\nsessionguard_profile_jobs %d\nsessionguard_cleanup_jobs %d\nsessionguard_health_score_sum %d\n", len(recs), online, active, disconnected, profileJobs, cleanupJobs, healthTotal)
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r)
	httpx.JSON(w, 200, map[string]any{"user": u, "roles": a.roles(r), "permissions": a.permissions(r)})
}

var rolePermissions = map[string][]string{"viewer": {"view"}, "helpdesk": {"view", "session"}, "operator": {"view", "session", "session_logoff", "process", "maintenance"}, "profile_admin": {"view", "session", "profile"}, "policy_admin": {"view", "policy"}, "auditor": {"view", "audit"}, "admin": {"view", "session", "session_logoff", "process", "maintenance", "profile", "policy", "audit", "manage"}}

func (a *App) roles(r *http.Request) []string {
	u, ok := auth.UserFrom(r)
	if !ok {
		return nil
	}
	if len(a.cfg.RBAC.Groups) == 0 {
		return []string{"admin"}
	}
	set := map[string]bool{}
	for _, g := range u.Groups {
		for configured, roles := range a.cfg.RBAC.Groups {
			if auth.GroupMatches(g, configured) {
				for _, role := range roles {
					set[strings.ToLower(role)] = true
				}
			}
		}
	}
	if len(set) == 0 && a.cfg.RBAC.DefaultRole != "" {
		set[strings.ToLower(a.cfg.RBAC.DefaultRole)] = true
	}
	out := make([]string, 0, len(set))
	for role := range set {
		out = append(out, role)
	}
	sort.Strings(out)
	return out
}
func (a *App) permissions(r *http.Request) []string {
	set := map[string]bool{}
	for _, role := range a.roles(r) {
		for _, p := range rolePermissions[role] {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
func (a *App) hasPermission(r *http.Request, p string) bool {
	for _, x := range a.permissions(r) {
		if x == p {
			return true
		}
	}
	return false
}
func (a *App) require(permission string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.hasPermission(r, permission) {
			httpx.Error(w, 403, "insufficient role permission: "+permission)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func requestActor(r *http.Request) string {
	u, ok := auth.UserFrom(r)
	if !ok {
		return "unknown"
	}
	if strings.TrimSpace(u.Email) != "" {
		return u.Email
	}
	if strings.TrimSpace(u.Name) != "" {
		return u.Name
	}
	return u.Sub
}

func (a *App) masterPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	_, _ = fmt.Fprint(w, masterHTML)
}
func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
func hashToken(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func constantEqual(x, y string) bool {
	if len(x) != len(y) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(x), []byte(y)) == 1
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}
func commandTarget(rec model.AgentRecord, cmd model.SessionCommand) string {
	if cmd.PID != 0 {
		return fmt.Sprintf("%s/process/%d", rec.Name, cmd.PID)
	}
	if cmd.SessionID != 0 {
		return fmt.Sprintf("%s/session/%d", rec.Name, cmd.SessionID)
	}
	return rec.Name
}
func userSessionCount(ss []model.Session) int {
	n := 0
	for _, s := range ss {
		if s.User != "" {
			n++
		}
	}
	return n
}
func hasPendingAction(cmds []model.SessionCommand, action string) bool {
	for _, c := range cmds {
		if c.Action == action {
			return true
		}
	}
	return false
}
func agentOnline(rec model.AgentRecord, now time.Time, seconds int) bool {
	return !rec.LastSeen.IsZero() && now.Sub(rec.LastSeen) < time.Duration(seconds)*time.Second
}
func sessionCounts(ss []model.Session) (int, int) {
	a, d := 0, 0
	for _, s := range ss {
		if s.User == "" {
			continue
		}
		if s.State == "Active" {
			a++
		}
		if s.State == "Disconnected" {
			d++
		}
	}
	return a, d
}
func brokerScore(rec model.AgentRecord) float64 {
	active, disc := sessionCounts(rec.Snapshot.Sessions)
	mem := 0.0
	if rec.Snapshot.Server.MemoryTotal > 0 {
		mem = float64(rec.Snapshot.Server.MemoryTotal-rec.Snapshot.Server.MemoryAvailable) * 100 / float64(rec.Snapshot.Server.MemoryTotal)
	}
	return float64(rec.Snapshot.Health.Score)*10 - float64(active)*20 - float64(disc)*5 - rec.Snapshot.Server.CPUPercent*2 - mem
}
func normalizeUser(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	u = strings.ReplaceAll(u, "/", `\`)
	return u
}
func sessionMatchesUser(s model.Session, key string) bool {
	if s.User == "" {
		return false
	}
	key = normalizeUser(key)
	short := normalizeUser(s.User)
	qualified := normalizeUser(displaySessionUser(s))

	// A caller that supplied an explicit DOMAIN\user identity is expressing a
	// domain boundary. Never drop that qualifier merely because the short user
	// name happens to match another domain.
	if strings.Contains(key, `\`) {
		return qualified == key
	}
	if key == short || key == qualified {
		return true
	}
	if s.Domain != "" && normalizeUser(s.User+"@"+s.Domain) == key {
		return true
	}
	// PocketID preferred_username is often a UPN while WTS exposes a NetBIOS
	// domain. In that case the UPN suffix cannot be reliably derived from WTS;
	// permit local-part matching for UPNs, but retain strict matching above for
	// explicit DOMAIN\user values.
	if i := strings.Index(key, "@"); i > 0 && key[:i] == short {
		return true
	}
	return false
}
func displaySessionUser(s model.Session) string {
	if s.Domain != "" {
		return s.Domain + `\` + s.User
	}
	return s.User
}
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
func tagsMatch(have, need map[string]string) bool {
	for k, v := range need {
		if !strings.EqualFold(have[k], v) {
			return false
		}
	}
	return true
}
func remoteAppAlias(remoteApp string) string {
	v := strings.TrimSpace(remoteApp)
	v = strings.TrimPrefix(v, "||")
	return strings.TrimSpace(v)
}

func validRemoteAppAlias(alias string) bool {
	if alias == "" || len(alias) > 128 {
		return false
	}
	for _, r := range alias {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func (a *App) desiredRemoteAppsLocked(agentID string, rec model.AgentRecord) []model.RemoteAppSpec {
	out := make([]model.RemoteAppSpec, 0)
	for _, res := range a.store.data.Resources {
		if !res.Enabled || res.Kind != "remoteapp" || !res.ManageRemoteApp || strings.TrimSpace(res.RemoteAppPath) == "" {
			continue
		}
		if !a.agentInFarmLocked(agentID, rec, res.FarmID) {
			continue
		}
		alias := remoteAppAlias(res.RemoteApp)
		if !validRemoteAppAlias(alias) {
			continue
		}
		out = append(out, model.RemoteAppSpec{
			ResourceID: res.ID, Alias: alias, DisplayName: res.Name, Path: res.RemoteAppPath,
			IconPath: res.RemoteAppIconPath, IconIndex: res.RemoteAppIconIndex,
			CommandLineSetting: res.RemoteAppCommandLine, RequiredCommandLine: res.RemoteAppRequiredArgs,
			ShowInPortal: res.RemoteAppShowInPortal,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if strings.EqualFold(out[i].Alias, out[j].Alias) {
			return out[i].ResourceID < out[j].ResourceID
		}
		return strings.ToLower(out[i].Alias) < strings.ToLower(out[j].Alias)
	})
	return out
}

func agentResourceReady(rec model.AgentRecord, res *model.Resource) bool {
	if res == nil || res.Kind != "remoteapp" || !res.ManageRemoteApp {
		return true
	}
	alias := remoteAppAlias(res.RemoteApp)
	for _, st := range rec.Snapshot.RemoteApps {
		if st.ResourceID == res.ID || (st.ResourceID == "" && strings.EqualFold(st.Alias, alias)) {
			return st.Published && st.PathExists && st.InSync && strings.TrimSpace(st.Error) == ""
		}
	}
	return false
}

func resourceID(r *model.Resource) string {
	if r == nil {
		return ""
	}
	return r.ID
}
