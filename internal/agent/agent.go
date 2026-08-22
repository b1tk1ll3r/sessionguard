package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/sessionguard/internal/auth"
	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/httpx"
	"github.com/example/sessionguard/internal/model"
	profilesync "github.com/example/sessionguard/internal/profile"
	tpl "github.com/example/sessionguard/internal/templates"
	"github.com/example/sessionguard/internal/windowsx"
)

const Version = "0.3.0"

type App struct {
	cfg          config.Agent
	store        stateStore
	mu           sync.RWMutex
	state        State
	snapshot     model.AgentSnapshot
	lastMasterOK time.Time
	masterErr    string
	client       *masterClient
	sessionWake  chan struct{}
	bootstrapped bool
}

func New(cfg config.Agent) (*App, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	config.NormalizePolicy(&cfg.Policy)
	if err := config.ValidatePolicy(cfg.Policy); err != nil {
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
	config.NormalizePolicy(&st.Policy)
	return &App{cfg: cfg, store: stateStore{path: statePath(cfg.DataDir)}, state: st, client: newMasterClient(cfg.MasterURL), sessionWake: make(chan struct{}, 1)}, nil
}

func newRevision() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NotifySessionChange is called by the Windows service handler for SERVICE_CONTROL_SESSIONCHANGE.
// It is deliberately non-blocking: the worker performs the actual WTS/profile work outside the SCM callback.
func (a *App) NotifySessionChange() {
	select {
	case a.sessionWake <- struct{}{}:
	default:
	}
}

func (a *App) Run(ctx context.Context) error {
	go a.worker(ctx)
	return a.serveHTTP(ctx)
}

func (a *App) worker(ctx context.Context) {
	poll := time.NewTimer(0)
	defer poll.Stop()
	hb := time.NewTicker(time.Duration(max(3, a.cfg.HeartbeatSeconds)) * time.Second)
	defer hb.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			a.tick(ctx)
			poll.Reset(time.Duration(max(2, a.policy().Cleanup.PollSeconds)) * time.Second)
		case <-a.sessionWake:
			a.tick(ctx)
			if !poll.Stop() {
				select {
				case <-poll.C:
				default:
				}
			}
			poll.Reset(time.Duration(max(2, a.policy().Cleanup.PollSeconds)) * time.Second)
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
	processes, processErr := windowsx.Processes()
	if processErr != nil {
		log.Printf("processes: %v", processErr)
	}
	health := a.calculateHealth(server)
	now := time.Now().UTC()
	var autoLogoff []model.Session
	var templateNow []model.Session

	a.mu.Lock()
	isBootstrap := !a.bootstrapped
	currentBySID := map[string]bool{}
	currentIDs := map[uint32]model.Session{}
	for i := range sessions {
		s := &sessions[i]
		if s.User == "" {
			currentIDs[s.ID] = *s
			continue
		}
		if _, existed := a.state.LastSessions[s.ID]; !existed && !isBootstrap {
			t := model.SessionTelemetry{SessionID: s.ID, SID: s.SID, User: displayUser(*s), FirstSeenAt: now}
			if s.LogonAt != nil {
				t.LogonAt = *s.LogonAt
			}
			a.state.Telemetry[s.ID] = t
		}
		if s.SID != "" {
			currentBySID[s.SID] = true
			delete(a.state.Pending, s.SID)
			delete(a.state.ProfileJobs, backupJobID(s.SID))
		}
		if s.State == "Disconnected" {
			since, ok := a.state.DisconnectedSince[s.ID]
			if !ok {
				since = now
				a.state.DisconnectedSince[s.ID] = since
				a.appendEventLocked("info", "session_disconnected", displayUser(*s), fmt.Sprintf("Sitzung %d wurde getrennt", s.ID))
			}
			s.DisconnectedSince = &since
			sp := a.state.Policy.Sessions
			if sp.DisconnectedLogoffEnabled && !a.sessionExcluded(*s) && now.Sub(since) >= time.Duration(sp.DisconnectedTimeoutSeconds)*time.Second {
				if _, requested := a.state.AutoLogoffRequested[s.ID]; !requested {
					a.state.AutoLogoffRequested[s.ID] = now
					autoLogoff = append(autoLogoff, *s)
					a.appendEventLocked("warning", "auto_logoff_due", displayUser(*s), fmt.Sprintf("Getrennte Sitzung %d hat das Timeout von %d Sekunden erreicht", s.ID, sp.DisconnectedTimeoutSeconds))
				}
			}
		} else {
			delete(a.state.DisconnectedSince, s.ID)
			delete(a.state.AutoLogoffRequested, s.ID)
		}
		currentIDs[s.ID] = *s

		if s.SID != "" && !a.state.RestoredSessions[s.ID] {
			pp := a.state.Policy.Profiles
			if isBootstrap {
				// Never inject a restore into a session that was already present when the
				// service started. Restores are reserved for sessions observed after startup.
				a.state.RestoredSessions[s.ID] = true
				templateNow = append(templateNow, *s)
			} else if pp.Enabled && pp.RestoreOnLogon && !a.profileExcluded(*s) {
				if _, exists := a.state.ProfileJobs[restoreJobID(s.ID)]; !exists {
					if profilePath, e := windowsx.ProfilePath(s.SID); e == nil {
						a.state.ProfileJobs[restoreJobID(s.ID)] = model.ProfileJob{ID: restoreJobID(s.ID), Operation: "restore", SID: s.SID, User: displayUser(*s), SessionID: s.ID, ProfilePath: profilePath, Reason: "logon", CreatedAt: now, DueAt: now}
						t := a.state.Telemetry[s.ID]
						t.RestoreStartedAt = now
						a.state.Telemetry[s.ID] = t
						a.appendEventLocked("info", "profile_restore_scheduled", displayUser(*s), fmt.Sprintf("Profil-Wiederherstellung für Sitzung %d eingeplant", s.ID))
					} else {
						a.appendEventLocked("error", "profile_restore_schedule_error", displayUser(*s), fmt.Sprintf("Profilpfad konnte nicht ermittelt werden: %v", e))
					}
				}
			} else {
				a.state.RestoredSessions[s.ID] = true
				templateNow = append(templateNow, *s)
			}
		}
	}

	for id, prev := range a.state.LastSessions {
		if _, exists := currentIDs[id]; exists || prev.SID == "" || currentBySID[prev.SID] {
			continue
		}
		delete(a.state.DisconnectedSince, id)
		delete(a.state.AutoLogoffRequested, id)
		delete(a.state.RestoredSessions, id)
		delete(a.state.ProfileJobs, restoreJobID(id))
		a.handleEndedSessionLocked(prev, now)
	}
	a.state.LastSessions = currentIDs
	a.pruneTelemetryLocked(currentIDs, 500)
	a.bootstrapped = true
	_ = a.store.save(a.state)
	a.mu.Unlock()

	for _, s := range autoLogoff {
		a.autoLogoff(s)
	}
	for _, s := range templateNow {
		a.applyTemplates(s)
	}
	a.processProfileJobs(ctx, now)
	a.processCleanup(now)
	a.refreshSnapshot(server, sessions, limitUserProcesses(processes, 3000), health, now)
}

func (a *App) pruneTelemetryLocked(active map[uint32]model.Session, limit int) {
	if limit <= 0 || len(a.state.Telemetry) <= limit {
		return
	}
	type candidate struct {
		id   uint32
		time time.Time
	}
	candidates := make([]candidate, 0, len(a.state.Telemetry))
	for id, t := range a.state.Telemetry {
		if _, ok := active[id]; ok {
			continue
		}
		candidates = append(candidates, candidate{id: id, time: t.FirstSeenAt})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].time.Before(candidates[j].time) })
	remove := len(a.state.Telemetry) - limit
	if remove > len(candidates) {
		remove = len(candidates)
	}
	for i := 0; i < remove; i++ {
		delete(a.state.Telemetry, candidates[i].id)
	}
}

