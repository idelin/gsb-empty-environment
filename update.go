package indexvis

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrContradiction is returned when fixed statuses force a committed
	// transaction to reach an aborted transaction through dependencies.
	ErrContradiction = errors.New("indexvis: contradictory transaction statuses")
	// ErrPairNotCovered is returned when an incremental pair cannot be placed
	// in any bag of the fixed decomposition.
	ErrPairNotCovered = errors.New("indexvis: version pair is not covered by the fixed decomposition")
)

// ContradictionError reports one committed-to-aborted dependency path.
type ContradictionError struct {
	From TxID
	To   TxID
	Path []TxID
}

func (e *ContradictionError) Error() string {
	return fmt.Sprintf("%v: %d commits but dependency path %v reaches aborted transaction %d",
		ErrContradiction, e.From, e.Path, e.To)
}

func (e *ContradictionError) Is(target error) bool {
	return target == ErrContradiction
}

// BagUpdateCount is the counting evidence for one recomputed bag.
type BagUpdateCount struct {
	Bag               int
	TableEntries      int
	ProjectionEntries int
}

// UpdateProof is returned by incremental updates. It records which bags were
// recomputed and how many table/projection entries were touched. For the
// supported updates the affected set is exactly the union of the affected
// bags' paths to the root; CheckOnlyRootPaths verifies that invariant.
type UpdateProof struct {
	Kind string
	Tx   TxID
	Pair Pair

	AffectedBags []int
	UpdatedBags  []int
	Bags         []BagUpdateCount

	TotalBags     int
	UntouchedBags int

	TableEntries      int
	ProjectionEntries int
	AuxEntries        int

	AttemptTableEntries      int
	AttemptProjectionEntries int
	RolledBack               bool
}

// CheckOnlyRootPaths verifies that UpdatedBags is precisely the union of the
// root paths of AffectedBags, with no other bags updated.
func (p UpdateProof) CheckOnlyRootPaths(td *TreeDecomposition) bool {
	if td == nil {
		return false
	}
	expected := make(map[int]struct{})
	for _, a := range p.AffectedBags {
		if a < 0 || a >= len(td.Bags) {
			return false
		}
		for _, b := range td.PathToRoot(a) {
			expected[b] = struct{}{}
		}
	}
	if len(expected) != len(p.UpdatedBags) {
		return false
	}
	seen := make(map[int]struct{}, len(p.UpdatedBags))
	for _, b := range p.UpdatedBags {
		if _, ok := expected[b]; !ok {
			return false
		}
		if _, dup := seen[b]; dup {
			return false
		}
		seen[b] = struct{}{}
	}
	return true
}

func (ix *Index) updatePath(start int) UpdateProof {
	proof := UpdateProof{AffectedBags: []int{start}, TotalBags: len(ix.td.Bags)}
	for cur := start; cur >= 0; {
		tableEntries := ix.recomputeBag(cur)
		projEntries := ix.refreshProjection(cur)
		proof.UpdatedBags = append(proof.UpdatedBags, cur)
		proof.Bags = append(proof.Bags, BagUpdateCount{
			Bag:               cur,
			TableEntries:      tableEntries,
			ProjectionEntries: projEntries,
		})
		proof.TableEntries += tableEntries
		proof.ProjectionEntries += projEntries
		cur = ix.td.Parent[cur]
	}
	proof.UntouchedBags = proof.TotalBags - len(proof.UpdatedBags)
	return proof
}

