// Package inference reconstructs the secret set hidden behind an
// intersection-size oracle.
//
// An intersection service is expected to leak only |S intersect Q| for
// every queried subset Q. A sequence of such answers can nevertheless
// pin down individual members of the secret set S. A Reasoner maintains
// every secret set that stays compatible with the observed answers over
// a finite candidate universe of at most MaxElements elements and a
// fixed secret cardinality.
//
// While the family of compatible sets fits inside the configured search
// budget the reasoner keeps it explicitly (ModeExact) and reports the
// first observation on which each element becomes uniquely determined.
// When enumeration or a feasibility search exhausts the budget it
// switches to sound per-element lower/upper bounds (ModeBounded): a
// member is only claimed forced when a budgeted constraint search proves
// it, otherwise it stays unknown.
package inference

import (
	"errors"
	"fmt"
	"math/bits"
)

// MaxElements is the largest supported candidate universe.
const MaxElements = 24

// DefaultBudget is the per-search candidate-check budget used by
// NewDefault.
const DefaultBudget uint64 = 1 << 20

// Mode describes how precisely the reasoner can track compatible sets.
type Mode int

const (
	// ModeExact keeps the full family of compatible secret sets.
	ModeExact Mode = iota + 1
	// ModeBounded keeps only proven per-element lower/upper bounds.
	ModeBounded
)

// Exported error values; parameterized failures wrap these sentinels.
var (
	ErrTooManyElements = errors.New("inference: candidate universe exceeds MaxElements")
	ErrBadCardinality  = errors.New("inference: secret cardinality out of range")
	ErrBadBudget       = errors.New("inference: search budget must be positive")
	ErrDuplicateName   = errors.New("inference: duplicate candidate name")
	ErrUnknownElement  = errors.New("inference: subset references an element outside the candidate universe")
	ErrBadResponse     = errors.New("inference: intersection count is infeasible for the queried subset")
	ErrInconsistent    = errors.New("inference: answers admit no secret set of the required cardinality")
)

type constraint struct {
	mask uint32
	want int
}

type searchOutcome int

const (
	outUnknown searchOutcome = iota // search budget exhausted before proof
	outSatisfiable
	outUnsatisfiable
)

// Reasoner ingests subset/count observations about one fixed secret set.
type Reasoner struct {
	universe []string
	index    map[string]int
	n        int
	k        int
	budget   uint64
	visited  uint64 // cumulative candidate checks across every search

	mode Mode
	dead bool // proven inconsistent

	// ModeExact state: every compatible k-subset as a bit mask.
	feasible []uint32

	// ModeBounded state.
	constraints []constraint
	seen        map[uint32]int
	status      []int8 // -1 unknown, 0 forced out, 1 forced in

	// Elements already surfaced through a Result, in both modes.
	reportedIn  uint32
	reportedOut uint32
}

// New builds a Reasoner over candidates for a secret set of the given
// cardinality. budget bounds the number of candidate secret sets a
// single enumeration or feasibility search may inspect; cumulative spend
// is available through NodesVisited. The candidate order is preserved in
// every returned slice.
func New(candidates []string, cardinality int, budget uint64) (*Reasoner, error) {
	n := len(candidates)
	if n > MaxElements {
		return nil, fmt.Errorf("%w: got %d elements", ErrTooManyElements, n)
	}
	if cardinality < 0 || cardinality > n {
		return nil, fmt.Errorf("%w: got cardinality %d for %d elements", ErrBadCardinality, cardinality, n)
	}
	if budget == 0 {
		return nil, ErrBadBudget
	}
	index := make(map[string]int, n)
	for i, name := range candidates {
		if _, dup := index[name]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateName, name)
		}
		index[name] = i
	}

	r := &Reasoner{
		universe: append([]string(nil), candidates...),
		index:    index,
		n:        n,
		k:        cardinality,
		budget:   budget,
		seen:     make(map[uint32]int),
		status:   make([]int8, n),
	}
	for i := range r.status {
		r.status[i] = -1
	}

	sets, exhausted := r.enumerateInitial()
	if exhausted {
		r.mode = ModeBounded
		r.applyCardinalityShortcut()
	} else {
		r.mode = ModeExact
		r.feasible = sets
	}
	r.reportedIn, r.reportedOut = r.currentMasks()
	return r, nil
}

// NewDefault creates a Reasoner with DefaultBudget.
func NewDefault(candidates []string, cardinality int) (*Reasoner, error) {
	return New(candidates, cardinality, DefaultBudget)
}