func limitUserProcesses(in []model.ProcessInfo, limit int) []model.ProcessInfo {
	if limit <= 0 {
		return nil
	}
	out := make([]model.ProcessInfo, 0, min(len(in), limit))
	for _, p := range in {
		if p.SessionID == 0 {
			continue
		}
		out = append(out, p)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (a *App) handleEndedSessionLocked(prev model.Session, now time.Time) {
	pp := a.state.Policy.Profiles
	if pp.Enabled && pp.BackupOnLogoff && !a.profileExcluded(prev) {
		id := backupJobID(prev.SID)
		if _, exists := a.state.ProfileJobs[id]; !exists {
			profilePath, err := windowsx.ProfilePath(prev.SID)
			if err != nil {
				a.appendEventLocked("error", "profile_backup_schedule_error", displayUser(prev), fmt.Sprintf("Profilpfad konnte nicht ermittelt werden: %v", err))
				return
			}
			due := now.Add(time.Duration(max(0, pp.BackupDelaySeconds)) * time.Second)
			a.state.ProfileJobs[id] = model.ProfileJob{ID: id, Operation: "backup", SID: prev.SID, User: displayUser(prev), ProfilePath: profilePath, Reason: "logoff", CreatedAt: now, DueAt: due}
			a.appendEventLocked("info", "profile_backup_scheduled", displayUser(prev), fmt.Sprintf("Profilsicherung nach Sitzungsende in %d Sekunden eingeplant", pp.BackupDelaySeconds))
		}
		return // Cleanup is intentionally gated by successful backup.
	}
	a.scheduleCleanupLocked(prev.SID, displayUser(prev), now)
}

func (a *App) scheduleCleanupLocked(sid, user string, now time.Time) {
	p := a.state.Policy.Cleanup
	if !p.Enabled || sid == "" || excludedIdentity(user, sid, p.ExcludeUsers, p.ExcludeSIDs) {
		return
	}
	if _, exists := a.state.Pending[sid]; exists {
		return
	}
	path, err := windowsx.ProfilePath(sid)
	if err != nil {
		a.appendEventLocked("error", "cleanup_schedule_error", user, fmt.Sprintf("Profilpfad konnte nicht ermittelt werden: %v", err))
		return
	}
	if !safeProfilePath(path, p.AllowedProfileRoots) {
		a.appendEventLocked("error", "cleanup_blocked", user, fmt.Sprintf("Profilpfad außerhalb der erlaubten Roots: %s", path))
		return
	}
	a.state.Pending[sid] = model.CleanupJob{SID: sid, User: user, ProfilePath: path, DueAt: now.Add(time.Duration(p.GraceSeconds) * time.Second)}
	a.appendEventLocked("info", "cleanup_scheduled", user, fmt.Sprintf("Profilbereinigung in %d Sekunden geplant: %s", p.GraceSeconds, path))
}

func (a *App) processProfileJobs(ctx context.Context, now time.Time) {
	a.mu.RLock()
	jobs := make([]model.ProfileJob, 0, len(a.state.ProfileJobs))
	for _, j := range a.state.ProfileJobs {
		if !now.Before(j.DueAt) {
			jobs = append(jobs, j)
		}
	}
	a.mu.RUnlock()
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return
		default:
		}
		a.processProfileJob(job)
	}
}

