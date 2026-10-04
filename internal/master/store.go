package master

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/model"
	_ "github.com/lib/pq"
)

type data struct {
	Agents         map[string]model.AgentRecord `json:"agents"`
	Farms          map[string]model.Farm        `json:"farms,omitempty"`
	Resources      map[string]model.Resource    `json:"resources,omitempty"`
	Leases         map[string]model.UserLease   `json:"leases,omitempty"`
	Audit          []model.AuditEntry           `json:"audit,omitempty"`
	SessionHistory []model.SessionHistoryEvent  `json:"session_history,omitempty"`
	PolicyHistory  []model.PolicyVersion        `json:"policy_history,omitempty"`
	Alerts         map[string]model.Alert       `json:"alerts,omitempty"`
	AuthSessions   map[string]model.AuthSession `json:"auth_sessions,omitempty"`
	// IdentityBindings is keyed by the lower-cased Guacamole username.
	IdentityBindings map[string]model.IdentityBinding `json:"identity_bindings,omitempty"`
	GlobalPolicy     *model.Policy                    `json:"global_policy,omitempty"`
}

type persistence interface {
	Load(context.Context, *data) error
	Save(context.Context, data) error
	AppendAudit(context.Context, model.AuditEntry, int) error
	AppendHistory(context.Context, model.SessionHistoryEvent, int) error
	Close() error
	Kind() string
}

type store struct {
	mu         sync.RWMutex
	data       data
	backend    persistence
	persistErr error
}

func emptyData() data {
	return data{
		Agents: map[string]model.AgentRecord{}, Farms: map[string]model.Farm{}, Resources: map[string]model.Resource{},
		Leases: map[string]model.UserLease{}, Audit: []model.AuditEntry{}, SessionHistory: []model.SessionHistoryEvent{},
		PolicyHistory: []model.PolicyVersion{}, Alerts: map[string]model.Alert{}, AuthSessions: map[string]model.AuthSession{}, IdentityBindings: map[string]model.IdentityBinding{},
	}
}

func newStore(ctx context.Context, cfg config.Master) (*store, error) {
	var b persistence
	var err error
	if cfg.DatabaseURL != "" {
		b, err = newPostgresPersistence(ctx, cfg.DatabaseURL, cfg.HistoryLimit)
	} else {
		b = &jsonPersistence{path: cfg.DataFile}
	}
	if err != nil {
		return nil, err
	}
	s := &store{data: emptyData(), backend: b}
	if err := b.Load(ctx, &s.data); err != nil {
		_ = b.Close()
		return nil, err
	}
	s.normalize()
	return s, nil
}

func (s *store) normalize() {
	if s.data.Agents == nil {
		s.data.Agents = map[string]model.AgentRecord{}
	}
	if s.data.Farms == nil {
		s.data.Farms = map[string]model.Farm{}
	}
	if s.data.Resources == nil {
		s.data.Resources = map[string]model.Resource{}
	}
	if s.data.Leases == nil {
		s.data.Leases = map[string]model.UserLease{}
	}
	if s.data.Audit == nil {
		s.data.Audit = []model.AuditEntry{}
	}
	if s.data.SessionHistory == nil {
		s.data.SessionHistory = []model.SessionHistoryEvent{}
	}
	if s.data.PolicyHistory == nil {
		s.data.PolicyHistory = []model.PolicyVersion{}
	}
	if s.data.Alerts == nil {
		s.data.Alerts = map[string]model.Alert{}
	}
	if s.data.AuthSessions == nil {
		s.data.AuthSessions = map[string]model.AuthSession{}
	}
	if s.data.IdentityBindings == nil {
		s.data.IdentityBindings = map[string]model.IdentityBinding{}
	}
	seedIdentityBindings(&s.data)
	for id, a := range s.data.Agents {
		if a.Tags == nil {
			a.Tags = map[string]string{}
		}
		if a.MaintenanceMode == "" {
			a.MaintenanceMode = "online"
		}
		s.data.Agents[id] = a
	}
}