// Mode reports the active tracking precision.
func (r *Reasoner) Mode() Mode { return r.mode }

// IsConsistent reports whether at least one secret set still fits.
func (r *Reasoner) IsConsistent() bool { return !r.dead }

// Budget returns the per-search candidate-check budget.
func (r *Reasoner) Budget() uint64 { return r.budget }

// NodesVisited returns the cumulative candidate checks spent so far.
func (r *Reasoner) NodesVisited() uint64 { return r.visited }

// Universe returns the candidate names in construction order.
func (r *Reasoner) Universe() []string {
	return append([]string(nil), r.universe...)
}

// Observe records one oracle answer: count = |secret intersect subset|.
// Repeating an identical subset with the same count changes nothing;
// repeating it with a different count proves inconsistency. The Result
// surfaces elements uniquely determined for the first time by this
// observation.
func (r *Reasoner) Observe(subset []string, count int) (Result, error) {
	if r.dead {
		return r.Snapshot(), ErrInconsistent
	}
	mask, err := r.toMask(subset)
	if err != nil {
		return r.Snapshot(), err
	}
	maxCount := bits.OnesCount32(mask)
	if count < 0 || count > maxCount || count > r.k {
		return r.Snapshot(), fmt.Errorf("%w: count=%d subset_size=%d", ErrBadResponse, count, maxCount)
	}

	if previous, ok := r.seen[mask]; ok {
		if previous != count {
			r.dead = true
			return r.Snapshot(), fmt.Errorf("%w: subset previously answered %d, now %d", ErrInconsistent, previous, count)
		}
		return r.Snapshot(), nil
	}
	r.seen[mask] = count

	if r.mode == ModeExact {
		keep := r.feasible[:0]
		for _, candidate := range r.feasible {
			if bits.OnesCount32(candidate&mask) == count {
				keep = append(keep, candidate)
			}
		}
		r.feasible = keep
		if len(r.feasible) == 0 {
			r.dead = true
			return r.Snapshot(), fmt.Errorf("%w: observation eliminates every candidate set", ErrInconsistent)
		}
	} else {
		r.constraints = append(r.constraints, constraint{mask: mask, want: count})
		r.refineBounded()
		if r.dead {
			return r.Snapshot(), fmt.Errorf("%w: observation admits no compatible secret set", ErrInconsistent)
		}
	}
	return r.Snapshot(), nil
}

// ForcedIn returns every element proven to belong to the secret set.
func (r *Reasoner) ForcedIn() []string {
	in, _ := r.currentMasks()
	return r.maskNames(in)
}

// ForcedOut returns every element proven absent from the secret set.
func (r *Reasoner) ForcedOut() []string {
	_, out := r.currentMasks()
	return r.maskNames(out)
}

// Bounds returns per-element lower and upper membership bounds: lower is
// 1 only for forced-in elements, upper is 0 only for forced-out
// elements; unknown elements span [0, 1].
func (r *Reasoner) Bounds() (lower, upper map[string]int) {
	in, out := r.currentMasks()
	lower = make(map[string]int, r.n)
	upper = make(map[string]int, r.n)
	for i, name := range r.universe {
		bit := uint32(1) << uint(i)
		if in&bit != 0 {
			lower[name] = 1
		} else {
			lower[name] = 0
		}
		if out&bit != 0 {
			upper[name] = 0
		} else {
			upper[name] = 1
		}
	}
	return lower, upper
}

// PossibleSets returns all compatible secret sets in ModeExact and nil
// in ModeBounded. Each set lists its members in candidate order.
func (r *Reasoner) PossibleSets() [][]string {
	if r.mode != ModeExact || r.dead {
		return nil
	}
	sets := make([][]string, 0, len(r.feasible))
	for _, mask := range r.feasible {
		sets = append(sets, r.maskNames(mask))
	}
	return sets
}

// PossibleCount returns the number of compatible secret sets and whether
// that number is exact. In ModeBounded the count is -1.
func (r *Reasoner) PossibleCount() (count int, exact bool) {
	if r.mode == ModeExact && !r.dead {
		return len(r.feasible), true
	}
	return -1, false
}

