package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/model"
)

type State struct {
	AgentID               string                            `json:"agent_id,omitempty"`
	AgentToken            string                            `json:"agent_token,omitempty"`
	Policy                model.Policy                      `json:"policy"`
	LastSessions          map[uint32]model.Session          `json:"last_sessions,omitempty"`
	Pending               map[string]model.CleanupJob       `json:"pending,omitempty"`
	ProfileJobs           map[string]model.ProfileJob       `json:"profile_jobs,omitempty"`
	ProfileStatus         map[string]model.ProfileStatus    `json:"profile_status,omitempty"`
	DisconnectedSince     map[uint32]time.Time              `json:"disconnected_since,omitempty"`
	AutoLogoffRequested   map[uint32]time.Time              `json:"auto_logoff_requested,omitempty"`
	RestoredSessions      map[uint32]bool                   `json:"restored_sessions,omitempty"`
	ProcessedCommands     map[string]time.Time              `json:"processed_commands,omitempty"`
	CommandResults        []model.CommandResult             `json:"command_results,omitempty"`
	Events                []model.AgentEvent                `json:"events,omitempty"`
	Telemetry             map[uint32]model.SessionTelemetry `json:"telemetry,omitempty"`
	DesiredRemoteApps     []model.RemoteAppSpec             `json:"desired_remote_apps,omitempty"`
	ManagedRemoteApps     map[string]model.RemoteAppSpec    `json:"managed_remote_apps,omitempty"`
	OwnedRemoteAppAliases map[string]bool                   `json:"owned_remote_app_aliases,omitempty"`
	RemoteAppStatus       []model.RemoteAppStatus           `json:"remote_app_status,omitempty"`
	LastRemoteAppSync     time.Time                         `json:"last_remote_app_sync,omitempty"`
}

type stateStore struct {
	path string
	mu   sync.Mutex
}

func loadState(path string, initial model.Policy) (State, error) {
	s := State{
		Policy:                initial,
		LastSessions:          map[uint32]model.Session{},
		Pending:               map[string]model.CleanupJob{},
		ProfileJobs:           map[string]model.ProfileJob{},
		ProfileStatus:         map[string]model.ProfileStatus{},
		DisconnectedSince:     map[uint32]time.Time{},
		AutoLogoffRequested:   map[uint32]time.Time{},
		RestoredSessions:      map[uint32]bool{},
		ProcessedCommands:     map[string]time.Time{},
		CommandResults:        []model.CommandResult{},
		Events:                []model.AgentEvent{},
		Telemetry:             map[uint32]model.SessionTelemetry{},
		DesiredRemoteApps:     []model.RemoteAppSpec{},
		ManagedRemoteApps:     map[string]model.RemoteAppSpec{},
		OwnedRemoteAppAliases: map[string]bool{},
		RemoteAppStatus:       []model.RemoteAppStatus{},
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.LastSessions == nil {
		s.LastSessions = map[uint32]model.Session{}
	}
	if s.Pending == nil {
		s.Pending = map[string]model.CleanupJob{}
	}
	if s.ProfileJobs == nil {
		s.ProfileJobs = map[string]model.ProfileJob{}
	}
	if s.ProfileStatus == nil {
		s.ProfileStatus = map[string]model.ProfileStatus{}
	}
	if s.DisconnectedSince == nil {
		s.DisconnectedSince = map[uint32]time.Time{}
	}
	if s.AutoLogoffRequested == nil {
		s.AutoLogoffRequested = map[uint32]time.Time{}
	}
	if s.ProcessedCommands == nil {
		s.ProcessedCommands = map[string]time.Time{}
	}
	if s.CommandResults == nil {
		s.CommandResults = []model.CommandResult{}
	}
	if s.Events == nil {
		s.Events = []model.AgentEvent{}
	}
	if s.Telemetry == nil {
		s.Telemetry = map[uint32]model.SessionTelemetry{}
	}
	if s.DesiredRemoteApps == nil {
		s.DesiredRemoteApps = []model.RemoteAppSpec{}
	}
	if s.ManagedRemoteApps == nil {
		s.ManagedRemoteApps = map[string]model.RemoteAppSpec{}
	}
	if s.OwnedRemoteAppAliases == nil {
		s.OwnedRemoteAppAliases = map[string]bool{}
	}
	if s.RemoteAppStatus == nil {
		s.RemoteAppStatus = []model.RemoteAppStatus{}
	}
	if s.RestoredSessions == nil {
		// Upgrade safety: do not restore into sessions that were already active before
		// upgrading from a version that did not track per-session restore state.
		s.RestoredSessions = map[uint32]bool{}
		for id := range s.LastSessions {
			s.RestoredSessions[id] = true
		}
	}
	if len(s.Events) > 500 {
		s.Events = append([]model.AgentEvent(nil), s.Events[len(s.Events)-500:]...)
	}
	if len(s.CommandResults) > 100 {
		s.CommandResults = append([]model.CommandResult(nil), s.CommandResults[len(s.CommandResults)-100:]...)
	}
	if s.Policy.Revision == "" {
		s.Policy = initial
	}
	return s, nil
}

func (ss *stateStore) save(s State) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return config.SaveJSON(ss.path, s)
}

func statePath(dataDir string) string { return filepath.Join(dataDir, "state.json") }
