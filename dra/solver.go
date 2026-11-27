package dra

import (
	"fmt"
	"sort"
)

// solver runs the structured-device allocation for one claim on one node.
// It performs exhaustive backtracking so that a locally greedy choice can
// never lock the search out of a globally valid combination.
type solver struct {
	nodeName string
	classes  map[string]*DeviceClass
	pools    []poolSnapshot
	occupied map[string]struct{}
	claim    *ResourceClaim
}

type requestPlan struct {
	request    DeviceRequest
	selectors  []Selector
	candidates []deviceView
	// allGroups holds one option per pool for AllocationModeAll: every
	// matching device of a single pool is taken together.
	allGroups [][]deviceView
}

// solve validates the claim, builds per-request candidate sets and runs the
// backtracking search. Occupied device keys (devices already allocated to
// other claims on the node) are excluded up front.
func (s *solver) solve() (*AllocationResult, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}

	plans := make([]requestPlan, len(s.claim.Requests))
	for i, req := range s.claim.Requests {
		plan, err := s.buildPlan(req)
		if err != nil {
			return nil, err
		}
		plans[i] = plan
	}

	used := make(map[string]struct{}, len(s.occupied))
	for key := range s.occupied {
		used[key] = struct{}{}
	}
	chosen := make([][]DeviceResult, len(plans))

	if !s.dfs(plans, 0, used, chosen) {
		return nil, fmt.Errorf("%w: node %q cannot satisfy claim %s",
			ErrNoDevices, s.nodeName, s.claim.Key())
	}

	results := make([]RequestResult, len(plans))
	for i, plan := range plans {
		results[i] = RequestResult{Request: plan.request.Name, Devices: chosen[i]}
	}
	return &AllocationResult{Node: s.nodeName, Results: results}, nil
}

func (s *solver) validate() error {
	if len(s.claim.Requests) == 0 {
		return fmt.Errorf("%w: claim %s has no device requests", ErrInvalidSpec, s.claim.Key())
	}
	names := map[string]struct{}{}
	for _, req := range s.claim.Requests {
		if req.Name == "" {
			return fmt.Errorf("%w: request with empty name in claim %s", ErrInvalidSpec, s.claim.Key())
		}
		if _, dup := names[req.Name]; dup {
			return fmt.Errorf("%w: duplicate request name %q", ErrInvalidSpec, req.Name)
		}
		names[req.Name] = struct{}{}
		switch req.AllocationMode {
		case AllocationModeAll:
		case AllocationModeExactCount:
			if req.Count <= 0 {
				return fmt.Errorf("%w: ExactCount request %q requires a positive count",
					ErrInvalidSpec, req.Name)
			}
		default:
			return fmt.Errorf("%w: request %q has unknown allocation mode %q",
				ErrInvalidSpec, req.Name, req.AllocationMode)
		}
	}
	for _, con := range s.claim.Constraints {
		if con.MatchAttribute == nil {
			continue
		}
		if con.MatchAttribute.Attribute == "" || len(con.MatchAttribute.Requests) == 0 {
			return fmt.Errorf("%w: malformed MatchAttribute constraint", ErrInvalidSpec)
		}
		for _, name := range con.MatchAttribute.Requests {
			if _, ok := names[name]; !ok {
				return fmt.Errorf("%w: MatchAttribute references unknown request %q",
					ErrInvalidSpec, name)
			}
		}
	}
	return nil
}

