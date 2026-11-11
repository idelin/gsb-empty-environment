// Package evidence merges signed tree heads and directed Merkle-tree
// consistency proofs observed by independent clients in order to detect
// transparent-log forks.
//
// The merger is agnostic to the log's cryptographic details: tree-head and
// consistency-proof verification is delegated to a caller-supplied
// Verifier. The merger only maintains the directed proof graph and the
// relations between observed tree heads.
//
// Relation rules, scoped to a single log identity:
//
//   - Two heads with equal tree size but different roots are a direct fork
//     witness.
//   - A larger head is compatible with a smaller one only when a directed
//     chain of consistency proofs connects the smaller to the larger.
//   - Without such a chain the pair is reported as pending, never malicious.
//
// Proofs never cross log identities: a proof whose endpoints belong to
// different logs is rejected.
package evidence

import (
	"encoding/hex"
	"errors"
	"sort"
	"sync"
)

// Sentinel errors returned by the merger. Callers should use errors.Is.
var (
	// ErrInvalidTreeHead is returned for a malformed tree head.
	ErrInvalidTreeHead = errors.New("evidence: invalid tree head")
	// ErrInvalidProof is returned for a structurally malformed proof.
	ErrInvalidProof = errors.New("evidence: invalid consistency proof")
	// ErrUntrusted wraps the verifier error when a tree head fails verification.
	ErrUntrusted = errors.New("evidence: tree head failed verification")
	// ErrBadProof wraps the verifier error when a proof fails verification.
	ErrBadProof = errors.New("evidence: consistency proof failed verification")
	// ErrDifferentLog is returned when a proof or relation crosses log identities.
	ErrDifferentLog = errors.New("evidence: endpoints belong to different log identities")
)

// TreeHead is a signed tree head (STH) observed by a client. Signature is
// opaque to the merger and checked by the injected Verifier.
type TreeHead struct {
	LogID     string
	Size      uint64
	Root      []byte
	Signature []byte
}

// ConsistencyProof is a directed Merkle consistency proof showing that the
// tree at NewSize (NewRoot) extends the tree at OldSize (OldRoot). Hashes is
// opaque to the merger: cryptographic validity is checked by the Verifier.
type ConsistencyProof struct {
	LogID   string
	OldSize uint64
	OldRoot []byte
	NewSize uint64
	NewRoot []byte
	Hashes  [][]byte
}

// Verifier is injected by the caller and encapsulates all cryptographic
// checks. Implementations return a non-nil error for any untrusted input.
type Verifier interface {
	VerifyTreeHead(th TreeHead) error
	VerifyConsistency(proof ConsistencyProof) error
}

// Status classifies the relation between two tree heads.
type Status int

const (
	// StatusUnknown means at least one head is unknown or heads differ in log.
	StatusUnknown Status = iota
	// StatusCompatible means identical heads or a connected proof chain.
	StatusCompatible
	// StatusPending means different sizes with no connecting chain yet.
	StatusPending
	// StatusWitness means equal-size heads carry different roots: a fork.
	StatusWitness
)

func (s Status) String() string {
	switch s {
	case StatusCompatible:
		return "compatible"
	case StatusPending:
		return "pending"
	case StatusWitness:
		return "witness"
	default:
		return "unknown"
	}
}

// Relation describes the relation between two tree heads. Chain, when
// non-empty, is ordered from the smaller head to the larger head.
type Relation struct {
	LogID  string
	A      TreeHead
	B      TreeHead
	Status Status
	Chain  []ConsistencyProof
}

// Witness is direct fork evidence: two equal-size heads with different roots.
type Witness struct {
	LogID string
	Size  uint64
	A     TreeHead
	B     TreeHead
}

// Pending is a pair of different-size heads not yet connected by a chain.
type Pending struct {
	LogID   string
	Smaller TreeHead
	Larger  TreeHead
}

// Stats exposes merger counters, mainly for monitoring and tests.
type Stats struct {
	TreeHeads       int
	Proofs          int
	PendingProofs   int
	DuplicateProofs int
	BadProofs       int
	RejectedHeads   int
	CrossLogProofs  int
}

