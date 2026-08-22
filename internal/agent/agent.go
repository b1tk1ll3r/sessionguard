package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/example/sessionguard/internal/auth"
	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/httpx"
	"github.com/example/sessionguard/internal/model"
	tpl "github.com/example/sessionguard/internal/templates"
	"github.com/example/sessionguard/internal/windowsx"
)

const Version = "0.1.0"

type App struct {
	cfg          config.Agent
	store        stateStore
	mu           sync.RWMutex
	state        State
	snapshot     model.AgentSnapshot
	lastMasterOK time.Time
	masterErr    string
	client       *masterClient
}

func New(cfg config.Agent) (*App, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	if cfg.Policy.Revision == "" {
		cfg.Policy.Revision = newRevision()
		cfg.Policy.UpdatedAt = time.Now().UTC()
	}
	st, err := loadState(statePath(cfg.DataDir), cfg.Policy)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, store: stateStore{path: statePath(cfg.DataDir)}, state: st, client: newMasterClient(cfg.MasterURL)}
	return a, nil
}

func newRevision() string { b := make([]byte, 12); _, _ = rand.Read(b); return hex.EncodeToString(b) }

func (a *App) Run(ctx context.Context) error {
	go a.worker(ctx)
	return a.serveHTTP(ctx)
}

func (a *App) worker(ctx context.Context) {
	poll := time.NewTicker(time.Duration(max(2, a.policy().Cleanup.PollSeconds)) * time.Second)
	defer poll.Stop()
	hb := time.NewTicker(time.Duration(max(3, a.cfg.HeartbeatSeconds)) * time.Second)
	defer hb.Stop()
	a.tick(ctx)
	a.sendHeartbeat(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			a.tick(ctx)
		case <-hb.C:
			a.sendHeartbeat(ctx)
		}
	}
}

func (a *App) tick(ctx context.Context) {
	sessions, err := windowsx.Sessions()
	if err != nil {
		log.Printf("sessions: %v", err)
		return
	}
	server, err := windowsx.Server()
	if err != nil {
		log.Printf("server info: %v", err)
	}
	now := time.Now().UTC()

	a.mu.Lock()
	defer a.mu.Unlock()
	currentBySID := map[string]bool{}
	currentIDs := map[uint32]model.Session{}
	for _, s := range sessions {
		currentIDs[s.ID] = s
		if s.SID != "" {
			currentBySID[s.SID] = true
			delete(a.state.Pending, s.SID)
		}
	}

	for id, prev := range a.state.LastSessions {
		if _, exists := currentIDs[id]; exists || prev.SID == "" || currentBySID[prev.SID] {
			continue
		}
		if _, exists := a.state.Pending[prev.SID]; exists {
			continue
		}
		if a.excluded(prev) {
			continue
		}
		path, err := windowsx.ProfilePath(prev.SID)
		if err != nil {
			log.Printf("profile path for %s/%s: %v", prev.User, prev.SID, err)
			continue
		}
		if !a.safeProfilePath(path) {
			log.Printf("refusing cleanup outside allowed roots: %s (%s)", path, prev.User)
			continue
		}
		a.state.Pending[prev.SID] = model.CleanupJob{SID: prev.SID, User: displayUser(prev), ProfilePath: path, DueAt: now.Add(time.Duration(a.state.Policy.Cleanup.GraceSeconds) * time.Second)}
		log.Printf("scheduled profile cleanup: %s in %ds", displayUser(prev), a.state.Policy.Cleanup.GraceSeconds)
	}

	for _, s := range sessions {
		if s.SID == "" || s.User == "" {
			continue
		}
		_, was := a.state.LastSessions[s.ID]
		if !was {
			a.applyTemplatesLocked(s)
		}
	}

	if a.state.Policy.Cleanup.Enabled {
		a.processCleanupLocked(now, currentBySID)
	}
	a.state.LastSessions = currentIDs
	a.snapshot = model.AgentSnapshot{ProtocolVersion: model.ProtocolVersion, AgentID: a.state.AgentID, Server: server, Sessions: sessions, PendingCleanup: pendingSlice(a.state.Pending), PolicyRevision: a.state.Policy.Revision, AgentVersion: Version, Time: now}
	if err := a.store.save(a.state); err != nil {
		log.Printf("save state: %v", err)
	}
}