func (s *solver) buildPlan(req DeviceRequest) (requestPlan, error) {
	class, ok := s.classes[req.ClassName]
	if !ok {
		return requestPlan{}, fmt.Errorf("%w: device class %q", ErrNotFound, req.ClassName)
	}
	selectors := append(append([]Selector{}, class.Selectors...), req.Selectors...)

	plan := requestPlan{request: req, selectors: selectors}
	perPool := map[string][]deviceView{}
	var poolOrder []string

	for _, pool := range s.pools {
		// Incomplete pools are already filtered upstream; keep the guard
		// defensive so no partial view can ever reach the solver.
		if !pool.complete {
			continue
		}
		for _, view := range pool.devices {
			match, err := deviceMatches(view.device, selectors)
			if err != nil {
				return requestPlan{}, err
			}
			if !match {
				continue
			}
			plan.candidates = append(plan.candidates, view)
			if req.AllocationMode == AllocationModeAll {
				if _, seen := perPool[pool.poolName]; !seen {
					poolOrder = append(poolOrder, pool.poolName)
				}
				perPool[pool.poolName] = append(perPool[pool.poolName], view)
			}
		}
	}

	sort.Slice(plan.candidates, func(i, j int) bool {
		return plan.candidates[i].result().Key() < plan.candidates[j].result().Key()
	})

	switch req.AllocationMode {
	case AllocationModeExactCount:
		available := 0
		for _, view := range plan.candidates {
			if _, busy := s.occupied[view.result().Key()]; !busy {
				available++
			}
		}
		if int64(available) < req.Count {
			return requestPlan{}, fmt.Errorf("%w: request %q needs %d devices, %d are available",
				ErrNoDevices, req.Name, req.Count, available)
		}
	case AllocationModeAll:
		sort.Strings(poolOrder)
		for _, name := range poolOrder {
			plan.allGroups = append(plan.allGroups, perPool[name])
		}
		if len(plan.allGroups) == 0 {
			return requestPlan{}, fmt.Errorf("%w: request %q found no matching pool",
				ErrNoDevices, req.Name)
		}
	}
	return plan, nil
}

// dfs assigns requests in order, enumerating every legal device combination
// and pruning branches that violate mutual exclusion or MatchAttribute.
func (s *solver) dfs(plans []requestPlan, idx int, used map[string]struct{}, chosen [][]DeviceResult) bool {
	if idx == len(plans) {
		return true
	}
	plan := plans[idx]

	enumerate := func(option []deviceView) bool {
		results := make([]DeviceResult, 0, len(option))
		for _, view := range option {
			key := view.result().Key()
			if _, clash := used[key]; clash {
				return false
			}
			results = append(results, view.result())
		}
		for _, res := range results {
			used[res.Key()] = struct{}{}
		}
		chosen[idx] = results
		if s.constraintsHold(plans, chosen, idx) && s.dfs(plans, idx+1, used, chosen) {
			return true
		}
		for _, res := range results {
			delete(used, res.Key())
		}
		return false
	}

	if plan.request.AllocationMode == AllocationModeAll {
		for _, group := range plan.allGroups {
			if enumerate(group) {
				return true
			}
		}
		return false
	}

	// ExactCount: lazily enumerate every count-sized candidate combination.
	need := int(plan.request.Count)
	combo := make([]deviceView, 0, need)
	var walk func(start int) bool
	walk = func(start int) bool {
		if len(combo) == need {
			return enumerate(append([]deviceView(nil), combo...))
		}
		for i := start; i < len(plan.candidates); i++ {
			view := plan.candidates[i]
			if _, clash := used[view.result().Key()]; clash {
				continue
			}
			combo = append(combo, view)
			if walk(i + 1) {
				return true
			}
			combo = combo[:len(combo)-1]
		}
		return false
	}
	return walk(0)
}

// constraintsHold re-evaluates every MatchAttribute constraint over the
// requests assigned so far. All selected devices must expose the attribute
// and share one identical value.
func (s *solver) constraintsHold(plans []requestPlan, chosen [][]DeviceResult, lastIdx int) bool {
	deviceByKey := s.deviceIndex(plans)
	for _, con := range s.claim.Constraints {
		mat := con.MatchAttribute
		if mat == nil {
			continue
		}
		var want AttributeValue
		haveWant := false
		for _, reqName := range mat.Requests {
			reqIdx := s.requestIndex(mat, reqName)
			if reqIdx > lastIdx || reqIdx < 0 {
				continue
			}
			for _, res := range chosen[reqIdx] {
				view, ok := deviceByKey[res.Key()]
				if !ok {
					return false
				}
				val, ok := view.device.Attributes[mat.Attribute]
				if !ok {
					return false
				}
				if !haveWant {
					want = val
					haveWant = true
					continue
				}
				eq, err := valuesEqual(want, val)
				if err != nil || !eq {
					return false
				}
			}
		}
	}
	return true
}

func (s *solver) requestIndex(_ *MatchAttributeConstraint, name string) int {
	for i, req := range s.claim.Requests {
		if req.Name == name {
			return i
		}
	}
	return -1
}

func (s *solver) deviceIndex(plans []requestPlan) map[string]deviceView {
	out := map[string]deviceView{}
	for _, plan := range plans {
		for _, view := range plan.candidates {
			out[view.result().Key()] = view
		}
	}
	return out
}
