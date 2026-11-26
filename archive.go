package archiveproof

import (
	"fmt"
	"sort"
)

type archiveIndex struct {
	approved  map[NodeID]*Node
	hidden    map[NodeID]*Node
	allNodes  map[NodeID]*Node
	shared    map[Commitment]*SharedEvidence
	licensing map[Commitment]NodeID
}

func (a *Archive) buildIndex() (*archiveIndex, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: nil archive", ErrInvalidArchive)
	}
	idx := &archiveIndex{
		approved:  make(map[NodeID]*Node),
		hidden:    make(map[NodeID]*Node),
		allNodes:  make(map[NodeID]*Node),
		shared:    make(map[Commitment]*SharedEvidence),
		licensing: make(map[Commitment]NodeID),
	}
	for i := range a.Nodes {
		n := &a.Nodes[i]
		if n.ID == "" {
			return nil, fmt.Errorf("%w: node with empty id", ErrInvalidArchive)
		}
		if _, exists := idx.allNodes[n.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate node id %q", ErrInvalidArchive, n.ID)
		}
		if n.ClockSource() == "" {
			return nil, fmt.Errorf("%w: node %q has empty clock source", ErrInvalidArchive, n.ID)
		}
		if !n.Offset.Valid() {
			return nil, fmt.Errorf("%w: node %q has empty offset interval", ErrInvalidArchive, n.ID)
		}
		if !boundsFit(n.Time, n.Offset) {
			return nil, fmt.Errorf("%w: node %q has overflowing time bounds", ErrInvalidArchive, n.ID)
		}
		idx.allNodes[n.ID] = n
		if n.Approved {
			idx.approved[n.ID] = n
		} else {
			idx.hidden[n.ID] = n
		}
	}
	for i := range a.Shared {
		s := &a.Shared[i]
		if s.Commitment.IsZero() {
			return nil, fmt.Errorf("%w: shared evidence with zero commitment", ErrInvalidArchive)
		}
		if _, exists := idx.shared[s.Commitment]; exists {
			return nil, fmt.Errorf("%w: duplicate shared commitment %s", ErrInvalidArchive, s.Commitment)
		}
		if s.Source == "" {
			return nil, fmt.Errorf("%w: shared commitment %s has empty source", ErrInvalidArchive, s.Commitment)
		}
		if !s.Offset.Valid() {
			return nil, fmt.Errorf("%w: shared commitment %s has empty offset interval", ErrInvalidArchive, s.Commitment)
		}
		idx.shared[s.Commitment] = s
	}
	return idx, nil
}

// Validate checks every structural invariant needed by the verifier:
// renewals bind the exact old identity and content digest, shared evidence is
// either licensed by an approved timestamp or explicitly host-internal, and
// all references resolve.
func (a *Archive) Validate() error {
	idx, err := a.buildIndex()
	if err != nil {
		return err
	}
	if len(a.Genesis) == 0 {
		return fmt.Errorf("%w: no genesis reference", ErrInvalidArchive)
	}
	if len(a.Acceptance) == 0 {
		return fmt.Errorf("%w: no acceptance reference", ErrInvalidArchive)
	}

	for i := range a.Nodes {
		n := &a.Nodes[i]
		for _, link := range n.Commits {
			if link.Commitment.IsZero() {
				return fmt.Errorf("%w: node %q has zero commitment link", ErrInvalidArchive, n.ID)
			}
			if !link.Gap.Valid() {
				return fmt.Errorf("%w: node %q has invalid commitment gap", ErrInvalidArchive, n.ID)
			}
			if _, ok := idx.shared[link.Commitment]; !ok {
				return fmt.Errorf("%w: node %q commits unknown evidence %s", ErrInvalidArchive, n.ID, link.Commitment)
			}
		}
		for _, r := range n.Renews {
			if err := idx.validateRenewal(r, n.ID); err != nil {
				return err
			}
		}
	}

	for i := range a.Shared {
		s := &a.Shared[i]
		if !boundsFit(s.Time, s.Offset) {
			return fmt.Errorf("%w: shared commitment %s has overflowing time bounds", ErrInvalidArchive, s.Commitment)
		}
		for _, r := range s.RenewsNodes {
			if err := idx.validateRenewal(r, s.LicensedBy); err != nil {
				return err
			}
		}
		for _, r := range s.RenewsShared {
			if r.To.IsZero() {
				return fmt.Errorf("%w: shared commitment %s renews a zero commitment", ErrInvalidArchive, s.Commitment)
			}
			if !r.Gap.Valid() {
				return fmt.Errorf("%w: shared commitment %s has invalid renewal gap", ErrInvalidArchive, s.Commitment)
			}
			if _, ok := idx.shared[r.To]; !ok {
				return fmt.Errorf("%w: shared commitment %s renews unknown evidence %s", ErrInvalidArchive, s.Commitment, r.To)
			}
		}
		if s.LicensedBy == "" {
			continue
		}
		signer, ok := idx.approved[s.LicensedBy]
		if !ok {
			return fmt.Errorf("%w: shared commitment %s is licensed by non-approved node %q", ErrDisallowedEvidence, s.Commitment, s.LicensedBy)
		}
		if !signer.commitsTo(s.Commitment) && len(s.LicenseProof) == 0 {
			return fmt.Errorf("%w: node %q does not sign commitment %s", ErrDisallowedEvidence, signer.ID, s.Commitment)
		}
		idx.licensing[s.Commitment] = signer.ID
	}

	for _, ref := range append(append([]Reference{}, a.Genesis...), a.Acceptance...) {
		if err := idx.resolveReference(ref, true); err != nil {
			return err
		}
	}
	if _, err := a.ClockDomains(); err != nil {
		return err
	}
	return nil
}

