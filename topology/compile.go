package topology

import "sort"

// CompileNodeRoutes builds the final weighted backend table for one node.
//
// EndpointSlices are merged and de-duplicated first. A nil allocation is
// treated as "no hints", which always produces global routing.
func CompileNodeRoutes(
	cfg ServiceConfig,
	node, zone string,
	allocation *Allocation,
	slices []EndpointSlice,
) *RouteTable {
	endpoints := MergeSlices(slices)
	return compile(cfg, node, zone, allocation, endpoints)
}

// CompileNodeRoutesMerged is CompileNodeRoutes for callers that already merged
// their EndpointSlices (for example via MergeSlices once for the whole mesh).
func CompileNodeRoutesMerged(
	cfg ServiceConfig,
	node, zone string,
	allocation *Allocation,
	endpoints []Endpoint,
) *RouteTable {
	merged := append([]Endpoint(nil), endpoints...)
	sort.Slice(merged, func(i, j int) bool { return merged[i].IP < merged[j].IP })
	return compile(cfg, node, zone, allocation, merged)
}

func compile(
	cfg ServiceConfig,
	node, zone string,
	allocation *Allocation,
	endpoints []Endpoint,
) *RouteTable {
	table := &RouteTable{Node: node, Zone: zone}

	if cfg.InternalTrafficPolicy == InternalTrafficPolicyLocal {
		table.Local = true
		local := filterNode(endpoints, node)
		ready, terminating := splitRoutable(local)

		if len(ready) > 0 {
			table.Backends = toBackends(ready, zone, false)
			return table
		}
		if len(terminating) > 0 {
			table.FallbackTerminating = true
			table.Backends = toBackends(terminating, zone, true)
			return table
		}
		return table
	}

	ready, terminating := splitRoutable(endpoints)

	// Zone-hinted routing requires both active hints and a ready endpoint whose
	// hint selects this node's zone.
	hintedReady := hintMatchedReady(ready, allocation, zone)
	if len(hintedReady) > 0 {
		table.HintsApplied = true
		table.Backends = toBackends(hintedReady, zone, false)
		return table
	}

	// Global ready fallback (covers missing hints and empty local hint sets).
	if len(ready) > 0 {
		table.Backends = toBackends(ready, "", false)
		return table
	}

	// No ready endpoint cluster-wide: graceful-shutdown endpoints become the
	// last-resort backends, honoring zone hints when usable.
	hintedTerminating := hintMatchedTerminating(terminating, allocation, zone)
	if len(hintedTerminating) > 0 {
		table.HintsApplied = true
		table.FallbackTerminating = true
		table.Backends = toBackends(hintedTerminating, zone, true)
		return table
	}

	if len(terminating) > 0 {
		table.FallbackTerminating = true
		table.Backends = toBackends(terminating, "", true)
	}
	return table
}

// filterNode keeps endpoints co-located with the target node.
func filterNode(endpoints []Endpoint, node string) []Endpoint {
	result := make([]Endpoint, 0)
	for _, ep := range endpoints {
		if ep.Node == node {
			result = append(result, ep)
		}
	}
	return result
}

// splitRoutable partitions endpoints into ready backends and
// serving+terminating fallback backends. Failed endpoints
// (ready=false, serving=false) and not-serving endpoints are dropped.
func splitRoutable(endpoints []Endpoint) (ready, terminating []Endpoint) {
	for _, ep := range endpoints {
		switch {
		case ep.Conditions.Ready:
			ready = append(ready, ep)
		case ep.Conditions.Serving && ep.Conditions.Terminating:
			terminating = append(terminating, ep)
		default:
			// ready=false && serving=false (or not terminating): failed.
		}
	}
	return ready, terminating
}

func hintMatchedReady(ready []Endpoint, allocation *Allocation, zone string) []Endpoint {
	if allocation == nil || !allocation.Active {
		return nil
	}
	matched := make([]Endpoint, 0)
	for _, ep := range ready {
		hint, ok := allocation.Hints[ep.IP]
		if ok && containsZone(hint.ForZones, zone) {
			matched = append(matched, ep)
		}
	}
	return matched
}

func hintMatchedTerminating(terminating []Endpoint, allocation *Allocation, zone string) []Endpoint {
	if allocation == nil || !allocation.Active {
		return nil
	}
	matched := make([]Endpoint, 0)
	for _, ep := range terminating {
		hint, ok := allocation.Hints[ep.IP]
		if ok && containsZone(hint.ForZones, zone) {
			matched = append(matched, ep)
		}
	}
	return matched
}

func containsZone(zones []string, zone string) bool {
	for _, candidate := range zones {
		if candidate == zone {
			return true
		}
	}
	return false
}

func toBackends(endpoints []Endpoint, selectedZone string, terminating bool) []Backend {
	backends := make([]Backend, 0, len(endpoints))
	for _, ep := range endpoints {
		weight := ep.Weight
		if weight <= 0 {
			weight = 1
		}
		zone := selectedZone
		if zone == "" {
			zone = ep.Zone
		}
		backends = append(backends, Backend{
			Endpoint:     ep,
			Weight:       weight,
			Terminating:  terminating,
			SelectedZone: zone,
		})
	}
	sort.Slice(backends, func(i, j int) bool {
		return backends[i].Endpoint.IP < backends[j].Endpoint.IP
	})
	return backends
}
