package clock

import "time"

// Clock gives the current time and timers. Production code uses Real.
// The replay harness uses Virtual so that a run can be repeated exactly.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type Real struct{}

// NewReal returns a clock backed by the operating system time.
func NewReal() *Real {
	return &Real{}
}

func (r *Real) Now() time.Time {
	return time.Now()
}

func (r *Real) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}