// Snapshot builds a Result for the current state without changing it.
func (r *Reasoner) Snapshot() Result {
	in, out := r.currentMasks()
	newIn := in &^ r.reportedIn
	newOut := out &^ r.reportedOut
	r.reportedIn |= newIn
	r.reportedOut |= newOut

	lower, upper := r.Bounds()
	unknown := r.maskNames(r.fullMask() &^ (in | out))
	count, exact := r.PossibleCount()

	return Result{
		Mode:           r.mode,
		Consistent:     !r.dead,
		NewlyForcedIn:  r.maskNames(newIn),
		NewlyForcedOut: r.maskNames(newOut),
		ForcedIn:       r.maskNames(in),
		ForcedOut:      r.maskNames(out),
		Unknown:        unknown,
		LowerBound:     lower,
		UpperBound:     upper,
		PossibleCount:  count,
		CountExact:     exact,
		NodesVisited:   r.visited,
		Budget:         r.budget,
	}
}

// Result is a point-in-time view of the reasoner state.
type Result struct {
	Mode           Mode
	Consistent     bool
	NewlyForcedIn  []string
	NewlyForcedOut []string
	ForcedIn       []string
	ForcedOut      []string
	Unknown        []string
	LowerBound     map[string]int
	UpperBound     map[string]int
	// PossibleCount is the exact compatible-set count when CountExact is
	// true, otherwise -1.
	PossibleCount int
	CountExact    bool
	NodesVisited  uint64
	Budget        uint64
}

func (r *Reasoner) fullMask() uint32 {
	if r.n == 32 {
		return ^uint32(0)
	}
	return uint32(1)<<uint(r.n) - 1
}

func (r *Reasoner) toMask(subset []string) (uint32, error) {
	var mask uint32
	for _, name := range subset {
		pos, ok := r.index[name]
		if !ok {
			return 0, fmt.Errorf("%w: %q", ErrUnknownElement, name)
		}
		mask |= uint32(1) << uint(pos)
	}
	return mask, nil
}

func (r *Reasoner) maskNames(mask uint32) []string {
	names := make([]string, 0, bits.OnesCount32(mask))
	for i, name := range r.universe {
		if mask&(uint32(1)<<uint(i)) != 0 {
			names = append(names, name)
		}
	}
	return names
}

// currentMasks returns the forced-in and forced-out bit masks for the
// active mode.
func (r *Reasoner) currentMasks() (in, out uint32) {
	switch {
	case r.dead:
		// Freeze the last proven classification; Snapshot marks the
		// inconsistency separately.
		if r.mode == ModeBounded {
			for i, s := range r.status {
				if s == 1 {
					in |= uint32(1) << uint(i)
				} else if s == 0 {
					out |= uint32(1) << uint(i)
				}
			}
		}
		return in, out
	case r.mode == ModeExact:
		if len(r.feasible) > 0 {
			in = r.feasible[0]
			out = ^r.feasible[0]
			for _, mask := range r.feasible[1:] {
				in &= mask
				out &= ^mask
			}
			out &= r.fullMask()
		}
	default:
		for i, s := range r.status {
			if s == 1 {
				in |= uint32(1) << uint(i)
			} else if s == 0 {
				out |= uint32(1) << uint(i)
			}
		}
	}
	return in, out
}

// enumerateInitial lists every k-subset of the universe, aborting (and
// returning exhausted=true) once the per-search budget is exceeded.
func (r *Reasoner) enumerateInitial() (sets []uint32, exhausted bool) {
	if r.k == 0 {
		r.visited++
		return []uint32{0}, false
	}
	pool := r.fullMask()
	dense := uint32(1)<<uint(r.k) - 1
	last := (uint32(1)<<uint(r.k) - 1) << uint(r.n-r.k)
	found := make([]uint32, 0)
	var spent uint64
	for {
		spent++
		r.visited++
		if spent > r.budget {
			return nil, true
		}
		found = append(found, expand(dense, pool))
		if dense == last {
			return found, false
		}
		lo := dense & -dense
		dense = dense + lo + (((dense ^ (dense + lo)) >> 2) / lo)
	}
}