type nodeID struct {
	log  string
	size uint64
	root string
}

type contentKey struct {
	size uint64
	root string
}

type graphNode struct {
	head TreeHead
	out  map[nodeID]*graphEdge
	in   map[nodeID]*graphEdge
}

type graphEdge struct {
	proof ConsistencyProof
	from  nodeID
	to    nodeID
}

// Merger incrementally collects tree heads and consistency proofs and answers
// relation queries over the resulting directed graph. It is safe for
// concurrent use.
type Merger struct {
	mu       sync.Mutex
	verifier Verifier
	nodes    map[nodeID]*graphNode
	edges    map[nodeID]map[nodeID]*graphEdge
	byLog    map[string]map[nodeID]struct{}
	owners   map[contentKey]map[string]struct{}
	pending  []ConsistencyProof
	stats    Stats
}

// New creates a Merger using the supplied verifier for all cryptographic
// checks. A nil verifier panics.
func New(verifier Verifier) *Merger {
	if verifier == nil {
		panic("evidence: nil verifier")
	}
	return &Merger{
		verifier: verifier,
		nodes:    make(map[nodeID]*graphNode),
		edges:    make(map[nodeID]map[nodeID]*graphEdge),
		byLog:    make(map[string]map[nodeID]struct{}),
		owners:   make(map[contentKey]map[string]struct{}),
	}
}

func idOf(th TreeHead) nodeID {
	return nodeID{log: th.LogID, size: th.Size, root: hex.EncodeToString(th.Root)}
}

func proofEndpoints(p ConsistencyProof) (nodeID, nodeID) {
	from := nodeID{log: p.LogID, size: p.OldSize, root: hex.EncodeToString(p.OldRoot)}
	to := nodeID{log: p.LogID, size: p.NewSize, root: hex.EncodeToString(p.NewRoot)}
	return from, to
}

// crossesLog reports whether either proof endpoint content is recorded only
// under log identities other than the proof's own log. Content unknown to the
// merger is never a mismatch (the proof may simply buffer).
func (m *Merger) crossesLog(p ConsistencyProof) bool {
	endpoints := []contentKey{
		{size: p.OldSize, root: hex.EncodeToString(p.OldRoot)},
		{size: p.NewSize, root: hex.EncodeToString(p.NewRoot)},
	}
	for _, key := range endpoints {
		ownerSet, known := m.owners[key]
		if !known || len(ownerSet) == 0 {
			continue
		}
		if _, ok := ownerSet[p.LogID]; !ok {
			return true
		}
	}
	return false
}

func validHead(th TreeHead) bool {
	return th.LogID != "" && th.Size > 0 && len(th.Root) > 0
}

