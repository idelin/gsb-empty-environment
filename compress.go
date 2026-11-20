package bootpolicy

import "sort"

// CompressResult is the output of Compress.
type CompressResult struct {
	// Selected is the minimum encoded-size subset of the candidates that is
	// equivalent to the original policy.
	Selected []Entry
	// SelectedIndexes are the indices of Selected within the candidates slice.
	SelectedIndexes []int
	// WireSize is the encoded byte cost of Selected.
	WireSize int
	// Certificate certifies the finite equivalence-class argument.
	Certificate Certificate
}

// Compress selects the encoded-byte-minimal subset of candidates whose
// admission decision is identical to original on every possible load event.
//
// A candidate recovery exemption is accepted only when it is byte-for-byte an
// exemption already present in the original policy, so compression can never
// mint a new exemption. The search is exhaustive over the at-most-18
// candidates; equivalence is decided over the finite partition of all load
// events, never over sampled generations or bounded label depths.
func Compress(original *Policy, candidates []Entry) (*CompressResult, error) {
	if err := original.Validate(); err != nil {
		return nil, err
	}
	if len(candidates) > MaxCandidates {
		return nil, ErrInvalidEntry
	}
	for i := range candidates {
		if err := candidates[i].Validate(); err != nil {
			return nil, err
		}
		if candidates[i].Kind == KindExempt && !containsEntry(original.Entries, candidates[i]) {
			return nil, ErrNewExemption
		}
	}

	nOrig := len(original.Entries)
	union := make([]Entry, 0, nOrig+len(candidates))
	union = append(union, original.Entries...)
	union = append(union, candidates...)

	part, err := BuildPartition(union, nOrig, MaxPartitionCells)
	if err != nil {
		return nil, err
	}
	groups := part.groups

	infos := make([]candInfoShim, len(candidates))
	for i := range candidates {
		bit := uint(nOrig + i)
		info := candInfoShim{
			index:    i,
			entry:    candidates[i],
			wireSize: EntryWireSize(candidates[i]),
			deny:     candidates[i].Kind == KindDeny,
		}
		for gi, g := range groups {
			if g.denyMask&(1<<bit) != 0 {
				info.denyHit |= 1 << uint(gi)
			}
			if g.exemptMask&(1<<bit) != 0 {
				info.exemptHit |= 1 << uint(gi)
			}
		}
		infos[i] = info
	}

	var oDeniedGroups, oAdmittedGroups uint64
	for gi, g := range groups {
		if g.origDenied {
			oDeniedGroups |= 1 << uint(gi)
		} else {
			oAdmittedGroups |= 1 << uint(gi)
		}
	}

	// Feasibility precondition: every originally denied group must be
	// coverable by at least one deny candidate. Interactions between over-broad
	// deny candidates and admitted groups are decided exactly by the exhaustive
	// subset search, not by this conservative precheck.
	denyCoverUnion := uint64(0)
	for i := range infos {
		if infos[i].deny {
			denyCoverUnion |= infos[i].denyHit & oDeniedGroups
		}
	}
	if oDeniedGroups&^denyCoverUnion != 0 {
		return nil, ErrNoEquivalentSet
	}

	order := make([]int, len(infos))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		if infos[order[a]].wireSize != infos[order[b]].wireSize {
			return infos[order[a]].wireSize < infos[order[b]].wireSize
		}
		return order[a] < order[b]
	})

	s := &search{
		infos:        infos,
		order:        order,
		groups:       groups,
		oDenied:      oDeniedGroups,
		oAdmitted:    oAdmittedGroups,
		bestCost:     maxInt,
		bestSelected: nil,
	}

	// Greedy upper bound: repeatedly add the best cost/new-coverage candidate.
	s.greedySeed()
	s.dfs(0, 0, searchState{
		denyMask:         0,
		exemptMask:       0,
		deniedCovered:    0,
		denyOnAdmitted:   0,
		exemptOnAdmitted: 0,
	})
	if s.bestSelected == nil {
		return nil, ErrNoEquivalentSet
	}

	selected := make([]Entry, 0, len(s.bestSelected))
	indexes := append([]int(nil), s.bestSelected...)
	sort.Ints(indexes)
	for _, idx := range indexes {
		selected = append(selected, candidates[idx])
	}

	// The importable certificate is rebuilt over exactly the original policy
	// and the installed subset, so an importer holding only the package can
	// independently reconstruct every equivalence class without the candidate
	// catalog.
	packageUnion := make([]Entry, 0, nOrig+len(selected))
	packageUnion = append(packageUnion, original.Entries...)
	packageUnion = append(packageUnion, selected...)
	packagePart, err := BuildPartition(packageUnion, nOrig, MaxPartitionCells)
	if err != nil {
		return nil, err
	}
	return &CompressResult{
		Selected:        selected,
		SelectedIndexes: indexes,
		WireSize:        s.bestCost,
		Certificate:     packagePart.Certificate(),
	}, nil
}

const maxInt = int(^uint(0) >> 1)

type searchState struct {
	denyMask         uint64
	exemptMask       uint64
	deniedCovered    uint64 // O-denied groups hit by a selected deny
	denyOnAdmitted   uint64 // O-admitted groups hit by a selected deny
	exemptOnAdmitted uint64 // O-admitted groups hit by a selected exemption
	exemptOnDenied   uint64 // O-denied groups hit by a selected exemption (illegal if nonempty)
}

