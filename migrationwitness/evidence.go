package migrationwitness

import (
	"errors"
	"sort"
)

// Evidence errors.
var (
	// ErrTableClosed is returned when a blob, concat or alias statement is
	// appended after the closure receipt.
	ErrTableClosed = errors.New("migrationwitness: candidate table already closed")
	// ErrDuplicateReceipt is returned for a repeated receipt identifier.
	ErrDuplicateReceipt = errors.New("migrationwitness: duplicate receipt id")
	// ErrUnknownReceipt is returned when a withdrawal names an absent receipt.
	ErrUnknownReceipt = errors.New("migrationwitness: unknown receipt id")
	// ErrDuplicateNode is returned for a repeated manifest node identifier.
	ErrDuplicateNode = errors.New("migrationwitness: duplicate node id")
	// ErrAlreadyClosed is returned when a second closure receipt is supplied.
	ErrAlreadyClosed = errors.New("migrationwitness: closure receipt already present")
	// ErrLengthMismatch is returned when a statement contradicts a logical length.
	ErrLengthMismatch = errors.New("migrationwitness: logical length mismatch")
)

// kind holds the static, kind-specific data of a manifest node.
type nodeData struct {
	kind    NodeKind
	ref     Ref
	payload []byte
	edges   []Ref
	id      NewID
}

// Candidate is a concrete object that may satisfy a legacy reference.
type Candidate struct {
	ID     CandidateID
	Kind   NodeKind
	Ref    Ref
	NewID  NewID
	nodeID NodeID
}

// Snapshot is an immutable view of the evidence at one log version.
type Snapshot struct {
	// Version is the number of accepted evidence events (zero based count).
	Version int
	Closed  bool

	nodes     map[NodeID]*nodeData
	nodeOrder []NodeID
	byRef     map[Ref][]CandidateID
	aliases   map[Ref][]Ref
	receipts  map[ReceiptID]DisambiguationReceipt
	receiptOf map[EdgeKey]ReceiptID
}

type event struct {
	verify func(store *TrustStore) error
	apply  func(s *Snapshot) error
}

// EvidenceLog is an append-only, versioned collection of signed migration
// evidence. Withdrawals are themselves signed events and never rewrite
// history: older versions stay reconstructable for certificate re-checks.
type EvidenceLog struct {
	store  *TrustStore
	events []event
}

// NewEvidenceLog creates an empty log anchored to the given trust store.
func NewEvidenceLog(store *TrustStore) *EvidenceLog {
	return &EvidenceLog{store: store}
}

// Version returns the number of accepted events.
func (l *EvidenceLog) Version() int { return len(l.events) }

func (l *EvidenceLog) append(ev event) (int, error) {
	if err := ev.verify(l.store); err != nil {
		return len(l.events), err
	}
	s := l.replay(len(l.events))
	if err := ev.apply(s); err != nil {
		return len(l.events), err
	}
	l.events = append(l.events, ev)
	return len(l.events), nil
}

// AddBlob accepts a signed raw block statement.
func (l *EvidenceLog) AddBlob(s Signed[BlobStatement]) (int, error) {
	b := s.Body
	return l.append(event{
		verify: func(store *TrustStore) error { return s.Verify(store) },
		apply: func(snap *Snapshot) error {
			if snap.Closed {
				return ErrTableClosed
			}
			if _, exists := snap.nodes[b.Node]; exists {
				return ErrDuplicateNode
			}
			nd := &nodeData{
				kind:    KindBlob,
				ref:     b.Ref,
				payload: append([]byte(nil), b.Payload...),
				id:      ComputeBlobID(b.Payload),
			}
			if b.Ref.Length != int64(len(b.Payload)) {
				return ErrLengthMismatch
			}
			snap.nodes[b.Node] = nd
			snap.nodeOrder = append(snap.nodeOrder, b.Node)
			addCandidate(snap, b.Node, KindBlob, b.Ref, nd.id)
			return nil
		},
	})
}

// AddConcat accepts a signed ordered concatenation statement.
func (l *EvidenceLog) AddConcat(s Signed[ConcatStatement]) (int, error) {
	c := s.Body
	return l.append(event{
		verify: func(store *TrustStore) error { return s.Verify(store) },
		apply: func(snap *Snapshot) error {
			if snap.Closed {
				return ErrTableClosed
			}
			if _, exists := snap.nodes[c.Node]; exists {
				return ErrDuplicateNode
			}
			edges := append([]Ref(nil), c.Edges...)
			snap.nodes[c.Node] = &nodeData{
				kind:  KindConcat,
				ref:   c.Ref,
				edges: edges,
			}
			snap.nodeOrder = append(snap.nodeOrder, c.Node)
			addCandidate(snap, c.Node, KindConcat, c.Ref, NewID{})
			return nil
		},
	})
}

// AddAlias accepts a signed alias redirection.
func (l *EvidenceLog) AddAlias(s Signed[AliasStatement]) (int, error) {
	a := s.Body
	return l.append(event{
		verify: func(store *TrustStore) error { return s.Verify(store) },
		apply: func(snap *Snapshot) error {
			if snap.Closed {
				return ErrTableClosed
			}
			snap.aliases[a.From] = append(snap.aliases[a.From], a.To)
			return nil
		},
	})
}

// AddReceipt accepts a signed disambiguation receipt.
func (l *EvidenceLog) AddReceipt(s Signed[DisambiguationReceipt]) (int, error) {
	r := s.Body
	return l.append(event{
		verify: func(store *TrustStore) error { return s.Verify(store) },
		apply: func(snap *Snapshot) error {
			if _, exists := snap.receipts[r.ID]; exists {
				return ErrDuplicateReceipt
			}
			snap.receipts[r.ID] = r
			snap.receiptOf[EdgeKey{Manifest: r.Manifest, Edge: r.Edge}] = r.ID
			return nil
		},
	})
}