// AddTreeHead verifies and incrementally records a signed tree head. Adding a
// head already known to the merger is a no-op. Buffered proofs whose
// endpoints are now both known are verified and activated.
func (m *Merger) AddTreeHead(th TreeHead) error {
	if !validHead(th) {
		return ErrInvalidTreeHead
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	id := idOf(th)
	if _, ok := m.nodes[id]; ok {
		return nil
	}
	if err := m.verifier.VerifyTreeHead(th); err != nil {
		m.stats.RejectedHeads++
		return errors.Join(ErrUntrusted, err)
	}

	m.nodes[id] = &graphNode{
		head: th,
		out:  make(map[nodeID]*graphEdge),
		in:   make(map[nodeID]*graphEdge),
	}
	if m.byLog[th.LogID] == nil {
		m.byLog[th.LogID] = make(map[nodeID]struct{})
	}
	m.byLog[th.LogID][id] = struct{}{}
	key := contentKey{size: th.Size, root: id.root}
	if m.owners[key] == nil {
		m.owners[key] = make(map[string]struct{})
	}
	m.owners[key][th.LogID] = struct{}{}
	m.stats.TreeHeads++

	m.activatePending()
	return nil
}

// AddConsistencyProof incrementally records a directed consistency proof.
//
// The proof is rejected when it is malformed, crosses log identities, or
// fails the injected verifier; rejected proofs never enter the graph. When an
// endpoint head is not yet known, the proof is buffered until both endpoints
// have been added. Re-adding a proof for an already connected endpoint pair
// is a deduplicating no-op.
func (m *Merger) AddConsistencyProof(proof ConsistencyProof) error {
	if proof.LogID == "" || proof.OldSize == 0 || proof.NewSize <= proof.OldSize ||
		len(proof.OldRoot) == 0 || len(proof.NewRoot) == 0 {
		return ErrInvalidProof
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.crossesLog(proof) {
		m.stats.CrossLogProofs++
		return ErrDifferentLog
	}

	from, to := proofEndpoints(proof)
	_, fromKnown := m.nodes[from]
	_, toKnown := m.nodes[to]

	if fromKnown && toKnown {
		if _, ok := m.edges[from][to]; ok {
			m.stats.DuplicateProofs++
			return nil
		}
		if err := m.verifier.VerifyConsistency(proof); err != nil {
			m.stats.BadProofs++
			return errors.Join(ErrBadProof, err)
		}
		m.insertEdge(from, to, proof)
		return nil
	}

	for _, buffered := range m.pending {
		bf, bt := proofEndpoints(buffered)
		if bf == from && bt == to {
			m.stats.DuplicateProofs++
			return nil
		}
	}
	m.pending = append(m.pending, proof)
	m.stats.PendingProofs++
	return nil
}

// activatePending verifies and inserts buffered proofs whose endpoints are
// now both known. Proofs that fail verification or cross log identities are
// isolated and dropped; a valid head insertion is never failed by them.
func (m *Merger) activatePending() {
	remaining := m.pending[:0]
	for _, proof := range m.pending {
		from, to := proofEndpoints(proof)
		fromKey := contentKey{size: proof.OldSize, root: from.root}
		toKey := contentKey{size: proof.NewSize, root: to.root}
		if _, ok := m.owners[fromKey]; !ok {
			remaining = append(remaining, proof)
			continue
		}
		if _, ok := m.owners[toKey]; !ok {
			remaining = append(remaining, proof)
			continue
		}
		m.stats.PendingProofs--
		if m.crossesLog(proof) {
			m.stats.CrossLogProofs++
			continue
		}
		_, fromKnown := m.nodes[from]
		_, toKnown := m.nodes[to]
		if !fromKnown || !toKnown {
			m.stats.CrossLogProofs++
			continue
		}
		if _, ok := m.edges[from][to]; ok {
			m.stats.DuplicateProofs++
			continue
		}
		if err := m.verifier.VerifyConsistency(proof); err != nil {
			m.stats.BadProofs++
			continue
		}
		m.insertEdge(from, to, proof)
	}
	m.pending = remaining
}

func (m *Merger) insertEdge(from, to nodeID, proof ConsistencyProof) {
	edge := &graphEdge{proof: proof, from: from, to: to}
	if m.edges[from] == nil {
		m.edges[from] = make(map[nodeID]*graphEdge)
	}
	m.edges[from][to] = edge
	m.nodes[from].out[to] = edge
	m.nodes[to].in[from] = edge
	m.stats.Proofs++
}

// Known reports whether the merger has recorded the given tree head.
func (m *Merger) Known(th TreeHead) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.nodes[idOf(th)]
	return ok
}

// Relate classifies the relation between two heads and, when compatible,
// returns the shortest directed proof chain from the smaller head to the
// larger one.
func (m *Merger) Relate(a, b TreeHead) Relation {
	m.mu.Lock()
	defer m.mu.Unlock()

	rel := Relation{A: a, B: b}
	if a.LogID != b.LogID {
		return rel
	}
	rel.LogID = a.LogID

	ida, idb := idOf(a), idOf(b)
	na, oka := m.nodes[ida]
	nb, okb := m.nodes[idb]
	if !oka || !okb || na.head.LogID != a.LogID || nb.head.LogID != b.LogID {
		return rel
	}

	switch {
	case ida == idb:
		rel.Status = StatusCompatible
	case a.Size == b.Size:
		rel.Status = StatusWitness
	default:
		from, to := ida, idb
		if a.Size > b.Size {
			from, to = to, from
		}
		if chain, ok := m.shortestPath(from, to); ok {
			rel.Status = StatusCompatible
			rel.Chain = chain
		} else {
			rel.Status = StatusPending
		}
	}
	return rel
}

// ShortestChain returns the shortest sequence of consistency proofs
// connecting the smaller head to the larger one, or nil when no chain exists
// or either head is unknown.
func (m *Merger) ShortestChain(smaller, larger TreeHead) []ConsistencyProof {
	m.mu.Lock()
	defer m.mu.Unlock()

	if smaller.LogID != larger.LogID || smaller.Size >= larger.Size {
		return nil
	}
	from, to := idOf(smaller), idOf(larger)
	if _, ok := m.nodes[from]; !ok {
		return nil
	}
	if _, ok := m.nodes[to]; !ok {
		return nil
	}
	chain, ok := m.shortestPath(from, to)
	if !ok {
		return nil
	}
	return chain
}

// shortestPath runs BFS over the directed graph and reconstructs the proof
// sequence from from to to. Callers must hold m.mu.
func (m *Merger) shortestPath(from, to nodeID) ([]ConsistencyProof, bool) {
	if from == to {
		return []ConsistencyProof{}, true
	}
	prev := make(map[nodeID]nodeID)
	prevEdge := make(map[nodeID]*graphEdge)
	visited := map[nodeID]bool{from: true}
	queue := []nodeID{from}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for next, edge := range m.nodes[cur].out {
			if visited[next] {
				continue
			}
			visited[next] = true
			prev[next] = cur
			prevEdge[next] = edge
			if next == to {
				chain := []ConsistencyProof{}
				for node := to; node != from; node = prev[node] {
					chain = append(chain, prevEdge[node].proof)
				}
				for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
					chain[i], chain[j] = chain[j], chain[i]
				}
				return chain, true
			}
			queue = append(queue, next)
		}
	}
	return nil, false
}

