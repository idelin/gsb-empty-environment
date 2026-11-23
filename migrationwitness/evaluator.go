package migrationwitness

import "errors"

// Status classifies an evaluation result.
type Status byte

const (
	// StatusPending means the evidence cannot yet justify publishing: the
	// candidate table is not closed or an unclosed alias chain is reachable.
	StatusPending Status = iota
	// StatusUnique means every compatible resolution converges on one NewID.
	StatusUnique
	// StatusMultiple means two distinct root NewIDs are compatible; certificates
	// for two re-checkable resolutions are returned.
	StatusMultiple
	// StatusNoResolution means no legal resolution survives all constraints.
	StatusNoResolution
	// StatusBudgetExhausted means the state budget was consumed before the
	// complete compatible-identity set could be determined; nothing is published.
	StatusBudgetExhausted
)

// Budget is a bound on solver work measured in node-entry attempts.
// A zero budget means unlimited.
type Budget int64

// Verdict is the result of evaluating a root against a snapshot.
type Verdict struct {
	Status         Status
	Root           NodeID
	DistinctIDs    []NewID
	Certificates   []Certificate
	Used           int64
	PendingReasons []string
}

// branch is one in-progress DFS resolution. Every map is local to the current
// assignment, so backtracking only requires discarding a branch; shared nodes
// are still enforced through the single edge-choice map carried by the branch.
type branch struct {
	memo    map[NodeID]NewID
	onPath  map[NodeID]bool
	choices map[EdgeKey]CandidateID
}

func newBranch() *branch {
	return &branch{
		memo:    map[NodeID]NewID{},
		onPath:  map[NodeID]bool{},
		choices: map[EdgeKey]CandidateID{},
	}
}

func (b *branch) clone() *branch {
	cp := &branch{
		memo:    make(map[NodeID]NewID, len(b.memo)),
		onPath:  make(map[NodeID]bool, len(b.onPath)+1),
		choices: make(map[EdgeKey]CandidateID, len(b.choices)),
	}
	for k, v := range b.memo {
		cp.memo[k] = v
	}
	for k := range b.onPath {
		cp.onPath[k] = true
	}
	for k, v := range b.choices {
		cp.choices[k] = v
	}
	return cp
}

type solver struct {
	snap   *Snapshot
	budget int64
	used   int64

	pending bool
	reasons []string

	root      NodeID
	distinct  map[string]Certificate
	order     []string
	stop      bool
	budgetOut bool
}

func newSolver(snap *Snapshot, budget int64, root NodeID) *solver {
	return &solver{
		snap:     snap,
		budget:   budget,
		root:     root,
		distinct: map[string]Certificate{},
	}
}

func (s *solver) markPending(msg string) {
	s.pending = true
	s.reasons = append(s.reasons, msg)
}

// edgeOpen marks a reference as pending when present evidence cannot close it.
// An empty candidate set after closure is a dead branch, not pending.
func (s *solver) edgeOpen(b *branch, r Ref) bool {
	if s.snap.Closed {
		// Once the table is sealed, missing candidates and dangling alias chains
		// are dead branches, never reasons to keep the root pending.
		return false
	}
	if s.snap.hasUnclosedAlias(r) {
		s.markPending("unclosed alias chain for " + string(r.Digest))
		return true
	}
	if len(s.snap.expand(r)) == 0 && !s.snap.Closed {
		s.markPending("candidate table not closed for " + string(r.Digest))
		return true
	}
	return false
}

func (s *solver) charge() bool {
	if s.budget <= 0 {
		return false
	}
	s.used++
	if s.used > s.budget {
		s.budgetOut = true
		return true
	}
	return false
}

func (s *solver) recordRoot(b *branch, rootID NewID) {
	key := rootID.String()
	if _, exists := s.distinct[key]; !exists {
		s.distinct[key] = s.buildCertificate(b, rootID)
		s.order = append(s.order, key)
		if len(s.distinct) >= 2 {
			s.stop = true
		}
	}
}

func (s *solver) run() {
	nd, ok := s.snap.nodes[s.root]
	if !ok {
		return
	}
	if nd.kind == KindBlob {
		if s.charge() {
			return
		}
		s.recordRoot(newBranch(), nd.id)
		return
	}
	if sumEdges(nd.edges) != nd.ref.Length {
		return
	}
	b := newBranch()
	b.onPath[s.root] = true
	s.solveEdges(b, s.root, nd.edges, 0, nil, func(children []NewID, winner *branch) {
		id := ComputeConcatID(nd.ref.Length, children)
		s.recordRoot(winner, id)
	})
}

