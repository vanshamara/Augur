package learn

import (
	"sync"
	"sync/atomic"
)

type message[O any] struct {
	observation O
	transform   any
	result      chan any
	barrier     chan struct{}
}

// Core holds learned state behind a single writer.
// One goroutine applies every update in the order it was queued, then publishes
// a new snapshot. Readers load the snapshot without a lock, so the read path that
// routing depends on never waits on a write. Apply must return a new value and
// leave the old one untouched, so a reader holding an older snapshot stays valid.
type Core[S, O any] struct {
	apply    func(current S, observation O) S
	snapshot atomic.Pointer[S]
	messages chan message[O]
	wait     sync.WaitGroup
}

// NewCore starts the writer goroutine with an initial snapshot and an apply function.
func NewCore[S, O any](initial S, apply func(S, O) S) *Core[S, O] {
	core := &Core[S, O]{
		apply:    apply,
		messages: make(chan message[O], 1024),
	}
	core.snapshot.Store(&initial)
	core.wait.Add(1)
	go core.run()
	return core
}

func (c *Core[S, O]) run() {
	defer c.wait.Done()
	for msg := range c.messages {
		if msg.barrier != nil {
			close(msg.barrier)
			continue
		}
		if msg.transform != nil {
			current := *c.snapshot.Load()
			next := msg.transform.(func(S) S)(current)
			c.snapshot.Store(&next)
			msg.result <- next
			continue
		}
		current := *c.snapshot.Load()
		next := c.apply(current, msg.observation)
		c.snapshot.Store(&next)
	}
}

// Update queues one observation for the writer. It does not wait for the apply,
// so the caller stays fast.
func (c *Core[S, O]) Update(observation O) {
	c.messages <- message[O]{observation: observation}
}

// Snapshot returns the latest published state without taking a lock.
func (c *Core[S, O]) Snapshot() S {
	return *c.snapshot.Load()
}

func (c *Core[S, O]) Transform(apply func(S) S) S {
	result := make(chan any)
	c.messages <- message[O]{transform: apply, result: result}
	return (<-result).(S)
}

// Flush blocks until the writer has applied every update queued before this call.
// Tests use it to read state at a known point.
func (c *Core[S, O]) Flush() {
	barrier := make(chan struct{})
	c.messages <- message[O]{barrier: barrier}
	<-barrier
}

// Close stops the writer after it drains the queued updates.
func (c *Core[S, O]) Close() {
	close(c.messages)
	c.wait.Wait()
}
