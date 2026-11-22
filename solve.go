package destplan

import "sort"

// Solve plans the minimum-cost edge deactivation for p. A malformed input
// returns an error; a well-formed but impossible request returns a Result
// whose Infeasible proof is populated.
func Solve(p *Problem) (*Result, error) {
	m, err := buildModel(p)
	if err != nil {
		return nil, err
	}
	destroyKeys, retainKeys, _, _, inf, err := collectObligations(p, m)
	if err != nil {
		return nil, err
	}
	if inf != nil {
		return &Result{
			Infeasible:  inf,
			DestroyKeys: sortedKeys(destroyKeys),
			RetainKeys:  sortedKeys(retainKeys),
		}, nil
	}
	dkList := sortedKeySet(destroyKeys)
	rkList := sortedKeySet(retainKeys)
	initReachable := reachable(m, 0)
	for _, k := range rkList {
		if !initReachable[k] {
			return &Result{
				Infeasible: &Infeasibility{
					Kind:           "retain-unreachable",
					Detail:         "retain key has no root path even with every edge active",
					UnreachableKey: k,
				},
				DestroyKeys: dkList,
				RetainKeys:  rkList,
			}, nil
		}
	}
	groupCount := len(m.groups)
	variables := make([]string, groupCount)
	for i, g := range m.groups {
		variables[i] = g.canonical
	}
	total := uint64(1) << groupCount
	var bestMask uint32
	var bestCost uint64
	found := false
	feasible := uint64(0)
	type cut struct {
		mask     uint32
		killedRk string
	}
	minimalCuts := []cut{}
	for mask := uint32(0); mask < uint32(total); mask++ {
		cost, ok := maskCost(m, mask)
		if !ok {
			return nil, errf("edge deactivation cost overflows uint64")
		}
		reach := reachable(m, mask)
		destroyDead := true
		for k := range destroyKeys {
			if reach[k] {
				destroyDead = false
				break
			}
		}
		retainAlive := true
		var killed string
		for _, k := range rkList {
			if !reach[k] {
				retainAlive = false
				if killed == "" {
					killed = k
				}
			}
		}
		if destroyDead {
			if isInclusionMinimal(m, mask, destroyKeys) {
				minimalCuts = append(minimalCuts, cut{mask: mask, killedRk: killed})
			}
		}
		if !destroyDead || !retainAlive {
			continue
		}
		feasible++
		if !found || betterMask(m, mask, cost, bestMask, bestCost) {
			found = true
			bestMask = mask
			bestCost = cost
		}
	}
	if !found {
		witnesses := make([]CutWitness, 0, len(minimalCuts))
		for _, c := range minimalCuts {
			witnesses = append(witnesses, CutWitness{
				DestroyCut:   maskNames(m, c.mask),
				KilledRetain: c.killedRk,
			})
		}
		sort.SliceStable(witnesses, func(i, j int) bool {
			return lexString(witnesses[i].DestroyCut, witnesses[j].DestroyCut) < 0
		})
		return &Result{
			Infeasible: &Infeasibility{
				Kind:   "cut-coupling",
				Detail: "every destroy cut also severs a retain key",
				Coupling: &CouplingProof{
					DestroyKeys: dkList,
					RetainKeys:  rkList,
					Subsets:     total,
					MinimalCuts: witnesses,
				},
			},
			DestroyKeys: dkList,
			RetainKeys:  rkList,
		}, nil
	}
	aliases := []string{}
	for i := 0; i < groupCount; i++ {
		if bestMask&(1<<i) != 0 {
			aliases = append(aliases, m.groups[i].names...)
		}
	}
	sort.Strings(aliases)
	return &Result{
		Plan: &Plan{
			DisabledGroups:  maskNames(m, bestMask),
			DisabledAliases: aliases,
			Cost:            bestCost,
			Certificate: OptimalityCertificate{
				Variables:       variables,
				Subsets:         total,
				LowerBound:      bestCost,
				FeasibleSubsets: feasible,
			},
		},
		DestroyKeys: dkList,
		RetainKeys:  rkList,
	}, nil
}

// betterMask ranks feasible cuts by cost, then number of deactivated groups,
// then the lexicographic order of the sorted canonical edge-ID sequence.
func betterMask(m *model, cand uint32, candCost uint64, cur uint32, curCost uint64) bool {
	if candCost != curCost {
		return candCost < curCost
	}
	candNames := maskNames(m, cand)
	curNames := maskNames(m, cur)
	if len(candNames) != len(curNames) {
		return len(candNames) < len(curNames)
	}
	return lexString(candNames, curNames) < 0
}

