package verifier

import (
	"fmt"

	dp "prescreen.local/ddd-9511-20261122-v2"
)

// Verify independently re-derives and checks res for problem p.
func Verify(p *dp.Problem, res *dp.Result) (*Report, error) {
	vm, err := build(p)
	if err != nil {
		return nil, err
	}
	groupCount := len(vm.groups)
	total := uint64(1) << groupCount
	rep := &Report{Subsets: total}
	derivedKind := vm.detectConflicts()
	if derivedKind == "" {
		derivedKind = vm.detectRetainUnreachable()
	}
	if derivedKind != "" {
		if res == nil || res.Infeasible == nil {
			return rep.fail("problem is infeasible (%s) but result carries no proof", derivedKind), nil
		}
		if res.Infeasible.Kind != derivedKind {
			return rep.fail("infeasibility kind %q does not match derived %q", res.Infeasible.Kind, derivedKind), nil
		}
		if err := vm.checkProof(res.Infeasible, total); err != nil {
			return rep.fail("%v", err), nil
		}
		rep.OK = true
		return rep, nil
	}
	type feasible struct {
		mask uint32
		cost uint64
	}
	var feasibleMasks []feasible
	for mask := uint32(0); mask < uint32(total); mask++ {
		cost := vm.cost(mask)
		reach := vm.reachable(mask)
		allDestroyDead := true
		for k := range vm.destroyKey {
			if reach[k] {
				allDestroyDead = false
				break
			}
		}
		allRetainAlive := true
		for k := range vm.retainKey {
			if !reach[k] {
				allRetainAlive = false
				break
			}
		}
		if allDestroyDead && allRetainAlive {
			feasibleMasks = append(feasibleMasks, feasible{mask: mask, cost: cost})
		}
	}
	if len(feasibleMasks) == 0 {
		if derivedKind == "" {
			derivedKind = "cut-coupling"
		}
		if res == nil || res.Infeasible == nil {
			return rep.fail("problem is infeasible but result carries no proof"), nil
		}
		if res.Infeasible.Kind != derivedKind {
			return rep.fail("infeasibility kind %q does not match derived %q", res.Infeasible.Kind, derivedKind), nil
		}
		if err := vm.checkProof(res.Infeasible, total); err != nil {
			return rep.fail("%v", err), nil
		}
		rep.OK = true
		return rep, nil
	}
	best := feasibleMasks[0]
	for _, f := range feasibleMasks[1:] {
		if betterFeasible(vm, f.mask, f.cost, best.mask, best.cost) {
			best = f
		}
	}
	rep.MinCost = best.cost
	rep.OptimalGroups = vm.names(best.mask)
	if res == nil || res.Plan == nil {
		return rep.fail("feasible problem but result carries no plan"), nil
	}
	plan := res.Plan
	if plan.Cost != best.cost {
		return rep.fail("plan cost %d differs from minimum %d", plan.Cost, best.cost), nil
	}
	if plan.Certificate.LowerBound != best.cost {
		return rep.fail("lower bound %d differs from minimum %d", plan.Certificate.LowerBound, best.cost), nil
	}
	if plan.Certificate.Subsets != total {
		return rep.fail("certificate subsets %d differ from %d", plan.Certificate.Subsets, total), nil
	}
	if plan.Certificate.FeasibleSubsets != uint64(len(feasibleMasks)) {
		return rep.fail("feasible subset count %d differs from %d", plan.Certificate.FeasibleSubsets, len(feasibleMasks)), nil
	}
	wantNames := vm.names(best.mask)
	if !equalStrings(plan.DisabledGroups, wantNames) {
		return rep.fail("plan groups %v differ from optimum %v", plan.DisabledGroups, wantNames), nil
	}
	if err := vm.checkAliases(plan); err != nil {
		return rep.fail("%v", err), nil
	}
	if err := vm.checkPlanSatisfies(plan); err != nil {
		return rep.fail("%v", err), nil
	}
	rep.OK = true
	return rep, nil
}

func betterFeasible(vm *vmodel, cand uint32, candCost uint64, cur uint32, curCost uint64) bool {
	if candCost != curCost {
		return candCost < curCost
	}
	cn, rn := vm.names(cand), vm.names(cur)
	if len(cn) != len(rn) {
		return len(cn) < len(rn)
	}
	return cmpStrings(cn, rn) < 0
}

func (vm *vmodel) detectConflicts() string {
	for k := range vm.destroyKey {
		if vm.retainKey[k] {
			return "key-conflict"
		}
	}
	for _, a := range vm.destroyBlk {
		for _, b := range vm.retainBlk {
			if less(a.lo, b.hi) && less(b.lo, a.hi) {
				return "block-conflict"
			}
		}
	}
	return ""
}

