package dataplane

import (
	"sync"

	"github.com/vanshamara/Augur/internal/core"
)

const DefaultTenant = "default"

type TenantLimit struct {
	MaxInFlight int64
	MaxCostUSD  float64
}

type TenantLimitConfig struct {
	DefaultTenant string
	Defaults      TenantLimit
	Tenants       map[string]TenantLimit
}

type TenantLimiter struct {
	mu            sync.Mutex
	defaultTenant string
	defaults      TenantLimit
	tenants       map[string]TenantLimit
	states        map[string]*tenantState
}

type tenantState struct {
	inFlight int64
	costUSD  float64
}

func NewTenantLimiter(config TenantLimitConfig) *TenantLimiter {
	if config.DefaultTenant == "" {
		config.DefaultTenant = DefaultTenant
	}
	tenants := make(map[string]TenantLimit, len(config.Tenants))
	for tenant, limit := range config.Tenants {
		if tenant != "" {
			tenants[tenant] = limit
		}
	}
	return &TenantLimiter{
		defaultTenant: config.DefaultTenant,
		defaults:      config.Defaults,
		tenants:       tenants,
		states:        map[string]*tenantState{},
	}
}

func (l *TenantLimiter) Name() string {
	return "tenant"
}

func (l *TenantLimiter) Apply(req core.Request, candidates []core.BackendID) []core.BackendID {
	return candidates
}

func (l *TenantLimiter) Acquire(req core.Request, id core.BackendID) (Release, bool) {
	tenant := l.tenant(req.TenantID)

	l.mu.Lock()
	defer l.mu.Unlock()

	state := l.state(tenant)
	limit := l.limit(tenant)
	if limit.MaxCostUSD > 0 && state.costUSD >= limit.MaxCostUSD {
		return nil, false
	}
	if limit.MaxInFlight > 0 && state.inFlight >= limit.MaxInFlight {
		return nil, false
	}
	state.inFlight++

	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()

		if state.inFlight > 0 {
			state.inFlight--
		}
	}, true
}

func (l *TenantLimiter) Observe(id core.BackendID, resp core.Response, err error) {
	if resp.CostUSD <= 0 {
		return
	}

	tenant := l.tenant(resp.TenantID)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.state(tenant).costUSD += resp.CostUSD
}

func (l *TenantLimiter) InFlight(tenant string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.state(l.tenant(tenant)).inFlight
}

func (l *TenantLimiter) CostUSD(tenant string) float64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.state(l.tenant(tenant)).costUSD
}

func (l *TenantLimiter) tenant(tenant string) string {
	if tenant == "" {
		return l.defaultTenant
	}
	return tenant
}

func (l *TenantLimiter) limit(tenant string) TenantLimit {
	limit := l.defaults
	override, ok := l.tenants[tenant]
	if !ok {
		return limit
	}
	if override.MaxInFlight > 0 {
		limit.MaxInFlight = override.MaxInFlight
	}
	if override.MaxCostUSD > 0 {
		limit.MaxCostUSD = override.MaxCostUSD
	}
	return limit
}

func (l *TenantLimiter) state(tenant string) *tenantState {
	state := l.states[tenant]
	if state == nil {
		state = &tenantState{}
		l.states[tenant] = state
	}
	return state
}