// SetStatus fixes a transaction's final commit status. Unknown removes the
// fixing. If the new status makes the dependency system contradictory, the
// update is rolled back before the contradiction is returned.
func (ix *Index) SetStatus(id TxID, st Status) (UpdateProof, error) {
	proof := UpdateProof{
		Kind:          "status",
		Tx:            id,
		TotalBags:     len(ix.td.Bags),
		UntouchedBags: len(ix.td.Bags),
	}
	if id == NoTransaction {
		return proof, fmt.Errorf("SetStatus: NoTransaction is not a transaction")
	}
	if !st.valid() {
		return proof, fmt.Errorf("SetStatus: invalid status %d", st)
	}
	old, existed := ix.status[id]
	if !existed {
		ix.status[id] = st
		return proof, nil
	}
	if old == st {
		return proof, nil
	}
	owner, covered := ix.ownerBag[id]
	if !covered {
		ix.status[id] = st
		return proof, nil
	}

	ix.status[id] = st
	proof.AuxEntries += ix.refreshValid(owner)
	attempt := ix.updatePath(owner)
	if ix.feasible() {
		attempt.Kind = "status"
		attempt.Tx = id
		attempt.AuxEntries = proof.AuxEntries
		return attempt, nil
	}

	ce := ix.findContradiction()
	attempt.RolledBack = true

	ix.status[id] = old
	proof.AuxEntries += ix.refreshValid(owner)
	rolled := ix.updatePath(owner)
	rolled.Kind = "status"
	rolled.Tx = id
	rolled.AuxEntries = proof.AuxEntries
	rolled.RolledBack = true
	rolled.AttemptTableEntries = attempt.TableEntries
	rolled.AttemptProjectionEntries = attempt.ProjectionEntries

	if ce == nil {
		ce = &ContradictionError{Path: []TxID{id}}
	}
	return rolled, ce
}

// SetVersions sets the multiplicity of one transaction pair on the current
// page, recomputing only the owning bag's path to the root.
func (ix *Index) SetVersions(pair Pair, n int64) (UpdateProof, error) {
	if n < 0 {
		return UpdateProof{Kind: "versions", Pair: pair}, fmt.Errorf("SetVersions: negative multiplicity %d", n)
	}
	current := ix.summary.Count(pair.Create, pair.Delete)
	return ix.AddVersions(pair.Create, pair.Delete, n-current)
}

// AddVersions adds delta to a transaction pair's multiplicity. The pair must
// already be covered by the fixed decomposition, or its two endpoints must
// occur together in some bag.
func (ix *Index) AddVersions(create, deleter TxID, delta int64) (UpdateProof, error) {
	pair := Pair{Create: create, Delete: deleter}
	proof := UpdateProof{
		Kind:          "versions",
		Pair:          pair,
		TotalBags:     len(ix.td.Bags),
		UntouchedBags: len(ix.td.Bags),
	}
	if err := validatePair(pair); err != nil {
		return proof, err
	}
	if delta == 0 {
		return proof, nil
	}
	current := ix.summary.Count(create, deleter)
	next := current + delta
	if next < 0 {
		return proof, fmt.Errorf("AddVersions: multiplicity for %s would become %d", pair, next)
	}
	bag, ok := ix.pairBag[pair]
	if !ok {
		bag, ok = ix.bagForPair(pair)
		if !ok {
			return proof, fmt.Errorf("%w: %s", ErrPairNotCovered, pair)
		}
	}

	ix.summary.ensure()
	if next == 0 {
		delete(ix.summary.counts, pair)
		delete(ix.pairBag, pair)
	} else {
		ix.summary.counts[pair] = next
		ix.pairBag[pair] = bag
	}

	proof.AuxEntries = ix.applyPairDelta(bag, pair, delta)
	path := ix.updatePath(bag)
	path.Kind = "versions"
	path.Pair = pair
	path.AuxEntries = proof.AuxEntries
	return path, nil
}

func (ix *Index) findContradiction() *ContradictionError {
	adj := make(map[TxID][]TxID)
	for _, e := range ix.deps {
		adj[e.From] = append(adj[e.From], e.To)
	}
	for from := range adj {
		sort.Slice(adj[from], func(i, j int) bool { return adj[from][i] < adj[from][j] })
	}

	parent := make(map[TxID]TxID)
	seen := make(map[TxID]bool)
	queue := make([]TxID, 0)
	for tx, st := range ix.status {
		if st == Committed {
			seen[tx] = true
			queue = append(queue, tx)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i] < queue[j] })

	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, v := range adj[u] {
			if ix.status[v] == Aborted {
				path := []TxID{v}
				for cur := u; ; {
					path = append(path, cur)
					if p, ok := parent[cur]; ok {
						cur = p
					} else {
						break
					}
				}
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				return &ContradictionError{From: path[0], To: v, Path: path}
			}
			if !seen[v] {
				seen[v] = true
				parent[v] = u
				queue = append(queue, v)
			}
		}
	}
	return nil
}