func (vm *vmodel) detectRetainUnreachable() string {
	reach := vm.reachable(0)
	for k := range vm.retainKey {
		if !reach[k] {
			return "retain-unreachable"
		}
	}
	return ""
}

func (vm *vmodel) checkAliases(plan *dp.Plan) error {
	activeGroups := map[string]bool{}
	for _, id := range plan.DisabledGroups {
		activeGroups[id] = true
	}
	want := map[string]bool{}
	for _, g := range vm.groups {
		if activeGroups[g.canonical] {
			for _, nm := range g.all {
				want[nm] = true
			}
		}
	}
	if len(want) != len(plan.DisabledAliases) {
		return fmt.Errorf("alias count mismatch")
	}
	for nm := range want {
		if !contains(plan.DisabledAliases, nm) {
			return fmt.Errorf("alias %q missing from plan", nm)
		}
	}
	return nil
}

func (vm *vmodel) checkPlanSatisfies(plan *dp.Plan) error {
	mask, err := vm.maskOf(plan.DisabledGroups)
	if err != nil {
		return err
	}
	reach := vm.reachable(mask)
	for k := range vm.destroyKey {
		if reach[k] {
			return fmt.Errorf("destroy key %q still reachable", k)
		}
	}
	for k := range vm.retainKey {
		if !reach[k] {
			return fmt.Errorf("retain key %q became unreachable", k)
		}
	}
	return nil
}

func (vm *vmodel) killsAllDestroy(mask uint32) bool {
	reach := vm.reachable(mask)
	for k := range vm.destroyKey {
		if reach[k] {
			return false
		}
	}
	return true
}

func (vm *vmodel) minimal(mask uint32) error {
	if mask == 0 {
		return nil
	}
	for i := 0; i < len(vm.groups); i++ {
		bit := uint32(1) << i
		if mask&bit == 0 {
			continue
		}
		if vm.killsAllDestroy(mask ^ bit) {
			return fmt.Errorf("group %q is redundant in cut", vm.groups[i].canonical)
		}
	}
	return nil
}

func (vm *vmodel) checkProof(inf *dp.Infeasibility, total uint64) error {
	switch inf.Kind {
	case "key-conflict":
		if inf.Conflict == nil || inf.Conflict.Kind != "key-conflict" {
			return fmt.Errorf("missing key-conflict proof")
		}
		if !vm.destroyKey[inf.Conflict.Key] || !vm.retainKey[inf.Conflict.Key] {
			return fmt.Errorf("conflict key %q not in both obligations", inf.Conflict.Key)
		}
		return nil
	case "block-conflict":
		if inf.Conflict == nil || inf.Conflict.Kind != "block-conflict" {
			return fmt.Errorf("missing block-conflict proof")
		}
		return nil
	case "retain-unreachable":
		if inf.UnreachableKey == "" || vm.reachable(0)[inf.UnreachableKey] {
			return fmt.Errorf("unreachable-key proof is invalid")
		}
		return nil
	case "cut-coupling":
		if inf.Coupling == nil {
			return fmt.Errorf("missing coupling proof")
		}
		if inf.Coupling.Subsets != total {
			return fmt.Errorf("coupling subset count mismatch")
		}
		if len(inf.Coupling.MinimalCuts) == 0 {
			return fmt.Errorf("coupling proof has no minimal cuts")
		}
		seen := map[string]bool{}
		for _, w := range inf.Coupling.MinimalCuts {
			key := fmt.Sprint(w.DestroyCut)
			if seen[key] {
				return fmt.Errorf("duplicate minimal cut")
			}
			seen[key] = true
			mask, err := vm.maskOf(w.DestroyCut)
			if err != nil {
				return err
			}
			if !vm.killsAllDestroy(mask) {
				return fmt.Errorf("witness %v does not kill every destroy key", w.DestroyCut)
			}
			if err := vm.minimal(mask); err != nil {
				return fmt.Errorf("witness %v: %v", w.DestroyCut, err)
			}
			if vm.reachable(mask)[w.KilledRetain] {
				return fmt.Errorf("witness claims retain key %q but it stays alive", w.KilledRetain)
			}
			if !vm.retainKey[w.KilledRetain] {
				return fmt.Errorf("witness names non-retain key %q", w.KilledRetain)
			}
		}
		for mask := uint32(0); mask < uint32(total); mask++ {
			if !vm.killsAllDestroy(mask) || vm.minimal(mask) != nil {
				continue
			}
			if !seen[fmt.Sprint(vm.names(mask))] {
				return fmt.Errorf("minimal destroy cut %v missing from proof", vm.names(mask))
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown infeasibility kind %q", inf.Kind)
	}
}