func (a *App) applyTemplatesLocked(s model.Session) {
	path, err := windowsx.ProfilePath(s.SID)
	if err != nil {
		log.Printf("templates profile %s: %v", displayUser(s), err)
		return
	}
	for _, item := range a.state.Policy.Templates {
		changed, err := tpl.Apply(path, item)
		if err != nil {
			log.Printf("template %s for %s: %v", item.ID, displayUser(s), err)
			continue
		}
		if changed {
			log.Printf("template %s applied for %s", item.ID, displayUser(s))
		}
	}
}

func (a *App) processCleanupLocked(now time.Time, active map[string]bool) {
	p := a.state.Policy.Cleanup
	for sid, job := range a.state.Pending {
		if active[sid] {
			delete(a.state.Pending, sid)
			continue
		}
		if now.Before(job.DueAt) {
			continue
		}
		if !a.safeProfilePath(job.ProfilePath) {
			job.LastError = "profile path is outside allowed roots"
			job.DueAt = now.Add(time.Duration(p.RetrySeconds) * time.Second)
			a.state.Pending[sid] = job
			continue
		}
		if p.DryRun {
			job.LastError = "dry-run: deletion skipped"
			job.DueAt = now.Add(time.Duration(p.RetrySeconds) * time.Second)
			a.state.Pending[sid] = job
			log.Printf("dry-run profile deletion: %s (%s)", job.User, job.ProfilePath)
			continue
		}
		if err := windowsx.DeleteProfile(sid); err != nil {
			job.Attempts++
			job.LastError = err.Error()
			job.DueAt = now.Add(time.Duration(p.RetrySeconds) * time.Second)
			a.state.Pending[sid] = job
			log.Printf("delete profile %s: %v", job.User, err)
			continue
		}
		delete(a.state.Pending, sid)
		log.Printf("deleted profile: %s (%s)", job.User, job.ProfilePath)
	}
}

func (a *App) excluded(s model.Session) bool {
	p := a.state.Policy.Cleanup
	for _, u := range p.ExcludeUsers {
		if strings.EqualFold(strings.TrimSpace(u), s.User) || strings.EqualFold(strings.TrimSpace(u), displayUser(s)) {
			return true
		}
	}
	for _, x := range p.ExcludeSIDs {
		if strings.EqualFold(s.SID, x) || strings.HasPrefix(strings.ToUpper(s.SID), strings.ToUpper(x)+"-") {
			return true
		}
	}
	return false
}