func (a *App) processProfileJob(job model.ProfileJob) {
	policy := a.policy()
	pp := policy.Profiles
	if !pp.Enabled {
		a.mu.Lock()
		delete(a.state.ProfileJobs, job.ID)
		_ = a.store.save(a.state)
		a.mu.Unlock()
		return
	}
	current, err := windowsx.Sessions()
	if err != nil {
		a.retryProfileJob(job, fmt.Errorf("session recheck: %w", err))
		return
	}
	var target *model.Session
	activeSID := false
	for i := range current {
		if current[i].SID == job.SID && current[i].User != "" {
			activeSID = true
		}
		if current[i].ID == job.SessionID {
			target = &current[i]
		}
	}
	if job.Operation == "backup" && activeSID {
		a.mu.Lock()
		delete(a.state.ProfileJobs, job.ID)
		delete(a.state.Pending, job.SID)
		a.appendEventLocked("info", "profile_backup_cancelled", job.User, "Profilsicherung verworfen, weil der Benutzer wieder angemeldet ist")
		_ = a.store.save(a.state)
		a.mu.Unlock()
		return
	}
	if job.Operation == "restore" && (target == nil || target.SID != job.SID) {
		a.mu.Lock()
		delete(a.state.ProfileJobs, job.ID)
		_ = a.store.save(a.state)
		a.mu.Unlock()
		return
	}

	switch job.Operation {
	case "backup":
		stats, err := profilesync.BackupGuarded(job.ProfilePath, pp.StoreRoot, job.SID, job.User, pp.Folders, pp.KeepVersions, func() error {
			sessions, err := windowsx.Sessions()
			if err != nil {
				return fmt.Errorf("session recheck before snapshot activation: %w", err)
			}
			for _, s := range sessions {
				if s.SID == job.SID && s.User != "" {
					return fmt.Errorf("user session %d became active during backup", s.ID)
				}
			}
			return nil
		})
		if err != nil {
			a.retryProfileJob(job, err)
			a.mu.Lock()
			st := a.state.ProfileStatus[job.SID]
			st.SID, st.User, st.LastBackupError = job.SID, job.User, err.Error()
			a.state.ProfileStatus[job.SID] = st
			_ = a.store.save(a.state)
			a.mu.Unlock()
			return
		}
		a.mu.Lock()
		delete(a.state.ProfileJobs, job.ID)
		st := a.state.ProfileStatus[job.SID]
		st.SID, st.User, st.LastBackupAt, st.LastBackupError = job.SID, job.User, time.Now().UTC(), ""
		a.state.ProfileStatus[job.SID] = st
		a.appendEventLocked("info", "profile_backup_complete", job.User, fmt.Sprintf("Profilsicherung abgeschlossen: %d Dateien, %d Bytes", stats.Files, stats.Bytes))
		// Cleanup starts only after the backup is safely activated as current snapshot.
		a.scheduleCleanupLocked(job.SID, job.User, time.Now().UTC())
		_ = a.store.save(a.state)
		a.mu.Unlock()
	case "restore":
		stats, found, err := profilesync.Restore(job.ProfilePath, pp.StoreRoot, job.SID, pp.Folders)
		if err != nil {
			if !job.CreatedAt.IsZero() && time.Since(job.CreatedAt) >= time.Duration(pp.RestoreWindowSeconds)*time.Second {
				a.abandonRestore(job, err, target)
				return
			}
			a.retryProfileJob(job, err)
			a.mu.Lock()
			st := a.state.ProfileStatus[job.SID]
			st.SID, st.User, st.LastRestoreError = job.SID, job.User, err.Error()
			a.state.ProfileStatus[job.SID] = st
			_ = a.store.save(a.state)
			a.mu.Unlock()
			return
		}
		a.mu.Lock()
		delete(a.state.ProfileJobs, job.ID)
		a.state.RestoredSessions[job.SessionID] = true
		st := a.state.ProfileStatus[job.SID]
		st.SID, st.User, st.LastRestoreAt, st.LastRestoreError = job.SID, job.User, time.Now().UTC(), ""
		a.state.ProfileStatus[job.SID] = st
		if t, ok := a.state.Telemetry[job.SessionID]; ok {
			t.RestoreFinishedAt = time.Now().UTC()
			if !t.RestoreStartedAt.IsZero() {
				t.RestoreDurationMS = t.RestoreFinishedAt.Sub(t.RestoreStartedAt).Milliseconds()
			}
			a.state.Telemetry[job.SessionID] = t
		}
		if found {
			a.appendEventLocked("info", "profile_restore_complete", job.User, fmt.Sprintf("Profil-Wiederherstellung abgeschlossen: %d Dateien, %d Bytes", stats.Files, stats.Bytes))
		} else {
			a.appendEventLocked("info", "profile_restore_empty", job.User, "Noch kein gespeichertes Profil vorhanden; Anmeldung wird ohne Restore fortgesetzt")
		}
		_ = a.store.save(a.state)
		a.mu.Unlock()
		if target != nil {
			a.applyTemplates(*target)
		}
	default:
		a.retryProfileJob(job, fmt.Errorf("unknown profile job operation %q", job.Operation))
	}
}

