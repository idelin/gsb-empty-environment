package migrationwitness

import (
	"errors"
	"sort"
)

// Certificate verification errors.
var (
	// ErrVersionUnavailable is returned when the pinned evidence version is
	// missing from the log.
	ErrVersionUnavailable = errors.New("migrationwitness: certificate evidence version unavailable")
	// ErrCertificateMismatch is returned when the recorded shared subgraph does
	// not reproduce the claimed root identity.
	ErrCertificateMismatch = errors.New("migrationwitness: certificate does not reproduce root identity")
	// ErrCertificateConstraint is returned when a certificate choice violates a
	// receipt, reference, length or closure constraint.
	ErrCertificateConstraint = errors.New("migrationwitness: certificate violates an evidence constraint")
)

// ReachedNode is one node of the shared subgraph expressed by a certificate.
type ReachedNode struct {
	Node  NodeID
	Kind  NodeKind
	NewID NewID
}

// Certificate is an independently re-checkable resolution witness. Choices
// binds every traversed edge to the selected candidate; the shared subgraph is
// the set of nodes actually reached. It is pinned to an evidence version.
type Certificate struct {
	EvidenceVersion int
	Root            NodeID
	RootNewID       NewID
	Choices         map[EdgeKey]CandidateID
	Reached         []ReachedNode
}

func (s *solver) buildCertificate(b *branch, rootID NewID) Certificate {
	cert := Certificate{
		EvidenceVersion: s.snap.Version,
		Root:            s.root,
		RootNewID:       rootID,
		Choices:         map[EdgeKey]CandidateID{},
	}
	for k, cid := range b.choices {
		cert.Choices[k] = cid
	}

	reached := map[NodeID]NewID{}
	var derive func(NodeID) NewID
	derive = func(n NodeID) NewID {
		if id, ok := reached[n]; ok {
			return id
		}
		nd := s.snap.nodes[n]
		var id NewID
		if nd.kind == KindBlob {
			id = nd.id
		} else {
			children := make([]NewID, 0, len(nd.edges))
			for i := range nd.edges {
				cid := b.choices[EdgeKey{Manifest: n, Edge: i}]
				children = append(children, derive(NodeID(cid)))
			}
			id = ComputeConcatID(nd.ref.Length, children)
		}
		reached[n] = id
		return id
	}
	derive(s.root)

	order := make([]NodeID, 0, len(reached))
	for n := range reached {
		order = append(order, n)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	for _, n := range order {
		cert.Reached = append(cert.Reached, ReachedNode{
			Node:  n,
			Kind:  s.snap.nodes[n].kind,
			NewID: reached[n],
		})
	}
	return cert
}

// VerifyCertificate re-checks a certificate against the evidence log at its
// pinned version. It replays the shared subgraph independently and refuses any
// certificate whose evidence version was not closed, whose choices violate a
// receipt or reference, or whose recomputed root identity differs.
func VerifyCertificate(log *EvidenceLog, cert Certificate) error {
	snap, err := log.SnapshotAt(cert.EvidenceVersion)
	if err != nil {
		return ErrVersionUnavailable
	}
	if !snap.Closed {
		return ErrCertificateConstraint
	}

	// Every choice must reference an existing candidate whose Ref matches the
	// edge, and must agree with any receipt active at this version.
	for key, cid := range cert.Choices {
		nd, ok := snap.nodes[key.Manifest]
		if !ok || nd.kind != KindConcat || key.Edge < 0 || key.Edge >= len(nd.edges) {
			return ErrCertificateConstraint
		}
		cand, ok := snap.candidate(cid)
		if !ok || cand.Ref.Length != nd.edges[key.Edge].Length {
			return ErrCertificateConstraint
		}
		if rid, has := snap.receiptOf[key]; has {
			if snap.receipts[rid].Candidate != cid {
				return ErrCertificateConstraint
			}
		}
	}

	reached := map[NodeID]NewID{}
	onPath := map[NodeID]bool{}
	var resolve func(NodeID) (NewID, error)
	resolve = func(n NodeID) (NewID, error) {
		if id, ok := reached[n]; ok {
			return id, nil
		}
		if onPath[n] {
			return NewID{}, ErrCertificateConstraint
		}
		onPath[n] = true
		defer delete(onPath, n)

		nd, ok := snap.nodes[n]
		if !ok {
			return NewID{}, ErrCertificateConstraint
		}
		var id NewID
		switch nd.kind {
		case KindBlob:
			id = nd.id
		case KindConcat:
			if sumEdges(nd.edges) != nd.ref.Length {
				return NewID{}, ErrCertificateConstraint
			}
			children := make([]NewID, 0, len(nd.edges))
			for i := range nd.edges {
				cid, ok := cert.Choices[EdgeKey{Manifest: n, Edge: i}]
				if !ok {
					return NewID{}, ErrCertificateConstraint
				}
				child, err := resolve(NodeID(cid))
				if err != nil {
					return NewID{}, err
				}
				children = append(children, child)
			}
			id = ComputeConcatID(nd.ref.Length, children)
		default:
			return NewID{}, ErrCertificateConstraint
		}
		reached[n] = id
		return id, nil
	}

	rootID, err := resolve(cert.Root)
	if err != nil {
		return err
	}
	if !rootID.Equal(cert.RootNewID) {
		return ErrCertificateMismatch
	}

	// The recorded shared subgraph must be exactly the traversed node set.
	if len(reached) != len(cert.Reached) {
		return ErrCertificateMismatch
	}
	for _, rn := range cert.Reached {
		got, ok := reached[rn.Node]
		if !ok || !got.Equal(rn.NewID) {
			return ErrCertificateMismatch
		}
	}
	return nil
}

// StillValid reports whether a certificate remains valid at the newest log
// version. A withdrawal can make an older certificate inapplicable even though
// it still verifies at its pinned version.
func StillValid(log *EvidenceLog, cert Certificate) bool {
	if err := VerifyCertificate(log, cert); err != nil {
		return false
	}
	current, err := log.SnapshotAt(log.Version())
	if err != nil {
		return false
	}
	for key, cid := range cert.Choices {
		if rid, has := current.receiptOf[key]; has {
			if current.receipts[rid].Candidate != cid {
				return false
			}
		}
	}
	return true
}
