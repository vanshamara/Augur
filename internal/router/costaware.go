package router

import "github.com/vanshamara/Augur/internal/core"

// CostAware sends each request to the backend with the lowest published price per
// token. Prices are known config, so it needs no signals and never changes its mind.
type CostAware struct {
	pricePerToken map[core.BackendID]float64
}

func NewCostAware(pricePerToken map[core.BackendID]float64) *CostAware {
	return &CostAware{pricePerToken: pricePerToken}
}

func (c *CostAware) Name() string {
	return "cost-aware"
}

func (c *CostAware) Pick(req core.Request, candidates []core.BackendID) core.BackendID {
	best := candidates[0]
	bestPrice := c.pricePerToken[best]
	for _, id := range candidates[1:] {
		price := c.pricePerToken[id]
		if price < bestPrice {
			best = id
			bestPrice = price
		}
	}
	return best
}

func (c *CostAware) Observe(choice core.BackendID, resp core.Response) {
}