func (s *store) saveLocked() error {
	persistErr := s.persistErr
	s.persistErr = nil
	if err := s.backend.Save(context.Background(), s.data); err != nil {
		return err
	}
	return persistErr
}
func (s *store) close() error { return s.backend.Close() }
func (s *store) kind() string { return s.backend.Kind() }
func (s *store) appendAuditLocked(e model.AuditEntry) {
	s.data.Audit = append(s.data.Audit, e)
	if len(s.data.Audit) > 10000 {
		s.data.Audit = append([]model.AuditEntry(nil), s.data.Audit[len(s.data.Audit)-10000:]...)
	}
	if err := s.backend.AppendAudit(context.Background(), e, 10000); err != nil && s.persistErr == nil {
		s.persistErr = err
	}
}
func (s *store) appendHistoryLocked(e model.SessionHistoryEvent, limit int) {
	s.data.SessionHistory = append(s.data.SessionHistory, e)
	if limit > 0 && len(s.data.SessionHistory) > limit {
		s.data.SessionHistory = append([]model.SessionHistoryEvent(nil), s.data.SessionHistory[len(s.data.SessionHistory)-limit:]...)
	}
	if err := s.backend.AppendHistory(context.Background(), e, limit); err != nil && s.persistErr == nil {
		s.persistErr = err
	}
}
func (s *store) all() []model.AgentRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.AgentRecord, 0, len(s.data.Agents))
	for _, a := range s.data.Agents {
		a.TokenHash = ""
		out = append(out, a)
	}
	return out
}
func (s *store) get(id string) (model.AgentRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.data.Agents[id]
	a.TokenHash = ""
	return a, ok
}
func (s *store) audit(limit int) []model.AuditEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	start := len(s.data.Audit) - limit
	if start < 0 {
		start = 0
	}
	return append([]model.AuditEntry(nil), s.data.Audit[start:]...)
}
func (s *store) history(limit int) []model.SessionHistoryEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	start := len(s.data.SessionHistory) - limit
	if start < 0 {
		start = 0
	}
	return append([]model.SessionHistoryEvent(nil), s.data.SessionHistory[start:]...)
}

type jsonPersistence struct{ path string }

