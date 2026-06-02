package clock

import (
	"sort"
	"sync"
	"time"
)

type timer struct {
	deadline time.Time
	fire     chan time.Time
}

// Virtual is a clock whose time only moves when Advance is called.
// This makes harness runs reproducible because nothing depends on the wall clock.
type Virtual struct {
	mu     sync.Mutex
	now    time.Time
	timers []*timer
}

// NewVirtual returns a virtual clock that starts at the given time.
func NewVirtual(start time.Time) *Virtual {
	return &Virtual{now: start}
}

func (v *Virtual) Now() time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.now
}

// After registers a timer and returns a channel that fires once virtual time
// reaches now plus d. The channel is buffered so firing never blocks Advance.
func (v *Virtual) After(d time.Duration) <-chan time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	fire := make(chan time.Time, 1)
	v.timers = append(v.timers, &timer{deadline: v.now.Add(d), fire: fire})
	return fire
}

// Advance moves virtual time forward by d and fires every timer that has
// reached its deadline, in deadline order so the result is deterministic.
func (v *Virtual) Advance(d time.Duration) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.now = v.now.Add(d)
	v.fireExpired()
}

func (v *Virtual) fireExpired() {
	var expired []*timer
	var pending []*timer
	for _, t := range v.timers {
		if t.deadline.After(v.now) {
			pending = append(pending, t)
		} else {
			expired = append(expired, t)
		}
	}
	sort.SliceStable(expired, func(i, j int) bool {
		return expired[i].deadline.Before(expired[j].deadline)
	})
	for _, t := range expired {
		t.fire <- t.deadline
	}
	v.timers = pending
}