func (a *App) safeProfilePath(path string) bool {
	clean, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	for _, root := range a.state.Policy.Cleanup.AllowedProfileRoots {
		r, err := filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		c, rr := strings.ToLower(clean), strings.ToLower(r)
		if c == rr || strings.HasPrefix(c, rr+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func displayUser(s model.Session) string {
	if s.Domain != "" {
		return s.Domain + `\` + s.User
	}
	return s.User
}
func pendingSlice(m map[string]model.CleanupJob) []model.CleanupJob {
	out := make([]model.CleanupJob, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (a *App) policy() model.Policy { a.mu.RLock(); defer a.mu.RUnlock(); return a.state.Policy }

func (a *App) sendHeartbeat(ctx context.Context) {
	if a.cfg.MasterURL == "" {
		return
	}
	a.mu.Lock()
	if a.state.AgentID == "" || a.state.AgentToken == "" {
		server, _ := windowsx.Server()
		mid, _ := windowsx.MachineID()
		resp, err := a.client.enroll(ctx, model.EnrollRequest{EnrollmentToken: a.cfg.EnrollmentToken, Name: server.Hostname, MachineID: mid})
		if err != nil {
			a.masterErr = err.Error()
			a.mu.Unlock()
			log.Printf("master enroll: %v", err)
			return
		}
		a.state.AgentID, a.state.AgentToken = resp.AgentID, resp.Token
		_ = a.store.save(a.state)
	}
	id, token, snap := a.state.AgentID, a.state.AgentToken, a.snapshot
	snap.AgentID = id
	a.mu.Unlock()
	resp, err := a.client.heartbeat(ctx, id, token, snap)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.masterErr = err.Error()
		log.Printf("master heartbeat: %v", err)
		return
	}
	a.lastMasterOK = time.Now().UTC()
	a.masterErr = ""
	if resp.DesiredPolicy != nil && resp.DesiredPolicy.Revision != "" && resp.DesiredPolicy.Revision != a.state.Policy.Revision {
		a.state.Policy = *resp.DesiredPolicy
		_ = a.store.save(a.state)
		log.Printf("applied master policy revision %s", a.state.Policy.Revision)
		for _, s := range a.state.LastSessions {
			if s.SID != "" && s.User != "" {
				a.applyTemplatesLocked(s)
			}
		}
	}
}

func (a *App) serveHTTP(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { httpx.JSON(w, 200, map[string]any{"ok": true}) })
	var am *auth.Manager
	if a.cfg.OIDC.Issuer != "" {
		var err error
		am, err = auth.New(ctx, a.cfg.OIDC)
		if err != nil {
			log.Printf("local OIDC unavailable: %v", err)
		}
	}
	if am != nil {
		am.Register(mux)
	}
	secure := func(h http.Handler) http.Handler {
		if am == nil {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "OIDC is not configured or unavailable", http.StatusServiceUnavailable)
			})
		}
		return am.Require(h)
	}
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = fmt.Fprint(w, agentJS)
	})
	mux.Handle("GET /", secure(http.HandlerFunc(a.agentPage)))
	mux.Handle("GET /api/v1/status", secure(http.HandlerFunc(a.statusAPI)))
	mux.Handle("GET /api/v1/policy", secure(http.HandlerFunc(a.policyAPI)))
	mux.Handle("PUT /api/v1/policy", secure(http.HandlerFunc(a.policyAPI)))
	server := &http.Server{Addr: a.cfg.Listen, Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(c)
	}()
	log.Printf("agent web listening on %s", a.cfg.Listen)
	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (a *App) statusAPI(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	httpx.JSON(w, 200, map[string]any{"snapshot": a.snapshot, "last_master_ok": a.lastMasterOK, "master_error": a.masterErr, "master_url": a.cfg.MasterURL})
}
func (a *App) policyAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.mu.RLock()
		defer a.mu.RUnlock()
		httpx.JSON(w, 200, a.state.Policy)
		return
	}
	if !httpx.SameOrigin(r) {
		httpx.Error(w, 403, "cross-origin request rejected")
		return
	}
	var p model.Policy
	if err := httpx.DecodeJSON(r, &p, 2<<20); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	p.Revision = newRevision()
	p.UpdatedAt = time.Now().UTC()
	if p.Cleanup.GraceSeconds < 1 || p.Cleanup.PollSeconds < 2 {
		httpx.Error(w, 400, "invalid cleanup timing")
		return
	}
	a.mu.Lock()
	a.state.Policy = p
	_ = a.store.save(a.state)
	sessions := a.state.LastSessions
	a.mu.Unlock()
	for _, s := range sessions {
		if s.SID != "" && s.User != "" {
			a.mu.Lock()
			a.applyTemplatesLocked(s)
			a.mu.Unlock()
		}
	}
	httpx.JSON(w, 200, p)
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

func (a *App) agentPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, agentHTML)
}
