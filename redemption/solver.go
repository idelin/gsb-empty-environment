package redemption

// Solve runs branch-and-bound over the at most 22 candidates and returns the
// optimal feasible subset together with a checkable optimality certificate.
func Solve(p Problem, sigVerifier SignatureVerifier, proofVerifier ProofVerifier) (*Solution, error) {
	ledger, err := Prepare(p, sigVerifier, proofVerifier)
	if err != nil {
		return nil, err
	}
	cert := &Certificate{
		Digest:      CanonicalDigest(p),
		NCandidates: ledger.N(),
	}
	s := &search{
		l:     ledger,
		cert:  cert,
		usedQ: make(map[BalanceKey]int64),
		nulls: make(map[Nullifier]bool),
		outs:  make(map[Outpoint]bool),
	}
	s.cert.Nodes = append(s.cert.Nodes, CertNode{Parent: -1, Depth: 0, Index: -1, Mask: 0})
	s.dfs(0, 0, 0, 0)
	cert.OptimalMask = s.bestMask
	cert.OptimalValue = s.bestValue
	return &Solution{
		Selected:     ledger.SelectedIDs(s.bestMask),
		Entitlements: s.bestValue,
		Certificate:  cert,
	}, nil
}

type search struct {
	l         *Ledger
	cert      *Certificate
	bestMask  uint32
	bestValue int64

	// incremental state for the current include-decision prefix
	mask  uint32
	usedQ map[BalanceKey]int64
	nulls map[Nullifier]bool
	outs  map[Outpoint]bool
}

// upperBound relaxes every remaining constraint and simply sums all
// non-negative candidate entitlements from index i onward. It is therefore
// always an upper bound on the value of every completion.
func (s *search) upperBound(i int, value int64) int64 {
	bound := value
	for j := i; j < s.l.N(); j++ {
		if s.l.candidates[j].entitlements > 0 {
			bound += s.l.candidates[j].entitlements
		}
	}
	return bound
}

func (s *search) addNode(parent, index int, included bool, mask uint32) int {
	id := len(s.cert.Nodes)
	s.cert.Nodes = append(s.cert.Nodes, CertNode{
		Parent: parent, Depth: index + 1, Index: index, Included: included, Mask: mask,
	})
	return id
}

func (s *search) prune(id int, reason PruneReason, bound int64) {
	s.cert.Nodes[id].Pruned = reason
	s.cert.Nodes[id].UpperBound = bound
}

func (s *search) dfs(i int, mask uint32, value int64, nodeID int) {
	s.mask = mask
	if i == s.l.N() {
		// Every leaf reached through incremental pruning is feasible; this
		// full check is the defensive backstop.
		ok, err := s.l.Feasible(mask)
		if err != nil || !ok {
			s.prune(nodeID, PruneInfeasibleLeaf, value)
			return
		}
		s.cert.Nodes[nodeID].UpperBound = value
		if value > s.bestValue || (value == s.bestValue && s.l.lexLess(mask, s.bestMask)) {
			s.bestValue = value
			s.bestMask = mask
		}
		return
	}

	bound := s.upperBound(i, value)
	if bound < s.bestValue || (bound == s.bestValue && s.tieAlreadyLost(mask)) {
		s.prune(nodeID, PruneBound, bound)
		return
	}

	// Include branch first: reaches high-value feasible leaves quickly,
	// which makes value-based pruning effective.
	incMask := mask | 1<<uint(i)
	if reason := s.includeCheck(i, incMask); reason != "" {
		incID := s.addNode(nodeID, i, true, incMask)
		s.prune(incID, reason, bound)
	} else {
		// Create the child immediately and recurse before any sibling node
		// exists so pre-order node IDs stay contiguous.
		incID := s.addNode(nodeID, i, true, incMask)
		s.push(i)
		s.dfs(i+1, incMask, value+s.l.candidates[i].entitlements, incID)
		s.pop(i)
	}

	// Exclude branch.
	excID := s.addNode(nodeID, i, false, mask)
	s.cert.Nodes[excID].UpperBound = bound
	s.dfs(i+1, mask, value, excID)
}

// tieAlreadyLost reports whether every completion of the current prefix is
// lexicographically worse than the incumbent once relaxed values tie.
//
// Because candidates are decided in strictly ascending transaction-ID order,
// the ascending ID sequence of a subset maps one-to-one onto its bitmask, and
// lexicographic order (one-wins at the first differing index, strict prefix
// first) equals comparing the lowest bit where the two masks differ. On the
// already-fixed prefix, if the incumbent has the smaller differing bit set,
// no later decision can change that outcome.
func (s *search) tieAlreadyLost(mask uint32) bool {
	diff := mask ^ s.bestMask
	if diff == 0 {
		return false
	}
	first := diff & -diff
	return s.bestMask&first != 0 && mask&first == 0
}

// includeCheck returns the pruning reason for including candidate i into the
// current prefix, or "" if the include branch is locally consistent.
func (s *search) includeCheck(i int, incMask uint32) PruneReason {
	info := &s.l.candidates[i]
	if info.blocked {
		return PrunePrefix
	}
	// Closure and acyclicity involve predecessors that may carry larger
	// indices under ascending-ID decision order; they are checked once the
	// whole mask is known at the leaf via Feasible. The earlier-decided
	// subset is pruned locally here when an excluded predecessor can never
	// be recovered.
	if missing := info.predMask &^ incMask; missing&((uint32(1)<<uint(i))-1) != 0 {
		return PruneDependency
	}
	for _, nf := range s.l.nullifierUse[i] {
		if s.nulls[nf] {
			return PruneConflict
		}
	}
	for _, op := range s.l.outpointUse[i] {
		if s.outs[op] {
			return PruneConflict
		}
	}
	for key, cost := range s.l.spendByKey[i] {
		if s.usedQ[key]+cost > s.l.quotas[key] {
			return PruneConflict
		}
	}
	return ""
}

func (s *search) push(i int) {
	for _, nf := range s.l.nullifierUse[i] {
		s.nulls[nf] = true
	}
	for _, op := range s.l.outpointUse[i] {
		s.outs[op] = true
	}
	for key, cost := range s.l.spendByKey[i] {
		s.usedQ[key] += cost
	}
}

func (s *search) pop(i int) {
	for _, nf := range s.l.nullifierUse[i] {
		delete(s.nulls, nf)
	}
	for _, op := range s.l.outpointUse[i] {
		delete(s.outs, op)
	}
	for key, cost := range s.l.spendByKey[i] {
		s.usedQ[key] -= cost
	}
}
