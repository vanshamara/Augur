package persist

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
)

const version = 1

type FileStore struct {
	path     string
	policyID string
	backends []core.BackendID
	now      func() time.Time
}

type FileConfig struct {
	Path     string
	PolicyID string
	Backends []core.BackendID
	Now      func() time.Time
}

type fileState struct {
	Version  int                    `json:"version"`
	SavedAt  time.Time              `json:"saved_at"`
	PolicyID string                 `json:"policy_id"`
	Backends []core.BackendID       `json:"backends"`
	Reward   control.LinearSnapshot `json:"reward"`
	Quality  control.LinearSnapshot `json:"quality"`
}

func NewFileStore(config FileConfig) (*FileStore, error) {
	if config.Path == "" {
		return nil, errors.New("persistence path is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &FileStore{
		path:     config.Path,
		policyID: config.PolicyID,
		backends: append([]core.BackendID(nil), config.Backends...),
		now:      config.Now,
	}, nil
}

func (s *FileStore) Load() (control.LearnedState, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return control.LearnedState{}, err
	}

	var state fileState
	if err := json.Unmarshal(data, &state); err != nil {
		return control.LearnedState{}, err
	}
	if err := s.validate(state); err != nil {
		return control.LearnedState{}, err
	}
	return control.LearnedState{Reward: state.Reward, Quality: state.Quality}, nil
}

func (s *FileStore) Save(state control.LearnedState) error {
	payload := fileState{
		Version:  version,
		SavedAt:  s.now(),
		PolicyID: s.policyID,
		Backends: append([]core.BackendID(nil), s.backends...),
		Reward:   state.Reward,
		Quality:  state.Quality,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".augur-state-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, s.path)
}

func (s *FileStore) validate(state fileState) error {
	if state.Version != version {
		return fmt.Errorf("unsupported learned state version %d", state.Version)
	}
	if s.policyID != "" && state.PolicyID != "" && state.PolicyID != s.policyID {
		return fmt.Errorf("learned state policy %q does not match %q", state.PolicyID, s.policyID)
	}
	if len(s.backends) > 0 && len(state.Backends) > 0 && !sameBackends(s.backends, state.Backends) {
		return errors.New("learned state backends do not match config")
	}
	return nil
}

func sameBackends(a, b []core.BackendID) bool {
	left := backendStrings(a)
	right := backendStrings(b)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func backendStrings(ids []core.BackendID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	sort.Strings(out)
	return out
}
