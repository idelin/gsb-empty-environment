// Package netsec implements a small finite-domain solver used by the binary
// review service to decide whether plugin-computed memory addresses can reach
// a sensitive key region.
//
// Addresses are unsigned modular integers of a fixed width (32 bits in
// production). The supported constraints are:
//
//   - masked equality: (addr & mask) == pattern
//   - unsigned inclusive intervals, which may wrap around zero
//   - the modular difference of two addresses: (a - b) mod 2^width == delta
//
// The solver searches for the lexicographically smallest pair of unsigned
// addresses that satisfies every constraint and lies inside the sensitive
// region. Search is bounded by a work budget; when the budget is spent before
// the answer is known, Solve returns StatusUnknown.
package netsec

import "fmt"

// Status is the outcome of a solver run.
type Status int

const (
	// StatusUnknown means the budget was exhausted before satisfiability was
	// decided; it is never a proof of impossibility.
	StatusUnknown Status = iota
	// StatusSatisfiable means a pair was found; Result.Addr0/Addr1 carry it.
	StatusSatisfiable
	// StatusUnsatisfiable means no address pair can satisfy the constraints.
	StatusUnsatisfiable
)

// String implements fmt.Stringer.
func (st Status) String() string {
	switch st {
	case StatusSatisfiable:
		return "satisfiable"
	case StatusUnsatisfiable:
		return "unsatisfiable"
	default:
		return "unknown"
	}
}

// defaultBudget limits propagation passes, mask enumeration runs and search
// branches for a single Solve call.
const defaultBudget int64 = 20_000_000

type rangeConstraint struct {
	variable int
	lo       uint64
	hi       uint64
}

type maskConstraint struct {
	variable int
	mask     uint64
	pattern  uint64
}

type sensitiveRange struct {
	lo0 uint64
	hi0 uint64
	lo1 uint64
	hi1 uint64
	set bool
}

// Solver accumulates constraints over two address variables (index 0 and 1)
// and answers reachability queries against one sensitive region.
type Solver struct {
	width     uint
	widthMask uint64
	ranges    []rangeConstraint
	masks     []maskConstraint
	diffs     []diffConstraint
	sensitive sensitiveRange
	budget    int64
}

// NewSolver creates a 32-bit address solver with the default budget.
func NewSolver() *Solver {
	return newSolver(32)
}

// newSolver creates a solver for the given address width; the 8-bit variant is
// used by the exhaustive reference tests.
func newSolver(width uint) *Solver {
	if width == 0 || width > 64 {
		panic("netsec: unsupported solver width")
	}
	return &Solver{
		width:     width,
		widthMask: (uint64(1) << width) - 1,
		budget:    defaultBudget,
	}
}

// SetBudget overrides the work-unit budget for the next Solve call. A
// non-positive value keeps the default budget.
func (sl *Solver) SetBudget(workUnits int64) {
	if workUnits > 0 {
		sl.budget = workUnits
	}
}

func (sl *Solver) checkVariable(variable int) error {
	if variable != 0 && variable != 1 {
		return fmt.Errorf("netsec: unknown address variable %d (only 0 and 1 exist)", variable)
	}
	return nil
}

// AddIntervalConstraint requires variable to belong to the inclusive unsigned
// range [lo, hi]. When lo > hi the range wraps around zero and denotes
// [lo, max] U [0, hi].
func (sl *Solver) AddIntervalConstraint(variable int, lo, hi uint32) error {
	if err := sl.checkVariable(variable); err != nil {
		return err
	}
	sl.ranges = append(sl.ranges, rangeConstraint{
		variable: variable,
		lo:       uint64(lo),
		hi:       uint64(hi),
	})
	return nil
}

// AddMaskConstraint requires (value(variable) & mask) == pattern. It returns
// an error when pattern fixes bits that mask declares free.
func (sl *Solver) AddMaskConstraint(variable int, mask, pattern uint32) error {
	if err := sl.checkVariable(variable); err != nil {
		return err
	}
	if uint64(pattern)&^uint64(mask) != 0 {
		return fmt.Errorf("netsec: mask pattern %#x sets bits outside mask %#x", pattern, mask)
	}
	sl.masks = append(sl.masks, maskConstraint{
		variable: variable,
		mask:     uint64(mask),
		pattern:  uint64(pattern),
	})
	return nil
}

// AddDifferenceConstraint requires
// (value(a) - value(b)) mod 2^width == delta, with all values unsigned.
func (sl *Solver) AddDifferenceConstraint(a, b int, delta uint32) error {
	if err := sl.checkVariable(a); err != nil {
		return err
	}
	if err := sl.checkVariable(b); err != nil {
		return err
	}
	sl.diffs = append(sl.diffs, diffConstraint{
		a:     a,
		b:     b,
		delta: uint64(delta),
	})
	return nil
}

// SetSensitiveRegion marks the inclusive unsigned region both addresses must
// fall into. Either bound pair may wrap around zero when its lower bound is
// greater than its upper bound. Calling this method again replaces the
// previous region.
func (sl *Solver) SetSensitiveRegion(lo0, hi0, lo1, hi1 uint32) {
	sl.sensitive = sensitiveRange{
		lo0: uint64(lo0),
		hi0: uint64(hi0),
		lo1: uint64(lo1),
		hi1: uint64(hi1),
		set: true,
	}
}

// Result is the answer returned by Solve.
type Result struct {
	// Status is Satisfiable, Unsatisfiable or Unknown.
	Status Status
	// Addr0 and Addr1 are the smallest unsigned satisfying pair; they are zero
	// unless Status is StatusSatisfiable.
	Addr0 uint32
	Addr1 uint32
}

// Solve runs the bounded search. The solver is reusable: constraints are kept
// and every call gets a fresh budget.
func (sl *Solver) Solve() Result {
	inner := &solver{widthMask: sl.widthMask, budget: sl.budget}

	// Build each variable's base domain from interval and sensitive-region
	// constraints first, so mask enumeration only runs over the already
	// restricted space instead of the full 32-bit universe.
	domains := [][]interval{fullDomain(sl.widthMask), fullDomain(sl.widthMask)}
	for _, rc := range sl.ranges {
		domains[rc.variable] = intersectDomains(domains[rc.variable],
			wrapRange(rc.lo, rc.hi, sl.widthMask))
		if len(domains[rc.variable]) == 0 {
			return Result{Status: StatusUnsatisfiable}
		}
	}
	if sl.sensitive.set {
		domains[0] = intersectDomains(domains[0],
			wrapRange(sl.sensitive.lo0, sl.sensitive.hi0, sl.widthMask))
		domains[1] = intersectDomains(domains[1],
			wrapRange(sl.sensitive.lo1, sl.sensitive.hi1, sl.widthMask))
		if len(domains[0]) == 0 || len(domains[1]) == 0 {
			return Result{Status: StatusUnsatisfiable}
		}
	}
	for _, mc := range sl.masks {
		dom, ok := filterMask(domains[mc.variable], mc, sl.widthMask, &inner.budget)
		if !ok {
			return Result{Status: StatusUnknown}
		}
		domains[mc.variable] = dom
		if len(dom) == 0 {
			return Result{Status: StatusUnsatisfiable}
		}
	}
	inner.diffs = append(inner.diffs, sl.diffs...)

	x0, x1, status := inner.search(domains)
	if status != StatusSatisfiable {
		return Result{Status: status}
	}
	return Result{
		Status: StatusSatisfiable,
		Addr0:  uint32(x0),
		Addr1:  uint32(x1),
	}
}
