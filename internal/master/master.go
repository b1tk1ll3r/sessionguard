package master

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/example/sessionguard/internal/auth"
	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/httpx"
	"github.com/example/sessionguard/internal/model"
)

const Version = "0.1.0"

type App struct {
	cfg   config.Master
	store *store
	auth  *auth.Manager
}

func New(ctx context.Context, cfg config.Master) (*App, error) {
	s, err := newStore(cfg.DataFile)
	if err != nil {
		return nil, err
	}
	a, err := auth.New(ctx, cfg.OIDC)
	if err != nil {
		return nil, fmt.Errorf("OIDC: %w", err)
	}
	return &App{cfg: cfg, store: s, auth: a}, nil
}

func (a *App) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	a.auth.Register(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, 200, map[string]any{"ok": true, "version": Version})
	})
	mux.HandleFunc("POST /api/v1/agents/enroll", a.enroll)
	mux.HandleFunc("POST /api/v1/agents/heartbeat", a.heartbeat)
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = fmt.Fprint(w, masterJS)
	})
	mux.Handle("GET /", a.auth.Require(http.HandlerFunc(a.masterPage)))
	mux.Handle("GET /api/v1/dashboard", a.auth.Require(http.HandlerFunc(a.dashboard)))
	mux.Handle("GET /api/v1/agents/{id}", a.auth.Require(http.HandlerFunc(a.agentDetail)))
	mux.Handle("PUT /api/v1/agents/{id}/policy", a.auth.Require(http.HandlerFunc(a.policy)))
	mux.Handle("PUT /api/v1/policy/all", a.auth.Require(http.HandlerFunc(a.policyAll)))
	server := &http.Server{Addr: a.cfg.Listen, Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(c)
	}()
	log.Printf("master listening on %s", a.cfg.Listen)
	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (a *App) enroll(w http.ResponseWriter, r *http.Request) {
	var req model.EnrollRequest
	if err := httpx.DecodeJSON(r, &req, 64<<10); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	if !constantEqual(req.EnrollmentToken, a.cfg.EnrollmentToken) || req.MachineID == "" {
		httpx.Error(w, 401, "invalid enrollment")
		return
	}
	now := time.Now().UTC()
	token := randomToken(32)
	id := randomToken(16)
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	for oldID, rec := range a.store.data.Agents {
		if rec.MachineID == req.MachineID {
			id = oldID
			break
		}
	}
	rec := a.store.data.Agents[id]
	rec.ID = id
	rec.Name = req.Name
	rec.MachineID = req.MachineID
	rec.TokenHash = hashToken(token)
	if rec.EnrolledAt.IsZero() {
		rec.EnrolledAt = now
	}
	a.store.data.Agents[id] = rec
	if err := a.store.saveLocked(); err != nil {
		httpx.Error(w, 500, err.Error())
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
	if err := httpx.DecodeJSON(r, &snap, 2<<20); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	rec, ok := a.store.data.Agents[id]
	if !ok || !constantEqual(hashToken(token), rec.TokenHash) {
		httpx.Error(w, 401, "invalid agent credentials")
		return
	}
	if snap.ProtocolVersion != model.ProtocolVersion {
		httpx.Error(w, 409, "protocol version mismatch")
		return
	}
	now := time.Now().UTC()
	snap.AgentID = id
	rec.LastSeen = now
	rec.Snapshot = snap
	if snap.Server.Hostname != "" {
		rec.Name = snap.Server.Hostname
	}
	a.store.data.Agents[id] = rec
	if err := a.store.saveLocked(); err != nil {
		httpx.Error(w, 500, err.Error())
		return
	}
	var desired *model.Policy
	if rec.DesiredPolicy != nil && rec.DesiredPolicy.Revision != snap.PolicyRevision {
		p := *rec.DesiredPolicy
		desired = &p
	}
	httpx.JSON(w, 200, model.HeartbeatResponse{DesiredPolicy: desired, ServerTime: now})
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	recs := a.store.all()
	sort.Slice(recs, func(i, j int) bool { return strings.ToLower(recs[i].Name) < strings.ToLower(recs[j].Name) })
	now := time.Now().UTC()
	type row struct {
		model.AgentRecord
		Online bool `json:"online"`
		Active int  `json:"active_sessions"`
		Total  int  `json:"total_sessions"`
	}
	out := make([]row, 0, len(recs))
	for _, rec := range recs {
		active := 0
		for _, s := range rec.Snapshot.Sessions {
			if s.State == "Active" {
				active++
			}
		}
		out = append(out, row{AgentRecord: rec, Online: now.Sub(rec.LastSeen) < time.Duration(a.cfg.OfflineAfterSeconds)*time.Second, Active: active, Total: len(rec.Snapshot.Sessions)})
	}
	httpx.JSON(w, 200, map[string]any{"agents": out, "server_time": now})
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
	if p.Cleanup.GraceSeconds < 1 || p.Cleanup.PollSeconds < 2 {
		httpx.Error(w, 400, "invalid cleanup timing")
		return
	}
	p.Revision = randomToken(12)
	p.UpdatedAt = time.Now().UTC()
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	rec, ok := a.store.data.Agents[id]
	if !ok {
		httpx.Error(w, 404, "agent not found")
		return
	}
	rec.DesiredPolicy = &p
	a.store.data.Agents[id] = rec
	if err := a.store.saveLocked(); err != nil {
		httpx.Error(w, 500, err.Error())
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
	if p.Cleanup.GraceSeconds < 1 || p.Cleanup.PollSeconds < 2 {
		httpx.Error(w, 400, "invalid cleanup timing")
		return
	}
	p.Revision = randomToken(12)
	p.UpdatedAt = time.Now().UTC()
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	for id, rec := range a.store.data.Agents {
		cp := p
		rec.DesiredPolicy = &cp
		a.store.data.Agents[id] = rec
	}
	if err := a.store.saveLocked(); err != nil {
		httpx.Error(w, 500, err.Error())
		return
	}
	httpx.JSON(w, 200, map[string]any{"updated_agents": len(a.store.data.Agents), "policy": p})
}

func (a *App) masterPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
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