func (a *App) abandonRestore(job model.ProfileJob, restoreErr error, target *model.Session) {
	a.mu.Lock()
	delete(a.state.ProfileJobs, job.ID)
	a.state.RestoredSessions[job.SessionID] = true
	st := a.state.ProfileStatus[job.SID]
	st.SID, st.User, st.LastRestoreError = job.SID, job.User, restoreErr.Error()
	a.state.ProfileStatus[job.SID] = st
	a.appendEventLocked("error", "profile_restore_abandoned", job.User, fmt.Sprintf("Restore-Fenster von %d Sekunden abgelaufen; Anmeldung läuft ohne weiteren Restore weiter: %v", a.state.Policy.Profiles.RestoreWindowSeconds, restoreErr))
	_ = a.store.save(a.state)
	a.mu.Unlock()
	if target != nil {
		a.applyTemplates(*target)
	}
}

func (a *App) retryProfileJob(job model.ProfileJob, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, ok := a.state.ProfileJobs[job.ID]
	if !ok {
		return
	}
	current.Attempts++
	current.LastError = err.Error()
	current.DueAt = time.Now().UTC().Add(time.Duration(max(1, a.state.Policy.Profiles.RetrySeconds)) * time.Second)
	a.state.ProfileJobs[job.ID] = current
	a.appendEventLocked("error", "profile_"+job.Operation+"_error", job.User, fmt.Sprintf("Profil-%s fehlgeschlagen (Versuch %d): %v", job.Operation, current.Attempts, err))
	_ = a.store.save(a.state)
}

func (a *App) applyTemplates(s model.Session) {
	path, err := windowsx.ProfilePath(s.SID)
	if err != nil {
		log.Printf("templates profile %s: %v", displayUser(s), err)
		return
	}
	policy := a.policy()
	for _, item := range policy.Templates {
		changed, err := tpl.Apply(path, item)
		a.mu.Lock()
		if err != nil {
			a.appendEventLocked("error", "template_error", displayUser(s), fmt.Sprintf("Template %s konnte nicht angewendet werden: %v", item.ID, err))
		} else if changed {
			a.appendEventLocked("info", "template_applied", displayUser(s), fmt.Sprintf("Template %s angewendet", item.ID))
		}
		_ = a.store.save(a.state)
		a.mu.Unlock()
		if err != nil {
			log.Printf("template %s for %s: %v", item.ID, displayUser(s), err)
		}
	}

	a.mu.Lock()
	if t, ok := a.state.Telemetry[s.ID]; ok {
		t.ReadyAt = time.Now().UTC()
		base := t.LogonAt
		if base.IsZero() {
			base = t.FirstSeenAt
		}
		if !base.IsZero() {
			t.ObservedLogonMS = t.ReadyAt.Sub(base).Milliseconds()
		}
		a.state.Telemetry[s.ID] = t
	}
	_ = a.store.save(a.state)
	a.mu.Unlock()
}

