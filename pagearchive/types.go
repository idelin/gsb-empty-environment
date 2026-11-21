package pagearchive

import "errors"

const (
	MaxPages           = 16
	MaxCachePages      = 3
	MaxDependencyDepth = 4
	MaxBaseCandidates  = 3
)

var (
	// ErrNoFeasibleLayout is returned when no layout satisfies the budget and
	// dependency constraints.
	ErrNoFeasibleLayout = errors.New("pagearchive: no feasible layout")

	// ErrInvalidInput is returned when planner or decoder input is malformed.
	ErrInvalidInput = errors.New("pagearchive: invalid input")
)

// CandidateEdge says page To may be stored as a patch relative to page From.
type CandidateEdge struct {
	From int
	To   int
}

// Representation is one globally selected storage representation for a page.
type Representation struct {
	Page int

	// Full is true for an entire-page record. When false, Base identifies the
	// page whose decoded content is needed to reconstruct Page.
	Full bool

	// Base is ignored when Full is true.
	Base int

	Payload []byte

	// MetadataCost is the fixed persistent metadata charge for this record.
	MetadataCost int
}

// Layout is the selected representation for every page.
type Layout struct {
	Representations []Representation

	// PersistentBytes is the sum of payload and metadata costs.
	PersistentBytes int

	// MaxSnapshotReadBytes is the largest snapshot read total in the layout.
	MaxSnapshotReadBytes int

	// SnapshotReadBytes contains one actual read total per input snapshot.
	SnapshotReadBytes []int

	// DependencyDepth is the longest base chain in the selected layout.
	DependencyDepth int
}

// PlanInput describes pages, access workloads, and permitted patch edges.
type PlanInput struct {
	// Pages contains equal-length real page bytes. There are at most 16 pages.
	Pages [][]byte

	// Snapshots contains ordered page IDs. Each snapshot starts with an empty
	// independent cache.
	Snapshots [][]int

	// CandidateEdges is the directed set of allowed patch references.
	CandidateEdges []CandidateEdge

	// MetadataCost is charged once for every selected page representation.
	MetadataCost int

	// Budget is a hard inclusive upper bound on PersistentBytes.
	Budget int

	// MaxDepth is the inclusive maximum dependency-chain depth. It must be at
	// most four.
	MaxDepth int

	// CacheCapacity is the number of decoded pages retained by each snapshot.
	// It must be at most three.
	CacheCapacity int
}

// PersistentCost returns the cost that a representation would charge.
func (r Representation) PersistentCost() int {
	return len(r.Payload) + r.MetadataCost
}

// PersistentCost returns the total abstract persistent cost of a layout.
func (l Layout) PersistentCost() int {
	total := 0
	for _, representation := range l.Representations {
		if representation.PersistentCost() > int(^uint(0)>>1)-total {
			return -1
		}
		total += representation.PersistentCost()
	}
	return total
}
