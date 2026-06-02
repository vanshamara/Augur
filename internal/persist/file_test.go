package persist

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
)

func TestFileStoreSavesAndLoadsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewFileStore(FileConfig{
		Path:     path,
		PolicyID: "prod",
		Backends: []core.BackendID{"a"},
		Now: func() time.Time {
			return time.Unix(123, 0)
		},
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	state := learnedState("a", 2)

	if err := store.Save(state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	if loaded.Reward.Arms["a"].Updates != 2 {
		t.Fatalf("reward updates got %v", loaded.Reward.Arms["a"].Updates)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat state: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode got %v", info.Mode().Perm())
	}
}

func TestFileStoreRejectsPolicyMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	first, err := NewFileStore(FileConfig{Path: path, PolicyID: "first", Backends: []core.BackendID{"a"}})
	if err != nil {
		t.Fatalf("new first store: %v", err)
	}
	if err := first.Save(learnedState("a", 1)); err != nil {
		t.Fatalf("save state: %v", err)
	}

	second, err := NewFileStore(FileConfig{Path: path, PolicyID: "second", Backends: []core.BackendID{"a"}})
	if err != nil {
		t.Fatalf("new second store: %v", err)
	}
	if _, err := second.Load(); err == nil {
		t.Fatal("policy mismatch should fail")
	}
}

func TestFileStoreRejectsBackendMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	first, err := NewFileStore(FileConfig{Path: path, PolicyID: "prod", Backends: []core.BackendID{"a"}})
	if err != nil {
		t.Fatalf("new first store: %v", err)
	}
	if err := first.Save(learnedState("a", 1)); err != nil {
		t.Fatalf("save state: %v", err)
	}

	second, err := NewFileStore(FileConfig{Path: path, PolicyID: "prod", Backends: []core.BackendID{"b"}})
	if err != nil {
		t.Fatalf("new second store: %v", err)
	}
	if _, err := second.Load(); err == nil {
		t.Fatal("backend mismatch should fail")
	}
}

func TestBanditRestoresLearnedState(t *testing.T) {
	bandit := control.NewBanditRouter(control.BanditConfig{
		Backends: []core.BackendID{"a"},
	})
	state := learnedState("a", 3)

	bandit.RestoreLearnedState(state)
	restored := bandit.LearnedState()

	if restored.Reward.Arms["a"].Updates != 3 {
		t.Fatalf("restored updates got %v", restored.Reward.Arms["a"].Updates)
	}
}

func learnedState(id core.BackendID, updates float64) control.LearnedState {
	at := time.Unix(123, 0)
	arm := control.LinearArm{
		Precision: []float64{updates},
		Target:    []float64{updates},
		Last:      at,
		Updates:   updates,
	}
	snapshot := control.LinearSnapshot{
		Arms: map[core.BackendID]control.LinearArm{
			id: arm,
		},
	}
	return control.LearnedState{
		Reward:  snapshot,
		Quality: snapshot,
	}
}
