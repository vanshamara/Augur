package dataplane

import "github.com/vanshamara/Augur/internal/core"

func normalizeCapabilities(ids []core.BackendID, configured map[core.BackendID][]core.RequestType) map[core.BackendID]map[core.RequestType]bool {
	out := make(map[core.BackendID]map[core.RequestType]bool, len(ids))
	for _, id := range ids {
		capabilities := configured[id]
		if len(capabilities) == 0 {
			capabilities = allRequestTypes()
		}
		out[id] = requestTypeSet(capabilities)
	}
	return out
}

func (g *Gateway) compatibleCandidates(req core.Request, candidates []core.BackendID) []core.BackendID {
	requestType := requestTypeForCapabilities(req)
	out := make([]core.BackendID, 0, len(candidates))
	for _, id := range candidates {
		if g.supportsRequestType(id, requestType) {
			out = append(out, id)
		}
	}
	return out
}

func (g *Gateway) supportsRequestType(id core.BackendID, requestType core.RequestType) bool {
	capabilities := g.capabilities[id]
	if len(capabilities) == 0 {
		return true
	}
	return capabilities[requestType]
}

func requestTypeForCapabilities(req core.Request) core.RequestType {
	if req.Features.Type == "" {
		return core.Chat
	}
	return req.Features.Type
}

func requestTypeSet(values []core.RequestType) map[core.RequestType]bool {
	out := make(map[core.RequestType]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func allRequestTypes() []core.RequestType {
	return []core.RequestType{
		core.Chat,
		core.Reasoning,
		core.Coding,
		core.Embedding,
	}
}
