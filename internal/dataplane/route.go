package dataplane

import (
	"strings"

	"github.com/vanshamara/Augur/internal/core"
)

type RouteRule struct {
	Name       string
	Match      RouteMatch
	Candidates []core.BackendID
}

type RouteMatch struct {
	TaskTypes []core.RequestType
	Tenants   []string
	UserTiers []string
}

type RouteDecision struct {
	Name       string
	Candidates []core.BackendID
}

type RouteSelector struct {
	routes []RouteRule
}

func NewRouteSelector(routes []RouteRule) *RouteSelector {
	copied := make([]RouteRule, len(routes))
	for i, route := range routes {
		copied[i] = RouteRule{
			Name:       route.Name,
			Match:      copyRouteMatch(route.Match),
			Candidates: append([]core.BackendID(nil), route.Candidates...),
		}
	}
	return &RouteSelector{routes: copied}
}

func (s *RouteSelector) Select(req core.Request, all []core.BackendID) RouteDecision {
	if s == nil || len(s.routes) == 0 {
		return RouteDecision{Candidates: append([]core.BackendID(nil), all...)}
	}
	for _, route := range s.routes {
		if route.Match.matches(req) {
			return RouteDecision{
				Name:       route.Name,
				Candidates: routeCandidates(route.Candidates, all),
			}
		}
	}
	return RouteDecision{}
}

func (m RouteMatch) matches(req core.Request) bool {
	if !matchesTaskType(m.TaskTypes, req.Features.Type) {
		return false
	}
	if !matchesText(m.Tenants, req.TenantID, false) {
		return false
	}
	return matchesText(m.UserTiers, req.Features.UserTier, true)
}

func matchesTaskType(accepted []core.RequestType, actual core.RequestType) bool {
	if len(accepted) == 0 {
		return true
	}
	for _, value := range accepted {
		if value == actual {
			return true
		}
	}
	return false
}

func matchesText(accepted []string, actual string, fold bool) bool {
	if len(accepted) == 0 {
		return true
	}
	if fold {
		actual = strings.ToLower(strings.TrimSpace(actual))
	} else {
		actual = strings.TrimSpace(actual)
	}
	for _, value := range accepted {
		candidate := strings.TrimSpace(value)
		if fold {
			candidate = strings.ToLower(candidate)
		}
		if candidate == actual {
			return true
		}
	}
	return false
}

func routeCandidates(route []core.BackendID, all []core.BackendID) []core.BackendID {
	available := map[core.BackendID]bool{}
	for _, id := range all {
		available[id] = true
	}
	out := make([]core.BackendID, 0, len(route))
	for _, id := range route {
		if available[id] {
			out = append(out, id)
		}
	}
	return out
}

func copyRouteMatch(match RouteMatch) RouteMatch {
	return RouteMatch{
		TaskTypes: append([]core.RequestType(nil), match.TaskTypes...),
		Tenants:   append([]string(nil), match.Tenants...),
		UserTiers: append([]string(nil), match.UserTiers...),
	}
}