func (a *App) processCleanup(now time.Time) {
	p := a.policy().Cleanup
	if !p.Enabled {
		return
	}
	sessions, err := windowsx.Sessions()
	if err != nil {
		return
	}
	active := map[string]bool{}
	for _, s := range sessions {
		if s.SID != "" {
			active[s.SID] = true
		}
	}
	a.mu.RLock()
	jobs := make([]model.CleanupJob, 0, len(a.state.Pending))
	for _, job := range a.state.Pending {
		if !now.Before(job.DueAt) {
			jobs = append(jobs, job)
		}
	}
	a.mu.RUnlock()
	for _, job := range jobs {
		if active[job.SID] {
			a.mu.Lock()
			delete(a.state.Pending, job.SID)
			_ = a.store.save(a.state)
			a.mu.Unlock()
			continue
		}
		if !safeProfilePath(job.ProfilePath, p.AllowedProfileRoots) {
			a.mu.Lock()
			job.LastError = "profile path is outside allowed roots"
			job.DueAt = time.Now().UTC().Add(time.Duration(p.RetrySeconds) * time.Second)
			a.state.Pending[job.SID] = job
			a.appendEventLocked("error", "cleanup_blocked", job.User, fmt.Sprintf("Profilpfad außerhalb der erlaubten Roots: %s", job.ProfilePath))
			_ = a.store.save(a.state)
			a.mu.Unlock()
			continue
		}
		// Recheck immediately before the destructive decision. A user may have logged in
		// after the session list at the beginning of this cleanup pass was collected.
		latest, recheckErr := windowsx.Sessions()
		if recheckErr != nil {
			a.mu.Lock()
			job.LastError = "final session recheck: " + recheckErr.Error()
			job.DueAt = time.Now().UTC().Add(time.Duration(p.RetrySeconds) * time.Second)
			a.state.Pending[job.SID] = job
			a.appendEventLocked("error", "cleanup_recheck_error", job.User, fmt.Sprintf("Finaler Session-Recheck vor Profilbereinigung fehlgeschlagen: %v", recheckErr))
			_ = a.store.save(a.state)
			a.mu.Unlock()
			continue
		}
		sidActive := false
		for _, s := range latest {
			if s.SID == job.SID && s.User != "" {
				sidActive = true
				break
			}
		}
		if sidActive {
			a.mu.Lock()
			delete(a.state.Pending, job.SID)
			a.appendEventLocked("info", "cleanup_cancelled", job.User, "Profilbereinigung beim finalen Recheck verworfen, weil der Benutzer wieder angemeldet ist")
			_ = a.store.save(a.state)
			a.mu.Unlock()
			continue
		}

		if p.DryRun {
			a.mu.Lock()
			a.appendEventLocked("dry-run", "cleanup_dry_run", job.User, fmt.Sprintf("Profil würde jetzt gelöscht: %s", job.ProfilePath))
			delete(a.state.Pending, job.SID)
			_ = a.store.save(a.state)
			a.mu.Unlock()
			continue
		}
		if err := windowsx.DeleteProfile(job.SID); err != nil {
			a.mu.Lock()
			job.Attempts++
			job.LastError = err.Error()
			job.DueAt = time.Now().UTC().Add(time.Duration(p.RetrySeconds) * time.Second)
			a.state.Pending[job.SID] = job
			a.appendEventLocked("error", "cleanup_error", job.User, fmt.Sprintf("Profil konnte nicht gelöscht werden: %v", err))
			_ = a.store.save(a.state)
			a.mu.Unlock()
			continue
		}
		a.mu.Lock()
		delete(a.state.Pending, job.SID)
		a.appendEventLocked("info", "cleanup_deleted", job.User, fmt.Sprintf("Profil gelöscht: %s", job.ProfilePath))
		_ = a.store.save(a.state)
		a.mu.Unlock()
	}
}

func (a *App) autoLogoff(s model.Session) {
	err := windowsx.LogoffSession(s.ID)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		delete(a.state.AutoLogoffRequested, s.ID)
		a.appendEventLocked("error", "auto_logoff_error", displayUser(s), fmt.Sprintf("Automatisches Abmelden von Sitzung %d fehlgeschlagen: %v", s.ID, err))
	} else {
		a.appendEventLocked("warning", "auto_logoff_requested", displayUser(s), fmt.Sprintf("Abmeldung der getrennten Sitzung %d ausgelöst; Profilsicherung folgt nach Sitzungsende", s.ID))
	}
	_ = a.store.save(a.state)
}

