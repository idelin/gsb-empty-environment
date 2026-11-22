package verifier

import (
	"fmt"
	"sort"

	dp "prescreen.local/ddd-9511-20261122-v2"
)

func build(p *dp.Problem) (*vmodel, error) {
	vm := &vmodel{
		p:          p,
		snaps:      map[string]dp.Snapshot{},
		roots:      map[string]bool{},
		data:       map[string]bool{},
		adj:        map[string][]vedge{},
		destroyKey: map[string]bool{},
		retainKey:  map[string]bool{},
	}
	for _, s := range p.Snapshots {
		if _, dup := vm.snaps[s.ID]; dup {
			return nil, fmt.Errorf("duplicate snapshot %q", s.ID)
		}
		vm.snaps[s.ID] = s
	}
	for _, k := range p.Graph.Keys {
		switch k.Role {
		case dp.KeyRoot:
			vm.roots[k.ID] = true
		case dp.KeyData:
			vm.data[k.ID] = true
		case dp.KeyIntermediate:
		default:
			return nil, fmt.Errorf("unknown key role for %q", k.ID)
		}
	}
	owner := map[string]int{}
	for _, e := range p.Graph.Edges {
		gi, known := owner[e.ID]
		if !known {
			set := append(append([]string{}, e.ID), e.Aliases...)
			for _, nm := range set {
				if g, seen := owner[nm]; seen {
					gi, known = g, true
					break
				}
			}
		}
		if !known {
			gi = len(vm.groups)
			vm.groups = append(vm.groups, vgroup{cost: e.Cost})
		}
		g := &vm.groups[gi]
		set := append(append([]string{}, e.ID), e.Aliases...)
		for _, nm := range set {
			if _, seen := owner[nm]; !seen {
				owner[nm] = gi
				g.all = append(g.all, nm)
			}
		}
	}
	if len(vm.groups) > 20 {
		return nil, fmt.Errorf("too many decision variables: %d", len(vm.groups))
	}
	for i := range vm.groups {
		sort.Strings(vm.groups[i].all)
		vm.groups[i].canonical = vm.groups[i].all[0]
	}
	for _, e := range p.Graph.Edges {
		if !vm.knownKey(e.From) || !vm.knownKey(e.To) {
			return nil, fmt.Errorf("edge %q references unknown key", e.ID)
		}
		vm.adj[e.From] = append(vm.adj[e.From], vedge{group: owner[e.ID], to: e.To})
	}
	if err := vm.collect(p.Destroy, true); err != nil {
		return nil, err
	}
	if err := vm.collect(p.Retain, false); err != nil {
		return nil, err
	}
	return vm, nil
}

func (vm *vmodel) knownKey(k string) bool {
	if vm.roots[k] || vm.data[k] {
		return true
	}
	for _, key := range vm.p.Graph.Keys {
		if key.ID == k && key.Role == dp.KeyIntermediate {
			return true
		}
	}
	return false
}

func (vm *vmodel) collect(reqs []dp.Requirement, destroy bool) error {
	for _, req := range reqs {
		snap, ok := vm.snaps[req.Snapshot]
		if !ok {
			return fmt.Errorf("unknown snapshot %q", req.Snapshot)
		}
		res, err := dp.Resolve(snap, req.Interval)
		if err != nil {
			return err
		}
		for _, pc := range res.Pieces {
			if !vm.data[pc.Key] {
				return fmt.Errorf("leaf key %q is not a data key", pc.Key)
			}
			br := vrange{lo: dp.Addr64(pc.BlockLo), hi: pc.BlockHi}
			if destroy {
				vm.destroyKey[pc.Key] = true
				vm.destroyBlk = append(vm.destroyBlk, br)
			} else {
				vm.retainKey[pc.Key] = true
				vm.retainBlk = append(vm.retainBlk, br)
			}
		}
	}
	return nil
}

func (vm *vmodel) reachable(disabled uint32) map[string]bool {
	seen := map[string]bool{}
	var queue []string
	for k := range vm.roots {
		if !seen[k] {
			seen[k] = true
			queue = append(queue, k)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range vm.adj[cur] {
			if disabled&(1<<e.group) != 0 {
				continue
			}
			if !seen[e.to] {
				seen[e.to] = true
				queue = append(queue, e.to)
			}
		}
	}
	return seen
}

func (vm *vmodel) cost(mask uint32) uint64 {
	var total uint64
	for i, g := range vm.groups {
		if mask&(1<<i) != 0 {
			total += g.cost
		}
	}
	return total
}

func (vm *vmodel) names(mask uint32) []string {
	var out []string
	for i, g := range vm.groups {
		if mask&(1<<i) != 0 {
			out = append(out, g.canonical)
		}
	}
	sort.Strings(out)
	return out
}

func (vm *vmodel) maskOf(names []string) (uint32, error) {
	mask := uint32(0)
	for _, nm := range names {
		found := false
		for i, g := range vm.groups {
			if g.canonical == nm {
				mask |= 1 << i
				found = true
				break
			}
		}
		if !found {
			return 0, fmt.Errorf("unknown edge group %q", nm)
		}
	}
	return mask, nil
}