func (n *Node) commitsTo(c Commitment) bool {
	for _, link := range n.Commits {
		if link.Commitment == c {
			return true
		}
	}
	return false
}

func (idx *archiveIndex) validateRenewal(r Renewal, renewer NodeID) error {
	if !r.Gap.Valid() {
		return fmt.Errorf("%w: renewal by %q has invalid gap", ErrInvalidArchive, renewer)
	}
	hasID := r.OldID != ""
	hasCommitment := !r.OldCommitment.IsZero()
	if !hasID && !hasCommitment {
		return fmt.Errorf("%w: renewal by %q names neither an old node nor a commitment", ErrUnboundRenewal, renewer)
	}
	if hasID {
		target, ok := idx.allNodes[r.OldID]
		if !ok {
			return fmt.Errorf("%w: renewal by %q names node %q", ErrUnknownTarget, renewer, r.OldID)
		}
		if r.OldContent != target.Content {
			return fmt.Errorf("%w: renewal by %q binds content %s but node %q has %s",
				ErrUnboundRenewal, renewer, r.OldContent, r.OldID, target.Content)
		}
		if hasCommitment {
			if r.OldCommitment != target.NodeCommitment() {
				shared, ok := idx.shared[r.OldCommitment]
				covered := false
				if ok {
					for _, id := range shared.Covers {
						if id == target.ID {
							covered = true
							break
						}
					}
				}
				if !covered {
					return fmt.Errorf("%w: commitment %s does not bind node %q", ErrUnboundRenewal, r.OldCommitment, target.ID)
				}
			}
		}
		return nil
	}
	if _, ok := idx.shared[r.OldCommitment]; !ok {
		return fmt.Errorf("%w: renewal by %q names commitment %s", ErrUnknownTarget, renewer, r.OldCommitment)
	}
	return nil
}

func (idx *archiveIndex) resolveReference(r Reference, allowHidden bool) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if r.Node != "" {
		n, ok := idx.allNodes[r.Node]
		if !ok {
			return fmt.Errorf("%w: reference to unknown node %q", ErrUnknownTarget, r.Node)
		}
		if !allowHidden && !n.Approved {
			return fmt.Errorf("%w: reference to hidden node %q", ErrDisallowedEvidence, r.Node)
		}
		return nil
	}
	if _, ok := idx.shared[r.Commitment]; !ok {
		return fmt.Errorf("%w: reference to unknown commitment %s", ErrUnknownTarget, r.Commitment)
	}
	return nil
}

// licenseMap returns the commitment disclosure permissions.  An explicit
// LicensedBy is authoritative.  If it is absent, any approved timestamp that
// signs the commitment via Commits is a valid permission; the lexicographically
// first such signer is used so certificates stay deterministic.
func (a *Archive) licenseMap(idx *archiveIndex) map[Commitment]NodeID {
	out := make(map[Commitment]NodeID)
	ids := make([]NodeID, 0, len(idx.approved))
	for id := range idx.approved {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		n := idx.approved[id]
		for _, link := range n.Commits {
			if _, ok := idx.shared[link.Commitment]; !ok {
				continue
			}
			if _, exists := out[link.Commitment]; !exists {
				out[link.Commitment] = id
			}
		}
	}
	for c, s := range idx.shared {
		if s.LicensedBy == "" {
			continue
		}
		if signer, ok := idx.approved[s.LicensedBy]; ok && (signer.commitsTo(c) || len(s.LicenseProof) != 0) {
			out[c] = s.LicensedBy
		}
	}
	return out
}

// ClockDomains returns the intersection of every approved record's signed
// offset bounds, grouped by permanent clock source.  A rotated signing key
// does not appear here at all: only Source (or Service when Source is empty)
// determines the clock variable.
func (a *Archive) ClockDomains() (map[ServiceID]Offset, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: nil archive", ErrInvalidArchive)
	}
	domains := make(map[ServiceID]Offset)
	for i := range a.Nodes {
		n := &a.Nodes[i]
		if !n.Approved {
			continue
		}
		src := n.ClockSource()
		if src == "" {
			return nil, fmt.Errorf("%w: node %q has empty clock source", ErrInvalidArchive, n.ID)
		}
		if !n.Offset.Valid() {
			return nil, fmt.Errorf("%w: node %q has empty offset interval", ErrInvalidArchive, n.ID)
		}
		cur, exists := domains[src]
		if !exists {
			domains[src] = n.Offset
			continue
		}
		next, ok := cur.Intersect(n.Offset)
		if !ok {
			return nil, fmt.Errorf("%w: source %q has disjoint bounds", ErrIncompatibleClocks, src)
		}
		domains[src] = next
	}
	return domains, nil
}
