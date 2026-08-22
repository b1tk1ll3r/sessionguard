package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/model"
)

type State struct {
	AgentID      string                      `json:"agent_id,omitempty"`
	AgentToken   string                      `json:"agent_token,omitempty"`
	Policy       model.Policy                `json:"policy"`
	LastSessions map[uint32]model.Session    `json:"last_sessions,omitempty"`
	Pending      map[string]model.CleanupJob `json:"pending,omitempty"`
}

type stateStore struct {
	path string
	mu   sync.Mutex
}

func loadState(path string, initial model.Policy) (State, error) {
	s := State{Policy: initial, LastSessions: map[uint32]model.Session{}, Pending: map[string]model.CleanupJob{}}
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
