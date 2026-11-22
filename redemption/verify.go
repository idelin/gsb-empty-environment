package redemption

import "fmt"

// VerifySolution independently checks a solution against the problem:
//
//  1. the certificate binds exactly this problem (canonical digest);
//  2. the claimed optimal mask is feasible and matches the selected IDs;
//  3. the branch-and-bound certificate tree is well-formed, every pruning
//     reason is independently re-derived, and the tree covers the complete
//     decision space;
//  4. induction over the tree proves no pruned subtree could contain a
//     strictly better (value, then tie-break) feasible subset.
//
// Signature and redemption-proof verification stay outside the certificate:
// they are applied when building the ledger via the host-injected verifiers.
func VerifySolution(p Problem, sol *Solution, sigVerifier SignatureVerifier, proofVerifier ProofVerifier) error {
	if sol == nil || sol.Certificate == nil {
		return fmt.Errorf("%w: missing certificate", ErrCertificate)
	}
	cert := sol.Certificate
	if got := CanonicalDigest(p); got != cert.Digest {
		return fmt.Errorf("%w: want %s got %s", ErrDigestMismatch, cert.Digest, got)
	}
	ledger, err := Prepare(p, sigVerifier, proofVerifier)
	if err != nil {
		return err
	}
	if ledger.N() != cert.NCandidates {
		return fmt.Errorf("%w: candidate count %d != %d", ErrCertificate, ledger.N(), cert.NCandidates)
	}

	claimedMask, err := ledger.MaskFromIDs(sol.Selected)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCertificate, err)
	}
	if claimedMask != cert.OptimalMask {
		return fmt.Errorf("%w: selected ids do not match optimal mask", ErrCertificate)
	}
	if ok, err := ledger.Feasible(cert.OptimalMask); !ok {
		return fmt.Errorf("%w: claimed optimum infeasible: %v", ErrCertificate, err)
	}
	if got := ledger.Entitlements(cert.OptimalMask); got != cert.OptimalValue {
		return fmt.Errorf("%w: value %d != %d", ErrCertificate, got, cert.OptimalValue)
	}
	if got := cert.OptimalValue; got != sol.Entitlements {
		return fmt.Errorf("%w: solution value %d != %d", ErrCertificate, sol.Entitlements, got)
	}
	return verifyTree(ledger, cert)
}

