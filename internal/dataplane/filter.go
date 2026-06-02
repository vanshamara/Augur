package dataplane

import "github.com/vanshamara/Augur/internal/core"

type Release func()

type Filter interface {
	Name() string
	Apply(req core.Request, candidates []core.BackendID) []core.BackendID
	Acquire(id core.BackendID) (Release, bool)
	Observe(id core.BackendID, resp core.Response, err error)
}

type filterBase struct{}

func (filterBase) Acquire(core.BackendID) (Release, bool) {
	return func() {}, true
}

func (filterBase) Observe(core.BackendID, core.Response, error) {
}

func copyCandidates(candidates []core.BackendID) []core.BackendID {
	out := make([]core.BackendID, len(candidates))
	copy(out, candidates)
	return out
}