func (j *jsonPersistence) Kind() string { return "json" }
func (j *jsonPersistence) Close() error { return nil }
func (j *jsonPersistence) Load(_ context.Context, out *data) error {
	b, err := os.ReadFile(j.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
func (j *jsonPersistence) Save(_ context.Context, d data) error                     { return config.SaveJSON(j.path, d) }
func (j *jsonPersistence) AppendAudit(context.Context, model.AuditEntry, int) error { return nil }
func (j *jsonPersistence) AppendHistory(context.Context, model.SessionHistoryEvent, int) error {
	return nil
}

type postgresPersistence struct {
	db           *sql.DB
	leaderConn   *sql.Conn
	historyLimit int
}

func newPostgresPersistence(ctx context.Context, dsn string, historyLimit int) (*postgresPersistence, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(c); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	// v0.3 persists a single mutable control-plane state document. Protect it
	// from accidental active/active masters until a future HA design moves all
	// mutable coordination to transactionally independent database rows.
	leaderConn, err := db.Conn(c)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres leader connection: %w", err)
	}
	const masterLockID int64 = 0x534755415244 // "SGUARD"
	var leader bool
	if err := leaderConn.QueryRowContext(c, `SELECT pg_try_advisory_lock($1)`, masterLockID).Scan(&leader); err != nil {
		_ = leaderConn.Close()
		_ = db.Close()
		return nil, fmt.Errorf("postgres master lock: %w", err)
	}
	if !leader {
		_ = leaderConn.Close()
		_ = db.Close()
		return nil, errors.New("another active SessionGuard master holds the PostgreSQL control-plane lock")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessionguard_state (id integer PRIMARY KEY CHECK (id=1), payload jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS sessionguard_audit (id bigserial PRIMARY KEY, event_time timestamptz NOT NULL, actor text NOT NULL, action text NOT NULL, target text NOT NULL DEFAULT '', result text NOT NULL, payload jsonb NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS sessionguard_audit_time_idx ON sessionguard_audit(event_time DESC)`,
		`CREATE INDEX IF NOT EXISTS sessionguard_audit_actor_idx ON sessionguard_audit(actor)`,
		`CREATE TABLE IF NOT EXISTS sessionguard_session_history (id bigserial PRIMARY KEY, event_time timestamptz NOT NULL, agent_id text NOT NULL, username text NOT NULL DEFAULT '', sid text NOT NULL DEFAULT '', event text NOT NULL, payload jsonb NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS sessionguard_history_time_idx ON sessionguard_session_history(event_time DESC)`,
		`CREATE INDEX IF NOT EXISTS sessionguard_history_user_idx ON sessionguard_session_history(lower(username), event_time DESC)`,
		`CREATE INDEX IF NOT EXISTS sessionguard_history_agent_idx ON sessionguard_session_history(agent_id, event_time DESC)`,
		`CREATE TABLE IF NOT EXISTS sessionguard_migrations (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`,
		`INSERT INTO sessionguard_migrations(version) VALUES (2) ON CONFLICT DO NOTHING`,
	}
	for _, q := range stmts {
		if _, err := db.ExecContext(c, q); err != nil {
			_ = leaderConn.Close()
			_ = db.Close()
			return nil, fmt.Errorf("postgres schema: %w", err)
		}
	}
	return &postgresPersistence{db: db, leaderConn: leaderConn, historyLimit: historyLimit}, nil
}
func (p *postgresPersistence) Kind() string { return "postgres" }
func (p *postgresPersistence) Close() error {
	if p.leaderConn != nil {
		_ = p.leaderConn.Close() // releases the session-level advisory lock
	}
	return p.db.Close()
}
func (p *postgresPersistence) Load(ctx context.Context, out *data) error {
	var raw []byte
	err := p.db.QueryRowContext(ctx, `SELECT payload FROM sessionguard_state WHERE id=1`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return err
		}
	}
	// Older v0.3 development builds kept history inside the state blob. If the
	// normalized tables are empty, migrate those events once before replacing
	// the in-memory slices from the dedicated tables.
	legacyAudit := append([]model.AuditEntry(nil), out.Audit...)
	legacyHistory := append([]model.SessionHistoryEvent(nil), out.SessionHistory...)
	if n, _ := p.tableCount(ctx, "sessionguard_audit"); n == 0 && len(legacyAudit) > 0 {
		for _, e := range legacyAudit {
			if err := p.AppendAudit(ctx, e, 10000); err != nil {
				return err
			}
		}
	}
	if n, _ := p.tableCount(ctx, "sessionguard_session_history"); n == 0 && len(legacyHistory) > 0 {
		for _, e := range legacyHistory {
			if err := p.AppendHistory(ctx, e, p.historyLimit); err != nil {
				return err
			}
		}
	}
	audit, err := p.loadAudit(ctx, 10000)
	if err != nil {
		return err
	}
	out.Audit = audit
	history, err := p.loadHistory(ctx, p.historyLimit)
	if err != nil {
		return err
	}
	out.SessionHistory = history
	return nil
}

func (p *postgresPersistence) Save(ctx context.Context, d data) error {
	if err := p.ensureLeader(ctx); err != nil {
		return err
	}
	// High-volume append-only data lives in dedicated tables. Keeping it out of
	// this JSONB document makes heartbeats O(control-plane-state), not O(history).
	d.Audit = nil
	d.SessionHistory = nil
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = p.db.ExecContext(c, `INSERT INTO sessionguard_state(id,payload,updated_at) VALUES(1,$1,now()) ON CONFLICT(id) DO UPDATE SET payload=EXCLUDED.payload, updated_at=now()`, string(raw))
	return err
}

func (p *postgresPersistence) ensureLeader(ctx context.Context) error {
	if p.leaderConn == nil {
		return errors.New("PostgreSQL control-plane lock connection is unavailable")
	}
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var held bool
	err := p.leaderConn.QueryRowContext(c, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted)`).Scan(&held)
	if err != nil {
		return fmt.Errorf("PostgreSQL control-plane lock check failed: %w", err)
	}
	if !held {
		return errors.New("PostgreSQL control-plane lock was lost; restart the SessionGuard master")
	}
	return nil
}

func (p *postgresPersistence) AppendAudit(ctx context.Context, e model.AuditEntry, limit int) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err = p.db.ExecContext(c, `INSERT INTO sessionguard_audit(event_time,actor,action,target,result,payload) VALUES($1,$2,$3,$4,$5,$6)`, e.Time, e.Actor, e.Action, e.Target, e.Result, string(raw)); err != nil {
		return err
	}
	return p.retain(c, "sessionguard_audit", limit)
}

func (p *postgresPersistence) AppendHistory(ctx context.Context, e model.SessionHistoryEvent, limit int) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err = p.db.ExecContext(c, `INSERT INTO sessionguard_session_history(event_time,agent_id,username,sid,event,payload) VALUES($1,$2,$3,$4,$5,$6)`, e.Time, e.AgentID, e.User, e.SID, e.Event, string(raw)); err != nil {
		return err
	}
	return p.retain(c, "sessionguard_session_history", limit)
}

func (p *postgresPersistence) retain(ctx context.Context, table string, limit int) error {
	if limit <= 0 {
		return nil
	}
	var q string
	switch table {
	case "sessionguard_audit":
		q = `DELETE FROM sessionguard_audit WHERE id < COALESCE((SELECT id FROM sessionguard_audit ORDER BY id DESC OFFSET ($1 - 1) LIMIT 1),0)`
	case "sessionguard_session_history":
		q = `DELETE FROM sessionguard_session_history WHERE id < COALESCE((SELECT id FROM sessionguard_session_history ORDER BY id DESC OFFSET ($1 - 1) LIMIT 1),0)`
	default:
		return errors.New("unsupported retention table")
	}
	_, err := p.db.ExecContext(ctx, q, limit)
	return err
}

func (p *postgresPersistence) loadAudit(ctx context.Context, limit int) ([]model.AuditEntry, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT payload FROM sessionguard_audit ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AuditEntry{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var e model.AuditEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	reverseAudit(out)
	return out, nil
}
func (p *postgresPersistence) loadHistory(ctx context.Context, limit int) ([]model.SessionHistoryEvent, error) {
	if limit <= 0 {
		limit = 50000
	}
	rows, err := p.db.QueryContext(ctx, `SELECT payload FROM sessionguard_session_history ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.SessionHistoryEvent{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var e model.SessionHistoryEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	reverseHistory(out)
	return out, nil
}
func (p *postgresPersistence) tableCount(ctx context.Context, table string) (int64, error) {
	var q string
	switch table {
	case "sessionguard_audit":
		q = `SELECT count(*) FROM sessionguard_audit`
	case "sessionguard_session_history":
		q = `SELECT count(*) FROM sessionguard_session_history`
	default:
		return 0, errors.New("unsupported table")
	}
	var n int64
	err := p.db.QueryRowContext(ctx, q).Scan(&n)
	return n, err
}
func reverseAudit(x []model.AuditEntry) {
	for i, j := 0, len(x)-1; i < j; i, j = i+1, j-1 {
		x[i], x[j] = x[j], x[i]
	}
}
func reverseHistory(x []model.SessionHistoryEvent) {
	for i, j := 0, len(x)-1; i < j; i, j = i+1, j-1 {
		x[i], x[j] = x[j], x[i]
	}
}