// verifyTree walks the certificate pre-order and validates every node.
func verifyTree(l *Ledger, cert *Certificate) error {
	if len(cert.Nodes) == 0 {
		return fmt.Errorf("%w: empty tree", ErrCertificate)
	}
	root := cert.Nodes[0]
	if root.Parent != -1 || root.Index != -1 || root.Depth != 0 || root.Mask != 0 {
		return fmt.Errorf("%w: malformed root", ErrCertificate)
	}

	n := l.N()
	nextID := 1
	// best value/mask reachable in each node's subtree, proven by induction.
	subBestMask := make([]uint32, len(cert.Nodes))
	subBestValue := make([]int64, len(cert.Nodes))
	subExists := make([]bool, len(cert.Nodes))

	var walk func(nodeID, depth int) error
	walk = func(nodeID, depth int) error {
		node := cert.Nodes[nodeID]
		if node.Depth != depth {
			return fmt.Errorf("%w: node %d depth %d != %d", ErrCertificate, nodeID, node.Depth, depth)
		}
		if depth > n {
			return fmt.Errorf("%w: node %d deeper than candidate count", ErrCertificate, nodeID)
		}
		if node.Mask&^((uint32(1)<<uint(n))-1) != 0 {
			return fmt.Errorf("%w: node %d mask out of range", ErrCertificate, nodeID)
		}

		if node.Pruned != "" {
			if err := verifyPrune(l, cert, nodeID); err != nil {
				return err
			}
			subExists[nodeID] = false
			return nil
		}

		if depth == n {
			// Leaf: fully decided. It must be feasible and carry its value.
			if ok, err := l.Feasible(node.Mask); !ok {
				return fmt.Errorf("%w: infeasible leaf %d: %v", ErrCertificate, nodeID, err)
			}
			val := l.Entitlements(node.Mask)
			if val != node.UpperBound {
				return fmt.Errorf("%w: leaf %d value %d != bound %d", ErrCertificate, nodeID, val, node.UpperBound)
			}
			subBestValue[nodeID] = val
			subBestMask[nodeID] = node.Mask
			subExists[nodeID] = true
			return nil
		}

		// Internal node: exactly two children deciding candidate depth,
		// include first then exclude, serialized in pre-order. Each walk
		// consumes exactly its own subtree via nextID.
		if nextID >= len(cert.Nodes) {
			return fmt.Errorf("%w: node %d missing include child", ErrCertificate, nodeID)
		}
		inc := cert.Nodes[nextID]
		wantMask := node.Mask | 1<<uint(depth)
		if inc.Parent != nodeID || inc.Depth != depth+1 || inc.Index != depth || !inc.Included || inc.Mask != wantMask {
			return fmt.Errorf("%w: node %d include child malformed", ErrCertificate, nodeID)
		}
		incID := nextID
		nextID++
		if err := walk(incID, depth+1); err != nil {
			return err
		}
		if nextID >= len(cert.Nodes) {
			return fmt.Errorf("%w: node %d missing exclude child", ErrCertificate, nodeID)
		}
		exc := cert.Nodes[nextID]
		if exc.Parent != nodeID || exc.Depth != depth+1 || exc.Index != depth || exc.Included || exc.Mask != node.Mask {
			return fmt.Errorf("%w: node %d exclude child malformed", ErrCertificate, nodeID)
		}
		excID := nextID
		nextID++
		if err := walk(excID, depth+1); err != nil {
			return err
		}
		// The best completion of this node is the best feasible child
		// subtree; both children may legitimately be fully pruned.
		switch {
		case !subExists[incID] && !subExists[excID]:
			subExists[nodeID] = false
		case !subExists[excID]:
			subExists[nodeID] = true
			subBestValue[nodeID] = subBestValue[incID]
			subBestMask[nodeID] = subBestMask[incID]
		case !subExists[incID]:
			subExists[nodeID] = true
			subBestValue[nodeID] = subBestValue[excID]
			subBestMask[nodeID] = subBestMask[excID]
		default:
			vi, ve := subBestValue[incID], subBestValue[excID]
			subExists[nodeID] = true
			if vi > ve || (vi == ve && l.lexLess(subBestMask[incID], subBestMask[excID])) {
				subBestValue[nodeID] = vi
				subBestMask[nodeID] = subBestMask[incID]
			} else {
				subBestValue[nodeID] = ve
				subBestMask[nodeID] = subBestMask[excID]
			}
		}
		return nil
	}
	if err := walk(0, 0); err != nil {
		return err
	}
	if nextID != len(cert.Nodes) {
		return fmt.Errorf("%w: %d unreachable certificate nodes", ErrCertificate, len(cert.Nodes)-nextID)
	}
	if !subExists[0] {
		return fmt.Errorf("%w: certificate tree contains no feasible subset", ErrCertificate)
	}

	rootValue := subBestValue[0]
	rootMask := subBestMask[0]
	if rootValue != cert.OptimalValue || rootMask != cert.OptimalMask {
		return fmt.Errorf("%w: tree optimum (%d,%b) != claimed (%d,%b)",
			ErrCertificate, rootValue, rootMask, cert.OptimalValue, cert.OptimalMask)
	}
	return nil
}