func collectObligations(p *Problem, m *model) (
	destroyKeys, retainKeys map[string]bool,
	destroyBlocks, retainBlocks []blockRange,
	inf *Infeasibility, err error,
) {
	destroyKeys = map[string]bool{}
	retainKeys = map[string]bool{}
	conflict := func(kind string, key string, block Addr, d, r Requirement) *Infeasibility {
		return &Infeasibility{
			Kind:   kind,
			Detail: "destroy and retain obligations share storage that cannot be both dead and alive",
			Conflict: &ConflictProof{
				Kind:       kind,
				Key:        key,
				Block:      block,
				DestroyRef: d,
				RetainRef:  r,
			},
		}
	}
	for _, req := range p.Destroy {
		res, rerr := Resolve(*m.snapshots[req.Snapshot], req.Interval)
		if rerr != nil {
			return nil, nil, nil, nil, nil, rerr
		}
		for _, pc := range res.Pieces {
			destroyKeys[pc.Key] = true
			br := blockRange{lo: Addr64(pc.BlockLo), hi: pc.BlockHi, req: req, key: pc.Key}
			destroyBlocks = append(destroyBlocks, br)
		}
	}
	for _, req := range p.Retain {
		res, rerr := Resolve(*m.snapshots[req.Snapshot], req.Interval)
		if rerr != nil {
			return nil, nil, nil, nil, nil, rerr
		}
		for _, pc := range res.Pieces {
			br := blockRange{lo: Addr64(pc.BlockLo), hi: pc.BlockHi, req: req, key: pc.Key}
			retainKeys[pc.Key] = true
			retainBlocks = append(retainBlocks, br)
		}
	}
	for k := range retainKeys {
		if destroyKeys[k] {
			return nil, nil, nil, nil, conflict("key-conflict", k, Addr{}, Requirement{}, findRetainReq(p, m, k)), nil
		}
	}
	for _, a := range destroyBlocks {
		for _, b := range retainBlocks {
			if addrCmp(a.lo, b.hi) < 0 && addrCmp(b.lo, a.hi) < 0 {
				return nil, nil, nil, nil, conflict("block-conflict", b.key, b.lo, a.req, b.req), nil
			}
		}
	}
	return destroyKeys, retainKeys, destroyBlocks, retainBlocks, nil, nil
}

type blockRange struct {
	lo, hi Addr
	req    Requirement
	key    string
}

func findRetainReq(p *Problem, m *model, key string) Requirement {
	for _, req := range p.Retain {
		res, err := Resolve(*m.snapshots[req.Snapshot], req.Interval)
		if err != nil {
			continue
		}
		for _, pc := range res.Pieces {
			if pc.Key == key {
				return req
			}
		}
	}
	return Requirement{}
}

// reachable performs a fresh root-to-key BFS using only edges whose group is
// not present in disabled. It examines every surviving edge, so every path
// is considered rather than a single witness path.
func reachable(m *model, disabled uint32) map[string]bool {
	seen := map[string]bool{}
	queue := []string{}
	for k := range m.rootKeys {
		if !seen[k] {
			seen[k] = true
			queue = append(queue, k)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range m.adj[cur] {
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

func maskCost(m *model, mask uint32) (uint64, bool) {
	var total uint64
	for i := range m.groups {
		if mask&(1<<i) == 0 {
			continue
		}
		c := m.groups[i].cost
		if total > ^uint64(0)-c {
			return 0, false
		}
		total += c
	}
	return total, true
}

// isInclusionMinimal reports that mask kills every destroy key while no
// strict subset of mask does.
func isInclusionMinimal(m *model, mask uint32, destroyKeys map[string]bool) bool {
	if mask == 0 {
		return true
	}
	for i := 0; i < len(m.groups); i++ {
		bit := uint32(1) << i
		if mask&bit == 0 {
			continue
		}
		reach := reachable(m, mask^bit)
		allDead := true
		for k := range destroyKeys {
			if reach[k] {
				allDead = false
				break
			}
		}
		if allDead {
			return false
		}
	}
	return true
}

func maskNames(m *model, mask uint32) []string {
	out := []string{}
	for i, g := range m.groups {
		if mask&(1<<i) != 0 {
			out = append(out, g.canonical)
		}
	}
	sort.Strings(out)
	return out
}

func lexString(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func sortedKeySet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(set map[string]bool) []string { return sortedKeySet(set) }