func (a *App) executeSessionCommand(cmd model.SessionCommand) model.CommandResult {
	res := model.CommandResult{ID: cmd.ID, Action: cmd.Action, SessionID: cmd.SessionID, PID: cmd.PID, CompletedAt: time.Now().UTC()}
	policy := a.policy()
	action := strings.ToLower(strings.TrimSpace(cmd.Action))
	if action != "restart_server" && !policy.Sessions.ControlEnabled {
		res.Error = "session control is disabled by policy"
		return res
	}
	if !cmd.ExpiresAt.IsZero() && time.Now().After(cmd.ExpiresAt) {
		res.Error = "command expired"
		return res
	}
	var err error
	switch action {
	case "logoff":
		err = windowsx.LogoffSession(cmd.SessionID)
	case "disconnect":
		err = windowsx.DisconnectSession(cmd.SessionID)
	case "message":
		if strings.TrimSpace(cmd.Message) == "" {
			err = fmt.Errorf("message is empty")
		} else {
			err = windowsx.SendMessage(cmd.SessionID, cmd.Title, cmd.Message)
		}
	case "kill_process":
		if cmd.PID == 0 || cmd.SessionID == 0 {
			err = fmt.Errorf("pid and session id are required")
		} else {
			// Re-resolve PID ownership immediately before termination. Windows can
			// reuse a PID while a queued command is in flight; never terminate it
			// if it no longer belongs to the expected RDS session.
			processes, listErr := windowsx.Processes()
			if listErr != nil {
				err = fmt.Errorf("verify process: %w", listErr)
			} else {
				matched := false
				for _, process := range processes {
					if process.PID == cmd.PID && process.SessionID == cmd.SessionID {
						matched = true
						break
					}
				}
				if !matched {
					err = fmt.Errorf("process %d no longer belongs to session %d", cmd.PID, cmd.SessionID)
				} else {
					err = windowsx.TerminateProcess(cmd.PID)
				}
			}
		}
	case "restart_server":
		err = windowsx.RestartServer(emptyAs(cmd.Message, "SessionGuard maintenance restart"))
	default:
		err = fmt.Errorf("unsupported session action %q", cmd.Action)
	}
	res.Success = err == nil
	if err != nil {
		res.Error = err.Error()
	}
	a.mu.Lock()
	level := "info"
	if err != nil {
		level = "error"
	}
	a.appendEventLocked(level, "session_command", "", fmt.Sprintf("Aktion %s für Session %d/PID %d durch %s: %s", cmd.Action, cmd.SessionID, cmd.PID, emptyAs(cmd.RequestedBy, "lokal"), resultText(err)))
	_ = a.store.save(a.state)
	a.mu.Unlock()
	return res
}

func (a *App) processCommands(commands []model.SessionCommand) {
	for _, cmd := range commands {
		a.mu.RLock()
		_, done := a.state.ProcessedCommands[cmd.ID]
		a.mu.RUnlock()
		if done || cmd.ID == "" {
			continue
		}
		res := a.executeSessionCommand(cmd)
		a.mu.Lock()
		a.state.ProcessedCommands[cmd.ID] = time.Now().UTC()
		a.state.CommandResults = append(a.state.CommandResults, res)
		if len(a.state.CommandResults) > 100 {
			a.state.CommandResults = append([]model.CommandResult(nil), a.state.CommandResults[len(a.state.CommandResults)-100:]...)
		}
		cutoff := time.Now().UTC().Add(-48 * time.Hour)
		for id, t := range a.state.ProcessedCommands {
			if t.Before(cutoff) {
				delete(a.state.ProcessedCommands, id)
			}
		}
		_ = a.store.save(a.state)
		a.mu.Unlock()
	}
}

func (a *App) cleanupExcluded(s model.Session) bool {
	return excludedBy(s, a.state.Policy.Cleanup.ExcludeUsers, a.state.Policy.Cleanup.ExcludeSIDs)
}
func (a *App) profileExcluded(s model.Session) bool {
	return excludedBy(s, a.state.Policy.Profiles.ExcludeUsers, a.state.Policy.Profiles.ExcludeSIDs)
}
func (a *App) sessionExcluded(s model.Session) bool {
	return excludedBy(s, a.state.Policy.Sessions.ExcludeUsers, a.state.Policy.Sessions.ExcludeSIDs)
}

func excludedBy(s model.Session, users, sids []string) bool {
	if excludedIdentity(displayUser(s), s.SID, users, sids) {
		return true
	}
	for _, u := range users {
		if strings.EqualFold(strings.TrimSpace(u), s.User) {
			return true
		}
	}
	return false
}
func excludedIdentity(user, sid string, users, sids []string) bool {
	plainUser := user
	if i := strings.LastIndexAny(plainUser, `\/`); i >= 0 && i+1 < len(plainUser) {
		plainUser = plainUser[i+1:]
	}
	for _, u := range users {
		u = strings.TrimSpace(u)
		if strings.EqualFold(u, user) || strings.EqualFold(u, plainUser) {
			return true
		}
	}
	for _, x := range sids {
		x = strings.TrimSpace(x)
		if x != "" && (strings.EqualFold(sid, x) || strings.HasPrefix(strings.ToUpper(sid), strings.ToUpper(x)+"-")) {
			return true
		}
	}
	return false
}