// verifyPrune independently re-derives the pruning reason and bound.
func verifyPrune(l *Ledger, cert *Certificate, nodeID int) error {
	node := cert.Nodes[nodeID]
	depth := node.Depth
	switch node.Pruned {
	case PruneBound:
		// The recorded bound must be a valid relaxation: recompute it.
		got := relaxedBound(l, node.Mask, depth)
		if got != node.UpperBound {
			return fmt.Errorf("%w: node %d bound %d != recomputed %d",
				ErrCertificate, nodeID, node.UpperBound, got)
		}
		if got > cert.OptimalValue {
			return fmt.Errorf("%w: node %d bound pruned but %d > optimum %d",
				ErrCertificate, nodeID, got, cert.OptimalValue)
		}
		// An equal bound may be pruned only when the fixed prefix already
		// loses the lexicographic tie against every completion.
		if got == cert.OptimalValue && !bitTieLost(node.Mask, cert.OptimalMask) {
			return fmt.Errorf("%w: node %d tie pruned although prefix could win", ErrCertificate, nodeID)
		}
	case PrunePrefix:
		if !node.Included {
			return fmt.Errorf("%w: node %d prefix prune without include", ErrCertificate, nodeID)
		}
		if !l.candidates[node.Index].blocked {
			return fmt.Errorf("%w: node %d not prefix-blocked", ErrCertificate, nodeID)
		}
	case PruneDependency:
		if !node.Included {
			return fmt.Errorf("%w: node %d dependency prune without include", ErrCertificate, nodeID)
		}
		decided := (uint32(1) << uint(node.Index)) - 1
		missing := l.candidates[node.Index].predMask &^ node.Mask & decided
		if missing == 0 {
			return fmt.Errorf("%w: node %d has no missing predecessor", ErrCertificate, nodeID)
		}
	case PruneConflict:
		if !node.Included {
			return fmt.Errorf("%w: node %d conflict prune without include", ErrCertificate, nodeID)
		}
		// Re-run the local consistency check on the claimed mask.
		if !localConflict(l, node.Mask, node.Index) {
			return fmt.Errorf("%w: node %d claimed conflict not reproducible", ErrCertificate, nodeID)
		}
	case PruneInfeasibleLeaf:
		if node.Depth != l.N() {
			return fmt.Errorf("%w: node %d leaf prune before full depth", ErrCertificate, nodeID)
		}
		if ok, _ := l.Feasible(node.Mask); ok {
			return fmt.Errorf("%w: node %d leaf claimed infeasible but passes", ErrCertificate, nodeID)
		}
	default:
		return fmt.Errorf("%w: unknown prune reason %q", ErrCertificate, node.Pruned)
	}
	return nil
}

// relaxedBound sums the value fixed by the mask plus all non-negative
// entitlements of undecided candidates (indices >= depth).
func relaxedBound(l *Ledger, mask uint32, depth int) int64 {
	bound := l.Entitlements(mask)
	for j := depth; j < l.N(); j++ {
		if l.candidates[j].entitlements > 0 {
			bound += l.candidates[j].entitlements
		}
	}
	return bound
}

// localConflict independently reproduces the include-time conflict test for
// adding candidate `added` on top of the other transactions in mask. Only
// nullifier/outpoint reuse against earlier decisions and quota overflow are
// local conflicts; prefix, dependency and cycle failures carry other reasons.
func localConflict(l *Ledger, mask uint32, added int) bool {
	if mask&(1<<uint(added)) == 0 {
		return false
	}
	seenNull := make(map[Nullifier]bool)
	seenOut := make(map[Outpoint]bool)
	used := make(map[BalanceKey]int64)
	for i := 0; i < l.N(); i++ {
		if i == added || mask&(1<<uint(i)) == 0 {
			continue
		}
		for _, nf := range l.nullifierUse[i] {
			seenNull[nf] = true
		}
		for _, op := range l.outpointUse[i] {
			seenOut[op] = true
		}
		for key, cost := range l.spendByKey[i] {
			used[key] += cost
		}
	}
	for _, nf := range l.nullifierUse[added] {
		if seenNull[nf] {
			return true
		}
	}
	for _, op := range l.outpointUse[added] {
		if seenOut[op] {
			return true
		}
	}
	for key, cost := range l.spendByKey[added] {
		if used[key]+cost > l.quotas[key] {
			return true
		}
	}
	return false
}

// bitTieLost independently re-derives the tie-aware pruning test. Ascending
// ID decision order makes bitmask one-wins order identical to the declared
// transaction-ID sequence lexicographic order.
func bitTieLost(mask, incumbent uint32) bool {
	diff := mask ^ incumbent
	if diff == 0 {
		return false
	}
	first := diff & -diff
	return incumbent&first != 0 && mask&first == 0
}
