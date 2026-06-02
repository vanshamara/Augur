package live

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/vanshamara/Augur/internal/control"
	"github.com/vanshamara/Augur/internal/core"
	"github.com/vanshamara/Augur/internal/quality"
	"github.com/vanshamara/Augur/internal/rng"
)

type Gateway interface {
	Call(ctx context.Context, req core.Request) (core.Response, error)
}

type Config struct {
	Gateway   Gateway
	Bandit    *control.BanditRouter
	Scorer    quality.Scorer
	Seed      uint64
	QueueSize int
}

type Learner struct {
	gateway Gateway
	bandit  *control.BanditRouter
	scorer  quality.Scorer
	deriver *rng.Deriver
	work    chan judgeWork
	wait    sync.WaitGroup
	done    chan struct{}
	closed  atomic.Bool
}

type judgeWork struct {
	req  core.Request
	resp core.Response
}

func New(config Config) (*Learner, error) {
	if config.Gateway == nil {
		return nil, errors.New("gateway is required")
	}
	if config.QueueSize <= 0 {
		config.QueueSize = 1024
	}

	learner := &Learner{
		gateway: config.Gateway,
		bandit:  config.Bandit,
		scorer:  config.Scorer,
		deriver: rng.NewDeriver(config.Seed),
		work:    make(chan judgeWork, config.QueueSize),
		done:    make(chan struct{}),
	}
	go learner.run()
	return learner, nil
}

func (l *Learner) Call(ctx context.Context, req core.Request) (core.Response, error) {
	resp, err := l.gateway.Call(ctx, req)
	if err == nil && !resp.Errored {
		l.enqueue(req, resp)
	}
	return resp, err
}

func (l *Learner) Flush() {
	l.wait.Wait()
	if l.bandit != nil {
		l.bandit.Flush()
	}
}

func (l *Learner) Close() {
	if l.closed.CompareAndSwap(false, true) {
		l.Flush()
		close(l.done)
	}
}

func (l *Learner) enqueue(req core.Request, resp core.Response) {
	if l.bandit == nil || l.scorer == nil || l.closed.Load() {
		return
	}

	l.wait.Add(1)
	select {
	case l.work <- judgeWork{req: req, resp: resp}:
	case <-l.done:
		l.wait.Done()
	default:
		l.wait.Done()
	}
}

func (l *Learner) run() {
	for {
		select {
		case work := <-l.work:
			l.handle(work)
			l.wait.Done()
		case <-l.done:
			return
		}
	}
}

func (l *Learner) handle(work judgeWork) {
	record, ok := l.bandit.Attribution().Decision(work.resp.RequestID)
	if !ok || !l.shouldScore(record, work.req, work.resp) {
		return
	}

	result, err := l.scorer.Score(context.Background(), work.req, work.resp)
	if err != nil {
		return
	}
	l.bandit.ObserveQualityWithContext(context.Background(), work.resp.RequestID, result.Score)
}

func (l *Learner) shouldScore(record control.DecisionRecord, req core.Request, resp core.Response) bool {
	propensity := record.JudgingPropensity
	if propensity <= 0 {
		return false
	}
	if propensity >= 1 {
		return true
	}

	gen := l.deriver.Rand(rng.HashKey(req.ID), rng.HashKey(string(resp.Backend)), rng.HashKey("live-judge"))
	return gen.Float64() < propensity
}