func safeProfilePath(profilePath string, roots []string) bool {
	clean, err := filepath.Abs(filepath.Clean(profilePath))
	if err != nil {
		return false
	}
	for _, root := range roots {
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
func profileJobSlice(m map[string]model.ProfileJob) []model.ProfileJob {
	out := make([]model.ProfileJob, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
func eventSlice(events []model.AgentEvent) []model.AgentEvent {
	return append([]model.AgentEvent(nil), events...)
}
func resultSlice(results []model.CommandResult) []model.CommandResult {
	return append([]model.CommandResult(nil), results...)
}
func cloneProfileStatus(in map[string]model.ProfileStatus) map[string]model.ProfileStatus {
	out := make(map[string]model.ProfileStatus, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func backupJobID(sid string) string { return "backup:" + sid }
func restoreJobID(id uint32) string { return "restore:" + strconv.FormatUint(uint64(id), 10) }
func emptyAs(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}
func resultText(err error) string {
	if err == nil {
		return "erfolgreich"
	}
	return err.Error()
}
func (a *App) appendEventLocked(level, typ, user, message string) {
	a.state.Events = append(a.state.Events, model.AgentEvent{Time: time.Now().UTC(), Level: level, Type: typ, User: user, Message: message})
	if len(a.state.Events) > 500 {
		a.state.Events = append([]model.AgentEvent(nil), a.state.Events[len(a.state.Events)-500:]...)
	}
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (a *App) policy() model.Policy { a.mu.RLock(); defer a.mu.RUnlock(); return a.state.Policy }

func (a *App) refreshSnapshot(server model.ServerInfo, sessions []model.Session, processes []model.ProcessInfo, health model.HealthStatus, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range sessions {
		if t, ok := a.state.DisconnectedSince[sessions[i].ID]; ok {
			tt := t
			sessions[i].DisconnectedSince = &tt
		}
	}
	telemetry := make(map[uint32]model.SessionTelemetry, len(a.state.Telemetry))
	for id, t := range a.state.Telemetry {
		telemetry[id] = t
	}
	a.snapshot = model.AgentSnapshot{ProtocolVersion: model.ProtocolVersion, AgentID: a.state.AgentID, Server: server, Health: health, Sessions: sessions, Processes: processes, Telemetry: telemetry, PendingCleanup: pendingSlice(a.state.Pending), ProfileJobs: profileJobSlice(a.state.ProfileJobs), ProfileStatus: cloneProfileStatus(a.state.ProfileStatus), Events: eventSlice(a.state.Events), CommandResults: resultSlice(a.state.CommandResults), Policy: a.state.Policy, PolicyRevision: a.state.Policy.Revision, AgentVersion: Version, Time: now}
}

func (a *App) calculateHealth(server model.ServerInfo) model.HealthStatus {
	h := model.HealthStatus{Score: 100, CalculatedAt: time.Now().UTC(), RDPListenerOK: true, ProfileStoreOK: true}
	add := func(name string, ok bool, message string, penalty int) {
		h.Checks = append(h.Checks, model.HealthCheck{Name: name, OK: ok, Message: message})
		if !ok {
			h.Score -= penalty
		}
	}
	memPct := 0.0
	if server.MemoryTotal > 0 {
		memPct = float64(server.MemoryTotal-server.MemoryAvailable) * 100 / float64(server.MemoryTotal)
	}
	add("cpu", server.CPUPercent < 95, fmt.Sprintf("%.1f%%", server.CPUPercent), 20)
	add("memory", memPct < 95, fmt.Sprintf("%.1f%%", memPct), 20)
	diskOK := server.DiskTotal == 0 || server.DiskFree >= 5<<30
	add("system_disk", diskOK, fmt.Sprintf("%d GiB free", server.DiskFree>>30), 20)
	conn, err := net.DialTimeout("tcp", "127.0.0.1:3389", 750*time.Millisecond)
	if err == nil {
		_ = conn.Close()
	}
	h.RDPListenerOK = err == nil
	add("rdp_listener", h.RDPListenerOK, resultText(err), 30)
	p := a.policy().Profiles
	if p.Enabled && strings.TrimSpace(p.StoreRoot) != "" {
		_, err = os.Stat(p.StoreRoot)
		h.ProfileStoreOK = err == nil
		add("profile_store", h.ProfileStoreOK, resultText(err), 30)
	}
	if h.Score < 0 {
		h.Score = 0
	}
	return h
}

func (a *App) sendHeartbeat(ctx context.Context) {
	if a.cfg.MasterURL == "" {
		return
	}
	a.mu.RLock()
	id, token := a.state.AgentID, a.state.AgentToken
	snap := a.snapshot
	a.mu.RUnlock()
	if id == "" || token == "" {
		server, _ := windowsx.Server()
		mid, _ := windowsx.MachineID()
		resp, err := a.client.enroll(ctx, model.EnrollRequest{EnrollmentToken: a.cfg.EnrollmentToken, Name: server.Hostname, MachineID: mid})
		if err != nil {
			a.mu.Lock()
			a.masterErr = err.Error()
			a.mu.Unlock()
			log.Printf("master enroll: %v", err)
			return
		}
		a.mu.Lock()
		a.state.AgentID, a.state.AgentToken = resp.AgentID, resp.Token
		id, token = resp.AgentID, resp.Token
		_ = a.store.save(a.state)
		a.mu.Unlock()
	}
	snap.AgentID = id
	resp, err := a.client.heartbeat(ctx, id, token, snap)
	if err != nil {
		a.mu.Lock()
		a.masterErr = err.Error()
		a.mu.Unlock()
		log.Printf("master heartbeat: %v", err)
		return
	}
	a.mu.Lock()
	a.lastMasterOK = time.Now().UTC()
	a.masterErr = ""
	changed := false
	if resp.DesiredPolicy != nil && resp.DesiredPolicy.Revision != "" && resp.DesiredPolicy.Revision != a.state.Policy.Revision {
		p := *resp.DesiredPolicy
		config.NormalizePolicy(&p)
		if err := config.ValidatePolicy(p); err != nil {
			a.appendEventLocked("error", "policy_rejected", "", fmt.Sprintf("Master-Policy verworfen: %v", err))
		} else {
			a.state.Policy = p
			changed = true
			a.appendEventLocked("info", "policy_applied", "", fmt.Sprintf("Master-Policy %s angewendet", p.Revision))
		}
	}
	_ = a.store.save(a.state)
	current := make([]model.Session, 0, len(a.state.LastSessions))
	if changed {
		for _, s := range a.state.LastSessions {
			current = append(current, s)
		}
	}
	a.mu.Unlock()
	if changed {
		for _, s := range current {
			if s.SID != "" && s.User != "" {
				a.applyTemplates(s)
			}
		}
	}
	a.processCommands(resp.Commands)
}

func (a *App) serveHTTP(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, 200, map[string]any{"ok": true, "version": Version})
	})
	mux.HandleFunc("GET /metrics", a.metricsAPI)
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
	mux.Handle("POST /api/v1/sessions/{id}/action", secure(http.HandlerFunc(a.sessionActionAPI)))
	server := &http.Server{Addr: a.cfg.Listen, Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second}
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

func (a *App) metricsAPI(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	s := a.snapshot
	a.mu.RUnlock()
	active := 0
	disconnected := 0
	for _, x := range s.Sessions {
		if x.State == "Active" {
			active++
		}
		if x.State == "Disconnected" {
			disconnected++
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "sessionguard_agent_up 1\nsessionguard_sessions_total %d\nsessionguard_sessions_active %d\nsessionguard_sessions_disconnected %d\nsessionguard_profile_jobs %d\nsessionguard_cleanup_jobs %d\n", len(s.Sessions), active, disconnected, len(s.ProfileJobs), len(s.PendingCleanup))
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
	config.NormalizePolicy(&p)
	if err := config.ValidatePolicy(p); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	p.Revision = newRevision()
	p.UpdatedAt = time.Now().UTC()
	a.mu.Lock()
	a.state.Policy = p
	a.appendEventLocked("info", "policy_local_update", "", "Lokale Policy über WebUI geändert")
	_ = a.store.save(a.state)
	sessions := make([]model.Session, 0, len(a.state.LastSessions))
	for _, s := range a.state.LastSessions {
		sessions = append(sessions, s)
	}
	a.mu.Unlock()
	for _, s := range sessions {
		if s.SID != "" && s.User != "" {
			a.applyTemplates(s)
		}
	}
	httpx.JSON(w, 200, p)
}
func (a *App) sessionActionAPI(w http.ResponseWriter, r *http.Request) {
	if !httpx.SameOrigin(r) {
		httpx.Error(w, 403, "cross-origin request rejected")
		return
	}
	id64, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		httpx.Error(w, 400, "invalid session id")
		return
	}
	var req model.SessionActionRequest
	if err := httpx.DecodeJSON(r, &req, 64<<10); err != nil {
		httpx.Error(w, 400, err.Error())
		return
	}
	u, _ := auth.UserFrom(r)
	actor := u.Email
	if actor == "" {
		actor = u.Name
	}
	cmd := model.SessionCommand{ID: "local-" + newRevision(), Action: req.Action, SessionID: uint32(id64), Title: req.Title, Message: req.Message, RequestedBy: actor, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}
	res := a.executeSessionCommand(cmd)
	if !res.Success {
		httpx.Error(w, 409, res.Error)
		return
	}
	httpx.JSON(w, 200, res)
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