type search struct {
	infos        []candInfoShim
	order        []int
	groups       []cellGroup
	oDenied      uint64
	oAdmitted    uint64
	bestCost     int
	bestSelected []int
}

type candInfoShim struct {
	index     int
	entry     Entry
	wireSize  int
	deny      bool
	denyHit   uint64
	exemptHit uint64
}

func (s *search) greedySeed() {
	state := searchState{}
	chosen := map[int]bool{}
	cost := 0
	for {
		if s.complete(state) {
			break
		}
		bestI, bestGain := -1, -1.0
		for _, oi := range s.order {
			if chosen[oi] {
				continue
			}
			info := s.infos[oi]
			gain := s.gain(info, state)
			if gain <= 0 {
				continue
			}
			score := float64(info.wireSize) / float64(gain)
			if bestI == -1 || score < bestGain {
				bestI, bestGain = oi, score
			}
		}
		if bestI == -1 {
			return
		}
		chosen[bestI] = true
		cost += s.infos[bestI].wireSize
		state = s.apply(state, s.infos[bestI])
	}
	if cost < s.bestCost {
		s.bestCost = cost
		s.bestSelected = keysOf(chosen)
	}
}

func keysOf(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (s *search) gain(info candInfoShim, st searchState) int {
	if info.deny {
		newCoverage := popcount64((info.denyHit & s.oDenied) &^ st.deniedCovered)
		newAdmitted := popcount64((info.denyHit & s.oAdmitted) &^ st.denyOnAdmitted)
		return newCoverage + newAdmitted
	}
	return popcount64((info.exemptHit & s.oAdmitted) &^ st.exemptOnAdmitted)
}

func (s *search) apply(st searchState, info candInfoShim) searchState {
	if info.deny {
		st.denyMask |= 1 << uint(info.index)
		st.deniedCovered |= info.denyHit & s.oDenied
		st.denyOnAdmitted |= info.denyHit & s.oAdmitted
	} else {
		st.exemptMask |= 1 << uint(info.index)
		st.exemptOnAdmitted |= info.exemptHit & s.oAdmitted
		st.exemptOnDenied |= info.exemptHit & s.oDenied
	}
	return st
}

func (s *search) complete(st searchState) bool {
	if st.deniedCovered != s.oDenied {
		return false
	}
	unsafe := st.denyOnAdmitted &^ st.exemptOnAdmitted
	return unsafe == 0 && st.exemptOnDenied == 0
}

func (s *search) dfs(pos, cost int, st searchState) {
	if cost >= s.bestCost {
		return
	}
	if pos == len(s.order) {
		if s.complete(st) {
			s.bestCost = cost
			s.bestSelected = s.selectedOf(st)
		}
		return
	}
	if s.lowerBound(st) >= s.bestCost-cost {
		return
	}
	oi := s.order[pos]
	info := s.infos[oi]

	// Skip branch first: entries never needed are pruned via the bound.
	s.dfs(pos+1, cost, st)

	next := s.apply(st, info)
	// A selected exemption must never match an O-denied group: such a group
	// cannot legally be rescued because the original policy rejects it.
	if info.deny || (info.exemptHit&s.oDenied) == 0 {
		s.dfs(pos+1, cost+info.wireSize, next)
	}
}

// lowerBound is a valid relaxation: the costliest per-group cheapest deny
// candidate needed to cover every still-uncovered O-denied group.
func (s *search) lowerBound(st searchState) int {
	uncovered := s.oDenied &^ st.deniedCovered
	bound := 0
	for uncovered != 0 {
		bit := uncovered & -uncovered
		gi := trailingZero64(bit)
		cheapest := maxInt
		for i := range s.infos {
			info := &s.infos[i]
			if !info.deny {
				continue
			}
			if info.denyHit&(1<<uint(gi)) != 0 && info.wireSize < cheapest {
				cheapest = info.wireSize
			}
		}
		if cheapest == maxInt {
			return maxInt
		}
		if cheapest > bound {
			bound = cheapest
		}
		uncovered &^= bit
	}
	return bound
}

func (s *search) selectedOf(st searchState) []int {
	out := make([]int, 0, len(s.infos))
	for i := range s.infos {
		if st.denyMask&(1<<uint(i)) != 0 || st.exemptMask&(1<<uint(i)) != 0 {
			out = append(out, i)
		}
	}
	return out
}

func containsEntry(entries []Entry, want Entry) bool {
	for i := range entries {
		if entriesEqual(entries[i], want) {
			return true
		}
	}
	return false
}

func entriesEqual(a, b Entry) bool {
	if a.Kind != b.Kind || a.SignerID != b.SignerID || a.LabelPrefix != b.LabelPrefix ||
		a.GenLo != b.GenLo || a.GenHi != b.GenHi || a.Location != b.Location ||
		a.RootID != b.RootID || a.NotAfter != b.NotAfter ||
		!a.Digest.equal(b.Digest) || !a.Parent.equal(b.Parent) {
		return false
	}
	return true
}

func popcount64(x uint64) int {
	n := 0
	for x != 0 {
		x &= x - 1
		n++
	}
	return n
}

func trailingZero64(x uint64) int {
	n := 0
	for x&1 == 0 {
		x >>= 1
		n++
	}
	return n
}