// refineBounded integrates the most recently appended constraint and
// upgrades per-element bounds wherever a bounded search yields a proof.
func (r *Reasoner) refineBounded() {
	if r.check(-1) == outUnsatisfiable {
		r.dead = true
		return
	}
	// An outUnknown global result only means that single search could not
	// finish; per-element searches each receive the full budget, so they
	// are still attempted below and every claim stays independently proven.

	// Elements touched by the new constraint are the most likely to
	// change, so probe them first while budget is fresh.
	last := r.constraints[len(r.constraints)-1].mask
	order := make([]int, 0, r.n)
	for i := 0; i < r.n; i++ {
		if r.status[i] == -1 && last&(uint32(1)<<uint(i)) != 0 {
			order = append(order, i)
		}
	}
	for i := 0; i < r.n; i++ {
		if r.status[i] == -1 && last&(uint32(1)<<uint(i)) == 0 {
			order = append(order, i)
		}
	}

	for _, element := range order {
		canIn := r.check(element)
		if canIn == outUnknown {
			continue
		}
		canOut := r.checkExcluded(element)
		if canOut == outUnknown {
			continue
		}
		switch {
		case canIn == outSatisfiable && canOut == outUnsatisfiable:
			r.status[element] = 1
		case canIn == outUnsatisfiable && canOut == outSatisfiable:
			r.status[element] = 0
		}
	}
	r.applyCardinalityShortcut()
	if !r.dead && r.check(-1) == outUnsatisfiable {
		r.dead = true
	}
}

// check searches for a compatible k-subset. probe >= 0 forces that
// element in; checkExcluded forces it out.
func (r *Reasoner) check(probe int) searchOutcome {
	return r.search(probe, true)
}

func (r *Reasoner) checkExcluded(probe int) searchOutcome {
	return r.search(probe, false)
}

func (r *Reasoner) search(probe int, probeIn bool) searchOutcome {
	// Restrict the enumeration universe using already-proven status and
	// the probe. The free pool must supply the remaining selections.
	var forcedIn, forcedOut uint32
	for i, s := range r.status {
		bit := uint32(1) << uint(i)
		switch s {
		case 1:
			forcedIn |= bit
		case 0:
			forcedOut |= bit
		}
	}
	if probe >= 0 {
		bit := uint32(1) << uint(probe)
		if probeIn {
			if forcedOut&bit != 0 {
				return outUnsatisfiable
			}
			forcedIn |= bit
		} else {
			if forcedIn&bit != 0 {
				return outUnsatisfiable
			}
			forcedOut |= bit
		}
	}

	free := r.fullMask() &^ (forcedIn | forcedOut)
	need := r.k - bits.OnesCount32(forcedIn)
	if need < 0 || need > bits.OnesCount32(free) {
		return outUnsatisfiable
	}

	// Every fixed member must already satisfy each constraint prefix;
	// the enumeration only varies the free pool below.
	for _, c := range r.constraints {
		if bits.OnesCount32(forcedIn&c.mask) > c.want {
			return outUnsatisfiable
		}
	}

	// Enumerate every need-sized subset of the free pool. The free pool
	// is compressed onto dense bit positions so Gosper's hack applies
	// directly; each visited candidate costs one budget unit and cost is
	// independent of where elements sit in the universe.
	poolSize := bits.OnesCount32(free)
	if need == 0 {
		r.visited++
		if r.satisfiesAll(forcedIn) {
			return outSatisfiable
		}
		return outUnsatisfiable
	}
	dense := uint32(1)<<uint(need) - 1
	last := (uint32(1)<<uint(need) - 1) << uint(poolSize-need)
	var spent uint64
	for {
		spent++
		r.visited++
		if spent > r.budget {
			return outUnknown
		}
		candidate := forcedIn | expand(dense, free)
		if r.satisfiesAll(candidate) {
			return outSatisfiable
		}
		if dense == last {
			return outUnsatisfiable
		}
		lo := dense & -dense
		dense = dense + lo + (((dense ^ (dense + lo)) >> 2) / lo)
	}
}

// satisfiesAll checks every stored intersection constraint against set.
func (r *Reasoner) satisfiesAll(set uint32) bool {
	for _, c := range r.constraints {
		if bits.OnesCount32(set&c.mask) != c.want {
			return false
		}
	}
	return true
}

// expand maps dense low-bit indices back onto the bit positions of pool.
func expand(dense, pool uint32) uint32 {
	var out uint32
	idx := uint(0)
	for pool != 0 {
		bit := pool & -pool
		if dense&(1<<idx) != 0 {
			out |= bit
		}
		idx++
		pool &^= bit
	}
	return out
}

// applyCardinalityShortcut marks every element forced by cardinality
// alone: all-in when k == n, all-out when k == 0.
func (r *Reasoner) applyCardinalityShortcut() {
	if r.k == 0 {
		for i := range r.status {
			r.status[i] = 0
		}
	} else if r.k == r.n {
		for i := range r.status {
			r.status[i] = 1
		}
	}
}
