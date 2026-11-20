package recovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type persistedContainer struct {
	Failures   int         `json:"failures"`
	Last       time.Time   `json:"last"`
	Attempts   []time.Time `json:"attempts"`
	Limited    bool        `json:"limited"`
	Recovering bool        `json:"recovering"`
}

func (s *Service) LoadState() error {
	if s.StatePath == "" {
		return nil
	}
	body, err := os.ReadFile(s.StatePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	persisted := map[string]persistedContainer{}
	if err := json.Unmarshal(body, &persisted); err != nil {
		return err
	}
	s.mu.Lock()
	for name, state := range persisted {
		s.state[name] = &containerState{failures: state.Failures, last: state.Last, attempts: state.Attempts, limited: state.Limited, recovering: state.Recovering}
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) persistLocked() {
	if s.StatePath == "" {
		return
	}
	persisted := map[string]persistedContainer{}
	for name, state := range s.state {
		persisted[name] = persistedContainer{Failures: state.failures, Last: state.last, Attempts: state.attempts, Limited: state.limited, Recovering: state.recovering}
	}
	body, err := json.Marshal(persisted)
	if err != nil {
		s.log("recovery state: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.StatePath), 0o700); err != nil {
		s.log("recovery state: %v", err)
		return
	}
	tmp := s.StatePath + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		s.log("recovery state: %v", err)
		return
	}
	if err := os.Rename(tmp, s.StatePath); err != nil {
		s.log("recovery state: %v", err)
	}
}
