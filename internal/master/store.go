package master

import (
	"encoding/json"
	"errors"
	"os"
	"sync"

	"github.com/example/sessionguard/internal/config"
	"github.com/example/sessionguard/internal/model"
)

type data struct {
	Agents map[string]model.AgentRecord `json:"agents"`
}

type store struct {
	path string
	mu   sync.RWMutex
	data data
}

func newStore(path string) (*store, error) {
	s := &store{path: path, data: data{Agents: map[string]model.AgentRecord{}}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, err
	}
	if s.data.Agents == nil {
		s.data.Agents = map[string]model.AgentRecord{}
	}
	return s, nil
}
func (s *store) saveLocked() error { return config.SaveJSON(s.path, s.data) }
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