// Witnesses enumerates every equal-size, different-root head pair in each
// log. Pairs are deduplicated and returned in deterministic order.
func (m *Merger) Witnesses() []Witness {
	m.mu.Lock()
	defer m.mu.Unlock()

	type forkKey struct {
		size uint64
		x, y nodeID
	}
	seen := make(map[forkKey]struct{})
	var out []Witness

	logs := make([]string, 0, len(m.byLog))
	for logID := range m.byLog {
		logs = append(logs, logID)
	}
	sort.Strings(logs)

	for _, logID := range logs {
		ids := make([]nodeID, 0, len(m.byLog[logID]))
		for id := range m.byLog[logID] {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			if ids[i].size != ids[j].size {
				return ids[i].size < ids[j].size
			}
			return ids[i].root < ids[j].root
		})
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				x, y := ids[i], ids[j]
				if x.size != y.size {
					continue
				}
				key := forkKey{size: x.size, x: x, y: y}
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, Witness{
					LogID: logID,
					Size:  x.size,
					A:     m.nodes[x].head,
					B:     m.nodes[y].head,
				})
			}
		}
	}
	return out
}

// PendingPairs enumerates different-size head pairs in each log that are not
// connected by a directed proof chain. Pending pairs are not malicious.
func (m *Merger) PendingPairs() []Pending {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []Pending
	logs := make([]string, 0, len(m.byLog))
	for logID := range m.byLog {
		logs = append(logs, logID)
	}
	sort.Strings(logs)

	for _, logID := range logs {
		ids := make([]nodeID, 0, len(m.byLog[logID]))
		for id := range m.byLog[logID] {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			if ids[i].size != ids[j].size {
				return ids[i].size < ids[j].size
			}
			return ids[i].root < ids[j].root
		})
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				small, large := ids[i], ids[j]
				if small.size == large.size {
					continue
				}
				if _, connected := m.shortestPath(small, large); connected {
					continue
				}
				out = append(out, Pending{
					LogID:   logID,
					Smaller: m.nodes[small].head,
					Larger:  m.nodes[large].head,
				})
			}
		}
	}
	return out
}

// Stats returns a snapshot of merger counters.
func (m *Merger) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}