// CloseTable accepts the signed receipt that seals the candidate table.
func (l *EvidenceLog) CloseTable(s Signed[ClosureReceipt]) (int, error) {
	return l.append(event{
		verify: func(store *TrustStore) error { return s.Verify(store) },
		apply: func(snap *Snapshot) error {
			if snap.Closed {
				return ErrAlreadyClosed
			}
			snap.Closed = true
			return nil
		},
	})
}

// Withdraw accepts a signed statement that retracts a disambiguation receipt.
func (l *EvidenceLog) Withdraw(s Signed[WithdrawalStatement]) (int, error) {
	w := s.Body
	return l.append(event{
		verify: func(store *TrustStore) error { return s.Verify(store) },
		apply: func(snap *Snapshot) error {
			r, ok := snap.receipts[w.Receipt]
			if !ok {
				return ErrUnknownReceipt
			}
			delete(snap.receipts, w.Receipt)
			key := EdgeKey{Manifest: r.Manifest, Edge: r.Edge}
			if snap.receiptOf[key] == w.Receipt {
				delete(snap.receiptOf, key)
			}
			return nil
		},
	})
}

func addCandidate(snap *Snapshot, node NodeID, kind NodeKind, ref Ref, id NewID) {
	cid := CandidateID(node)
	snap.byRef[ref] = append(snap.byRef[ref], cid)
}

func newSnapshot() *Snapshot {
	return &Snapshot{
		nodes:     map[NodeID]*nodeData{},
		byRef:     map[Ref][]CandidateID{},
		aliases:   map[Ref][]Ref{},
		receipts:  map[ReceiptID]DisambiguationReceipt{},
		receiptOf: map[EdgeKey]ReceiptID{},
	}
}

// replay rebuilds the snapshot after version events, re-verifying every
// signature. It is used both internally and by certificate verification.
func (l *EvidenceLog) replay(version int) *Snapshot {
	snap := newSnapshot()
	for i := 0; i < version && i < len(l.events); i++ {
		if err := l.events[i].verify(l.store); err != nil {
			panic("migrationwitness: stored event failed signature verification: " + err.Error())
		}
		if err := l.events[i].apply(snap); err != nil {
			panic("migrationwitness: stored event failed to replay: " + err.Error())
		}
	}
	snap.Version = version
	return snap
}

// SnapshotAt reconstructs the evidence view at a specific version.
func (l *EvidenceLog) SnapshotAt(version int) (*Snapshot, error) {
	if version < 0 || version > len(l.events) {
		return nil, errors.New("migrationwitness: version out of range")
	}
	return l.replay(version), nil
}

// Current returns the snapshot at the newest evidence version.
func (l *EvidenceLog) Current() *Snapshot { return l.replay(len(l.events)) }

// candidate resolves a candidate id to its public description.
func (s *Snapshot) candidate(id CandidateID) (Candidate, bool) {
	nd, ok := s.nodes[NodeID(id)]
	if !ok {
		return Candidate{}, false
	}
	return Candidate{
		ID:     id,
		Kind:   nd.kind,
		Ref:    nd.ref,
		NewID:  nd.id,
		nodeID: NodeID(id),
	}, true
}

// Candidates returns the public candidate list for a reference in stable order.
func (s *Snapshot) Candidates(r Ref) []Candidate {
	out := make([]Candidate, 0)
	for _, id := range s.byRef[r] {
		if cand, ok := s.candidate(id); ok {
			out = append(out, cand)
		}
	}
	return out
}

// expand follows direct candidates and alias chains to produce the candidate
// set for a reference. An alias cycle contributes nothing from the cyclic
// path but never suppresses legal candidates reached elsewhere.
func (s *Snapshot) expand(r Ref) []CandidateID {
	var walk func(Ref, map[Ref]bool) []CandidateID
	walk = func(cur Ref, path map[Ref]bool) []CandidateID {
		if path[cur] {
			return nil
		}
		path[cur] = true
		out := append([]CandidateID(nil), s.byRef[cur]...)
		for _, next := range s.aliases[cur] {
			out = append(out, walk(next, path)...)
		}
		return out
	}
	raw := walk(r, map[Ref]bool{})

	seen := map[CandidateID]bool{}
	out := make([]CandidateID, 0, len(raw))
	for _, id := range raw {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// hasUnclosedAlias reports whether an alias chain reachable from r terminates
// at a name with neither concrete candidates nor further aliases. Such a chain
// cannot be closed with the present evidence. Alias cycles are not "open":
// they are illegal branches handled during expansion.
func (s *Snapshot) hasUnclosedAlias(r Ref) bool {
	var walk func(Ref, bool, map[Ref]bool) bool
	walk = func(cur Ref, viaAlias bool, path map[Ref]bool) bool {
		if path[cur] {
			return false // cyclic chain: illegal branch, not an open chain
		}
		path[cur] = true
		if len(s.byRef[cur]) > 0 {
			return false
		}
		targets := s.aliases[cur]
		if len(targets) == 0 {
			// Only a name reached through an alias can be a dangling chain end;
			// a starting ref with no aliases is simply an empty candidate set.
			return viaAlias
		}
		for _, next := range targets {
			if walk(next, true, path) {
				return true
			}
		}
		return false
	}
	return walk(r, false, map[Ref]bool{})
}
