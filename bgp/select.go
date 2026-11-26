package bgp

import "sort"

// Selector evaluates candidate paths for one prefix against a fixed local
// router configuration. A Selector is safe for repeated use; it never mutates
// the supplied paths.
type Selector struct {
	cfg Config
}

// NewSelector constructs a selector from the local configuration.
func NewSelector(cfg Config) *Selector {
	return &Selector{cfg: cfg}
}

// Config returns the configuration the selector was built with.
func (s *Selector) Config() Config { return s.cfg }

// compare implements the ordered BGP best-path tie breaks. It returns -1 when
// a is strictly better than b, 1 when b is better, and 0 only when every
// attribute (including the caller-assigned ID) is equal.
//
// When useMED is false the MED step is skipped, matching the rule that MED is
// only compared across different neighbouring autonomous systems when
// always-compare-med is enabled.
func (s *Selector) compare(a, b *Path, useMED bool) int {
	// 1. LOCAL_PREF: higher wins.
	if a.LocalPref != b.LocalPref {
		return boolCmp(a.LocalPref > b.LocalPref)
	}
	// 2. AS_PATH length: shorter wins (confederation segments count zero).
	la, lb := ASPathLength(a.ASPath), ASPathLength(b.ASPath)
	if la != lb {
		return boolCmp(la < lb)
	}
	// 3. ORIGIN: IGP < EGP < INCOMPLETE.
	if a.Origin != b.Origin {
		return boolCmp(a.Origin < b.Origin)
	}
	// 4. MED: only when permitted for this pair of paths.
	if useMED {
		ma, mb := s.cfg.effectiveMED(a), s.cfg.effectiveMED(b)
		if ma != mb {
			return boolCmp(ma < mb)
		}
	}
	// 5. Path type: eBGP > confed-eBGP > iBGP.
	if ra, rb := a.LearnedFrom.rank(), b.LearnedFrom.rank(); ra != rb {
		return boolCmp(ra < rb)
	}
	// 6. IGP cost to next hop: lower wins.
	if a.IGPCost != b.IGPCost {
		return boolCmp(a.IGPCost < b.IGPCost)
	}
	// 7. CLUSTER_LIST length: shorter wins.
	if len(a.ClusterList) != len(b.ClusterList) {
		return boolCmp(len(a.ClusterList) < len(b.ClusterList))
	}
	// 8. Effective originator / peer Router ID: lower wins. A present
	// ORIGINATOR_ID takes precedence over the peer identifier.
	ra, rb := effectiveRouterID(a), effectiveRouterID(b)
	if ra != rb {
		return boolCmp(ra < rb)
	}
	// 9. Lowest peer IP address.
	if cmp := CompareIP(a.PeerIP, b.PeerIP); cmp != 0 {
		return cmp
	}
	// 10. Stable caller-assigned ID guarantees a total, deterministic order.
	switch {
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	default:
		return 0
	}
}

func effectiveRouterID(p *Path) uint32 {
	if p.OriginatorID != 0 {
		return p.OriginatorID
	}
	return p.PeerRouterID
}

// boolCmp maps "a wins" to -1 and "b wins" to +1.
func boolCmp(aWins bool) int {
	if aWins {
		return -1
	}
	return 1
}

// Select runs legality filtering and best-path selection over candidates.
// The result is independent of input ordering whenever DeterministicMED is
// enabled; with it disabled the legacy arrival-order-dependent pairwise
// algorithm is reproduced for comparison and regression testing.
func (s *Selector) Select(candidates []*Path) *Result {
	res := &Result{GroupWinners: make(map[uint32]*Path)}

	valid := s.filter(candidates, res)
	if len(valid) == 0 {
		return res
	}

	groups := make(map[uint32][]*Path)
	for _, p := range valid {
		key := NeighborAS(p.ASPath)
		groups[key] = append(groups[key], p)
	}

	// Group winners are always exposed; MED is always compared inside a single
	// neighbouring-AS group.
	for key, members := range groups {
		winner := members[0]
		for _, candidate := range members[1:] {
			if s.compare(candidate, winner, true) < 0 {
				winner = candidate
			}
		}
		res.GroupWinners[key] = winner
	}

	if s.cfg.DeterministicMED {
		res.Best = s.selectDeterministic(res.GroupWinners)
	} else {
		res.Best = s.selectLegacy(valid)
	}
	return res
}

// filter applies all mandatory legality checks, preserving input order in the
// rejected list.
func (s *Selector) filter(candidates []*Path, res *Result) []*Path {
	valid := make([]*Path, 0, len(candidates))
	for _, p := range candidates {
		if reason := s.cfg.validate(p); reason != RejectNone {
			res.Rejected = append(res.Rejected, FilteredPath{Path: p, Reason: reason})
			continue
		}
		valid = append(valid, p)
	}
	return valid
}

// selectDeterministic compares the precomputed group winners over ascending
// group keys. Cross-group MED participates only with always-compare-med. The
// result cannot depend on candidate arrival order.
func (s *Selector) selectDeterministic(winners map[uint32]*Path) *Path {
	keys := make([]uint32, 0, len(winners))
	for key := range winners {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	best := winners[keys[0]]
	for _, key := range keys[1:] {
		winner := winners[key]
		if s.compare(winner, best, s.cfg.AlwaysCompareMED) < 0 {
			best = winner
		}
	}
	return best
}

// selectLegacy reproduces the historical pairwise fold: each newly examined
// path is compared against the current best, and MED participates only when
// both share a neighbouring AS (or always-compare-med is set). Non-transitive
// MED/IGP interactions can make this output order dependent, which is exactly
// the oscillation deterministic MED exists to remove.
func (s *Selector) selectLegacy(valid []*Path) *Path {
	best := valid[0]
	for _, candidate := range valid[1:] {
		useMED := s.cfg.AlwaysCompareMED ||
			NeighborAS(candidate.ASPath) == NeighborAS(best.ASPath)
		if s.compare(candidate, best, useMED) < 0 {
			best = candidate
		}
	}
	return best
}
