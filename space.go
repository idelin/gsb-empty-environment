package netsec

import (
	"fmt"
	"sort"
)

// State is the safety classification of a region of the input space.
type State int

const (
	// Unknown means the region has not been assessed yet.
	Unknown State = iota
	// Safe means the region is proven safe by attached evidence.
	Safe
	// Danger means the region is proven dangerous by attached evidence.
	Danger
)

func (s State) String() string {
	switch s {
	case Safe:
		return "safe"
	case Danger:
		return "danger"
	default:
		return "unknown"
	}
}

// Evidence justifies a state claim over a region. A region's state may only
// change when new evidence is supplied.
type Evidence struct {
	// ID uniquely identifies the evidence (e.g. an analysis report ID).
	ID string
	// State is the state this evidence claims for the region. It is set by
	// Mark and needs not be filled in by the caller.
	State State
	// Note is an optional human-readable description.
	Note string
}

// Region is a non-overlapping piece of the input space with a uniform state.
type Region struct {
	Box   Box
	State State
	// Evidence holds every evidence record submitted for this exact area,
	// including conflicting claims. The established State is the first
	// evidenced claim; later conflicting claims are recorded here but do
	// not overwrite it.
	Evidence []Evidence
}

// Space manages the safety partition of one finite integer box.
type Space struct {
	root    Box
	regions []Region
}

// NewSpace creates a Space covering root, initially entirely Unknown.
func NewSpace(root Box) (*Space, error) {
	if root.Dims < 1 || root.Dims > MaxDims {
		return nil, fmt.Errorf("netsec: dimension count %d out of range [1,%d]", root.Dims, MaxDims)
	}
	if root.Empty() {
		return nil, fmt.Errorf("netsec: root box %s is empty", root)
	}
	return &Space{
		root:    root,
		regions: []Region{{Box: root, State: Unknown}},
	}, nil
}

// Root returns the bounding box of the whole space.
func (s *Space) Root() Box { return s.root }

// Mark records an evidenced state claim over claim. The claim must lie
// inside the root box and carry a non-empty evidence ID.
//
// Overlaps with existing regions are split out so that every region keeps a
// uniform state. Where the claim conflicts with an already evidenced state,
// the established state is kept (no last-write-wins) and the new evidence is
// appended alongside the existing records, so both sides are preserved.
func (s *Space) Mark(claim Box, st State, ev Evidence) error {
	if st != Safe && st != Danger {
		return fmt.Errorf("netsec: can only mark %s or %s, not %s", Safe, Danger, st)
	}
	if ev.ID == "" {
		return fmt.Errorf("netsec: evidence ID is required to change state")
	}
	if claim.Dims != s.root.Dims {
		return fmt.Errorf("netsec: claim has %d dims, space has %d", claim.Dims, s.root.Dims)
	}
	if claim.Empty() {
		return fmt.Errorf("netsec: claim box is empty")
	}
	if !s.root.ContainsBox(claim) {
		return fmt.Errorf("netsec: claim %s escapes root %s", claim, s.root)
	}
	ev.State = st

	next := make([]Region, 0, len(s.regions)+1)
	for _, r := range s.regions {
		inter, ok := r.Box.Intersect(claim)
		if !ok {
			next = append(next, r)
			continue
		}
		for _, rem := range Subtract(r.Box, inter) {
			next = append(next, Region{Box: rem, State: r.State, Evidence: cloneEvidence(r.Evidence)})
		}
		piece := Region{Box: inter, State: r.State, Evidence: cloneEvidence(r.Evidence)}
		if r.State == Unknown {
			piece.State = st
		}
		if !hasEvidence(piece.Evidence, ev.ID) {
			piece.Evidence = append(piece.Evidence, ev)
		}
		next = append(next, piece)
	}
	s.regions = next
	return nil
}

// Coverage returns the current partition: disjoint regions whose union is
// exactly the root box, sorted by box coordinates.
func (s *Space) Coverage() []Region {
	out := make([]Region, len(s.regions))
	for i, r := range s.regions {
		out[i] = Region{Box: r.Box, State: r.State, Evidence: cloneEvidence(r.Evidence)}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Box, out[j].Box
		for d := 0; d < a.Dims; d++ {
			if a.Lo[d] != b.Lo[d] {
				return a.Lo[d] < b.Lo[d]
			}
			if a.Hi[d] != b.Hi[d] {
				return a.Hi[d] < b.Hi[d]
			}
		}
		return false
	})
	return out
}

// Report groups the current regions by state: Safe lists inputs proven safe,
// Danger lists inputs proven dangerous, and Pending lists inputs still
// awaiting verification.
type Report struct {
	Safe    []Region
	Danger  []Region
	Pending []Region
}

// Report builds the safe/danger/pending listing of the whole space.
func (s *Space) Report() Report {
	var rep Report
	for _, r := range s.Coverage() {
		switch r.State {
		case Safe:
			rep.Safe = append(rep.Safe, r)
		case Danger:
			rep.Danger = append(rep.Danger, r)
		default:
			rep.Pending = append(rep.Pending, r)
		}
	}
	return rep
}

// StateAt returns the state of the region containing p, or false if p lies
// outside the root box.
func (s *Space) StateAt(p [MaxDims]int64) (State, bool) {
	r, ok := s.regionAt(p)
	if !ok {
		return Unknown, false
	}
	return r.State, true
}

// RegionAt returns the region containing p, or false if p lies outside the
// root box.
func (s *Space) RegionAt(p [MaxDims]int64) (Region, bool) {
	r, ok := s.regionAt(p)
	if !ok {
		return Region{}, false
	}
	r.Evidence = cloneEvidence(r.Evidence)
	return r, true
}

func (s *Space) regionAt(p [MaxDims]int64) (Region, bool) {
	if !s.root.ContainsPoint(p) {
		return Region{}, false
	}
	for _, r := range s.regions {
		if r.Box.ContainsPoint(p) {
			return r, true
		}
	}
	return Region{}, false
}

// Validate checks the internal invariants: regions are non-empty, inside the
// root, pairwise disjoint, cover the root exactly, and every non-Unknown
// region carries at least one evidence record.
func (s *Space) Validate() error {
	var total int64
	for i, r := range s.regions {
		if r.Box.Dims != s.root.Dims || r.Box.Empty() || !s.root.ContainsBox(r.Box) {
			return fmt.Errorf("netsec: region %d box %s invalid for root %s", i, r.Box, s.root)
		}
		if r.State != Unknown && len(r.Evidence) == 0 {
			return fmt.Errorf("netsec: region %d (%s) is %s without evidence", i, r.Box, r.State)
		}
		for j := i + 1; j < len(s.regions); j++ {
			if r.Box.Intersects(s.regions[j].Box) {
				return fmt.Errorf("netsec: regions %d and %d overlap: %s vs %s", i, j, r.Box, s.regions[j].Box)
			}
		}
		total += r.Box.Volume()
	}
	if total != s.root.Volume() {
		return fmt.Errorf("netsec: coverage volume %d != root volume %d (gap or overlap)", total, s.root.Volume())
	}
	return nil
}

func cloneEvidence(ev []Evidence) []Evidence {
	if ev == nil {
		return nil
	}
	out := make([]Evidence, len(ev))
	copy(out, ev)
	return out
}

func hasEvidence(ev []Evidence, id string) bool {
	for _, e := range ev {
		if e.ID == id {
			return true
		}
	}
	return false
}