// resolveNode computes the identity of n and returns the branch carrying a
// legal expansion. A revisit on the current DFS path is a cyclic expansion and
// only fails this branch.
func (s *solver) resolveNode(b *branch, n NodeID) (NewID, *branch, bool) {
	if s.budgetOut || s.stop {
		return NewID{}, nil, false
	}
	if id, ok := b.memo[n]; ok {
		return id, b, true
	}
	if b.onPath[n] {
		return NewID{}, nil, false
	}
	if s.charge() {
		return NewID{}, nil, false
	}
	nd, ok := s.snap.nodes[n]
	if !ok {
		return NewID{}, nil, false
	}
	if nd.kind == KindBlob {
		b.memo[n] = nd.id
		return nd.id, b, true
	}
	if sumEdges(nd.edges) != nd.ref.Length {
		return NewID{}, nil, false
	}

	b.onPath[n] = true
	defer delete(b.onPath, n)

	var firstID NewID
	var firstBranch *branch
	found := false
	s.solveEdges(b, n, nd.edges, 0, nil, func(children []NewID, winner *branch) {
		if !found {
			found = true
			firstID = ComputeConcatID(nd.ref.Length, children)
			firstBranch = winner
			winner.memo[n] = firstID
		}
	})
	if !found {
		return NewID{}, nil, false
	}
	return firstID, firstBranch, true
}

// solveEdges enumerates legal assignments of the ordered edges of owner. Each
// complete child-id vector invokes emit with the winning branch, so the full
// edge choice set is available and remains internally consistent.
func (s *solver) solveEdges(b *branch, owner NodeID, edges []Ref, idx int, acc []NewID, emit func([]NewID, *branch)) {
	if s.budgetOut || s.stop {
		return
	}
	if idx == len(edges) {
		emit(acc, b)
		return
	}
	r := edges[idx]
	if s.edgeOpen(b, r) {
		return
	}

	key := EdgeKey{Manifest: owner, Edge: idx}
	var candidates []CandidateID
	if rid, has := s.snap.receiptOf[key]; has {
		candidates = []CandidateID{s.snap.receipts[rid].Candidate}
	} else {
		candidates = s.snap.expand(r)
	}

	for _, cid := range candidates {
		if s.budgetOut || s.stop {
			return
		}
		cand, ok := s.snap.candidate(cid)
		// expand already scopes candidates to r, including objects reached
		// through alias chains; only the logical length has to match the edge.
		if !ok || cand.Ref.Length != r.Length {
			continue
		}
		if rid, has := s.snap.receiptOf[key]; has && s.snap.receipts[rid].Candidate != cid {
			continue
		}
		if prior, conflict := b.choices[key]; conflict && prior != cid {
			continue
		}

		child := cand.nodeID
		fork := b.clone()
		fork.choices[key] = cid

		childID, winner, ok := s.resolveNode(fork, child)
		if !ok {
			continue
		}
		s.solveEdges(winner, owner, edges, idx+1, append(acc, childID), emit)
	}
}

// Evaluate determines the migration verdict for root against the snapshot,
// bounded by budget. It never publishes when the table is open, an unclosed
// alias chain is reachable, or the budget cannot settle the question.
func Evaluate(snap *Snapshot, root NodeID, budget Budget) (*Verdict, error) {
	if _, ok := snap.nodes[root]; !ok {
		return nil, errors.New("migrationwitness: unknown root node")
	}
	s := newSolver(snap, int64(budget), root)
	if !snap.Closed {
		s.markPending("signed closure receipt absent")
	}
	s.run()

	v := &Verdict{
		Root:           root,
		Used:           s.used,
		PendingReasons: append([]string(nil), s.reasons...),
	}
	if s.budgetOut {
		v.Status = StatusBudgetExhausted
		return v, nil
	}
	if s.pending {
		v.Status = StatusPending
		return v, nil
	}
	for _, k := range s.order {
		v.DistinctIDs = append(v.DistinctIDs, s.distinct[k].RootNewID)
		v.Certificates = append(v.Certificates, s.distinct[k])
	}
	switch len(s.distinct) {
	case 0:
		v.Status = StatusNoResolution
	case 1:
		v.Status = StatusUnique
	default:
		v.Status = StatusMultiple
	}
	return v, nil
}

func sumEdges(edges []Ref) int64 {
	var total int64
	for _, e := range edges {
		total += e.Length
	}
	return total
}
