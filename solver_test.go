package netsec

import (
	"math/rand"
	"testing"
)

// bruteSpec mirrors the public constraint set for the 8-bit reference.
type bruteSpec struct {
	ranges    []rangeConstraint
	masks     []maskConstraint
	diffs     []diffConstraint
	sensitive sensitiveRange
}

// inWrap reports whether v belongs to the inclusive range [lo, hi], where
// lo > hi means the range wraps around zero.
func inWrap(v, lo, hi, mask uint64) bool {
	if lo <= hi {
		return v >= lo && v <= hi
	}
	return v >= lo || v <= hi
}

// bruteForce enumerates all width-bit pairs and returns the lexicographically
// smallest satisfying pair. ok=false means unsatisfiable. It is the trusted
// reference used to cross-check the bounded solver on 8-bit instances.
func bruteForce(width uint, spec bruteSpec) (x0, x1 uint64, ok bool) {
	mask := uint64(1)<<width - 1
	for x := uint64(0); x <= mask; x++ {
		for y := uint64(0); y <= mask; y++ {
			vars := [2]uint64{x, y}
			valid := true
			for _, rc := range spec.ranges {
				if !inWrap(vars[rc.variable], rc.lo, rc.hi, mask) {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			for _, mc := range spec.masks {
				if vars[mc.variable]&mc.mask != mc.pattern {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			for _, dc := range spec.diffs {
				if (vars[dc.a]-vars[dc.b])&mask != dc.delta {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			if spec.sensitive.set {
				if !inWrap(x, spec.sensitive.lo0, spec.sensitive.hi0, mask) ||
					!inWrap(y, spec.sensitive.lo1, spec.sensitive.hi1, mask) {
					continue
				}
			}
			return x, y, true
		}
	}
	return 0, 0, false
}

func newEightBit(spec bruteSpec) *Solver {
	sl := newSolver(8)
	sl.ranges = append(sl.ranges, spec.ranges...)
	sl.masks = append(sl.masks, spec.masks...)
	sl.diffs = append(sl.diffs, spec.diffs...)
	sl.sensitive = spec.sensitive
	return sl
}

// expectBrute runs the bounded solver with a generous budget and asserts it
// agrees with exhaustive enumeration, including the exact minimal pair.
func expectBrute(t *testing.T, name string, spec bruteSpec) {
	t.Helper()
	want0, want1, wantOK := bruteForce(8, spec)
	sl := newEightBit(spec)
	sl.SetBudget(1_000_000)
	got := sl.Solve()
	if !wantOK {
		if got.Status != StatusUnsatisfiable {
			t.Fatalf("%s: want unsatisfiable, got %s (%d,%d)", name, got.Status, got.Addr0, got.Addr1)
		}
		return
	}
	if got.Status != StatusSatisfiable {
		t.Fatalf("%s: want satisfiable, got %s", name, got.Status)
	}
	if uint64(got.Addr0) != want0 || uint64(got.Addr1) != want1 {
		t.Fatalf("%s: want minimal pair (%d,%d), got (%d,%d)",
			name, want0, want1, got.Addr0, got.Addr1)
	}
}

// TestSubtractionBorrow exercises modular subtraction where the unsigned
// subtraction wraps (borrow out of the 8-bit word).
func TestSubtractionBorrow(t *testing.T) {
	// x - y == 5 with x pinned to 2 forces y = 2 - 5 = 253 via borrow.
	spec := bruteSpec{
		ranges: []rangeConstraint{{variable: 0, lo: 2, hi: 2}},
		diffs:  []diffConstraint{{a: 0, b: 1, delta: 5}},
	}
	expectBrute(t, "borrow", spec)

	// Reverse direction: y - x == 251, with y inside a wrapping interval.
	spec = bruteSpec{
		ranges: []rangeConstraint{
			{variable: 0, lo: 2, hi: 2},
			{variable: 1, lo: 250, hi: 10}, // [250,255] U [0,10]
		},
		diffs: []diffConstraint{{a: 1, b: 0, delta: 251}},
	}
	expectBrute(t, "borrow-reverse-wrap", spec)
}

// TestMutuallyExclusiveMasks places contradictory mask requirements on the
// same variable, and a compatible mask pair interacting with a difference.
func TestMutuallyExclusiveMasks(t *testing.T) {
	spec := bruteSpec{
		masks: []maskConstraint{
			{variable: 0, mask: 0xF0, pattern: 0x10},
			{variable: 0, mask: 0xF0, pattern: 0x20},
		},
	}
	expectBrute(t, "contradictory-masks", spec)

	spec = bruteSpec{
		masks: []maskConstraint{
			{variable: 0, mask: 0xF0, pattern: 0x30},
			{variable: 1, mask: 0x0F, pattern: 0x0C},
		},
		// x in 0x30.., y has low nibble 0xC; delta 0x10 admits x=0x3c, y=0x2c.
		diffs: []diffConstraint{{a: 0, b: 1, delta: 0x10}},
	}
	expectBrute(t, "compatible-masks-diff", spec)
}

// TestWrapIntervals covers ranges that look disjoint in linear order but
// intersect once wrap-around is taken into account.
func TestWrapIntervals(t *testing.T) {
	// [250,5] and [3,252] only overlap on [3,5]; linear order says disjoint.
	spec := bruteSpec{
		ranges: []rangeConstraint{
			{variable: 0, lo: 250, hi: 5},
			{variable: 0, lo: 3, hi: 252},
		},
	}
	expectBrute(t, "wrap-intersection", spec)

	// Wrapping sensitive regions whose only feasible solutions sit in the
	// zero-adjacent tail of both variables.
	spec = bruteSpec{
		ranges: []rangeConstraint{
			{variable: 0, lo: 250, hi: 4},
			{variable: 1, lo: 251, hi: 6},
		},
		diffs: []diffConstraint{{a: 1, b: 0, delta: 2}},
		sensitive: sensitiveRange{
			lo0: 252, hi0: 3,
			lo1: 254, hi1: 5,
			set: true,
		},
	}
	expectBrute(t, "wrap-sensitive-tail", spec)
}

// TestFuzzAgainstBruteForce generates random 8-bit instances and checks the
// bounded solver against full enumeration across every constraint type.
func TestFuzzAgainstBruteForce(t *testing.T) {
	for _, seed := range []int64{20261112, 20261113, 20261114} {
		rng := rand.New(rand.NewSource(seed))
		runFuzzSeed(t, rng)
	}
}

func runFuzzSeed(t *testing.T, rng *rand.Rand) {
	t.Helper()
	mask8 := uint64(0xFF)
	for iter := 0; iter < 400; iter++ {
		spec := bruteSpec{}

		for v := 0; v < 2; v++ {
			if rng.Intn(2) == 0 {
				lo := rng.Uint64() & mask8
				hi := rng.Uint64() & mask8
				spec.ranges = append(spec.ranges, rangeConstraint{variable: v, lo: lo, hi: hi})
			}
			if rng.Intn(2) == 0 {
				m := rng.Uint64() & mask8
				p := rng.Uint64() & m
				spec.masks = append(spec.masks, maskConstraint{variable: v, mask: m, pattern: p})
			}
		}
		switch rng.Intn(3) {
		case 0:
			spec.diffs = append(spec.diffs, diffConstraint{a: 0, b: 1, delta: rng.Uint64() & mask8})
		case 1:
			spec.diffs = append(spec.diffs, diffConstraint{a: 1, b: 0, delta: rng.Uint64() & mask8})
		}
		if rng.Intn(2) == 0 {
			spec.sensitive = sensitiveRange{
				lo0: rng.Uint64() & mask8,
				hi0: rng.Uint64() & mask8,
				lo1: rng.Uint64() & mask8,
				hi1: rng.Uint64() & mask8,
				set: true,
			}
		}

		want0, want1, wantOK := bruteForce(8, spec)
		sl := newEightBit(spec)
		sl.SetBudget(100_000)
		got := sl.Solve()
		if got.Status == StatusUnknown || wantOK != (got.Status == StatusSatisfiable) ||
			(wantOK && (uint64(got.Addr0) != want0 || uint64(got.Addr1) != want1)) {
			t.Logf("iter %d spec: ranges=%+v masks=%+v diffs=%+v sensitive=%+v",
				iter, spec.ranges, spec.masks, spec.diffs, spec.sensitive)
		}
		if got.Status == StatusUnknown {
			t.Fatalf("iter %d: 8-bit instance must stay within budget, got unknown", iter)
		}
		if wantOK != (got.Status == StatusSatisfiable) {
			t.Fatalf("iter %d: satisfiability mismatch (want ok=%v)", iter, wantOK)
		}
		if wantOK && (uint64(got.Addr0) != want0 || uint64(got.Addr1) != want1) {
			t.Fatalf("iter %d: pair mismatch want (%d,%d) got (%d,%d)",
				iter, want0, want1, got.Addr0, got.Addr1)
		}
	}
}

// TestThirtyTwoBitBorrow checks the production 32-bit API on a subtraction
// that must borrow across the word boundary.
func TestThirtyTwoBitBorrow(t *testing.T) {
	sl := NewSolver()
	if err := sl.AddIntervalConstraint(0, 0x10, 0x10); err != nil {
		t.Fatal(err)
	}
	if err := sl.AddDifferenceConstraint(0, 1, 0x20); err != nil {
		t.Fatal(err)
	}
	got := sl.Solve()
	if got.Status != StatusSatisfiable || got.Addr0 != 0x10 || got.Addr1 != 0xFFFFFFF0 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

// TestThirtyTwoBitMinimalPair asserts lexicographic minimization across
// interval, mask and difference constraints.
func TestThirtyTwoBitMinimalPair(t *testing.T) {
	sl := NewSolver()
	// Confine x to a small window and require low nibble 0xC: the mask pushes
	// the minimal x from 0x1005 up to 0x100C, exercising mask enumeration over
	// a dense, cheap-to-enumerate mask.
	if err := sl.AddIntervalConstraint(0, 0x1005, 0x10FF); err != nil {
		t.Fatal(err)
	}
	if err := sl.AddMaskConstraint(0, 0x0000000F, 0x0000000C); err != nil {
		t.Fatal(err)
	}
	if err := sl.AddDifferenceConstraint(1, 0, 0x100); err != nil {
		t.Fatal(err)
	}
	sl.SetSensitiveRegion(0, 0xFFFFFFFF, 0, 0xFFFFFFFF)
	got := sl.Solve()
	if got.Status != StatusSatisfiable || got.Addr0 != 0x100C || got.Addr1 != 0x110C {
		t.Fatalf("unexpected minimal pair: %+v", got)
	}
}

// TestThirtyTwoBitWrapSensitive puts the only solution in a zero-wrapping
// sensitive region.
func TestThirtyTwoBitWrapSensitive(t *testing.T) {
	sl := NewSolver()
	// y = x + 1: the smallest pair inside both wrapping regions is
	// (0xFFFFFFF8, 0xFFFFFFF9).
	if err := sl.AddDifferenceConstraint(1, 0, 1); err != nil {
		t.Fatal(err)
	}
	// Both sensitive regions wrap zero. Restrict x to the high tail explicitly
	// so the solver cannot pick the zero-head pair (0,1); the smallest feasible
	// pair is then (0xFFFFFFF8, 0xFFFFFFF9).
	if err := sl.AddIntervalConstraint(0, 0xFFFFFFF8, 0xFFFFFFFF); err != nil {
		t.Fatal(err)
	}
	sl.SetSensitiveRegion(0xFFFFFFF8, 0x2, 0xFFFFFFF9, 0x1)
	got := sl.Solve()
	if got.Status != StatusSatisfiable || got.Addr0 != 0xFFFFFFF8 || got.Addr1 != 0xFFFFFFF9 {
		t.Fatalf("unexpected wrap pair: %+v", got)
	}
}

// TestBudgetExhausted verifies that a search exceeding the budget yields
// StatusUnknown instead of a guess, while the same instance with a workable
// budget returns the true result. The contradiction used here is between two
// unary mask constraints on different variables plus a difference, so arc
// consistency cannot detect it without branching.
func TestBudgetExhausted(t *testing.T) {
	build := func() *Solver {
		sl := NewSolver()
		// Both variables are confined to small low windows.
		if err := sl.AddIntervalConstraint(0, 0, 0xFFFF); err != nil {
			t.Fatal(err)
		}
		if err := sl.AddIntervalConstraint(1, 0, 0xFFFF); err != nil {
			t.Fatal(err)
		}
		// The two mask domains are disjoint (x ≡ 0x03 mod 16 vs y ≡ 0x0C mod
		// 16), while x == y is required. Arc consistency keeps both full
		// fragment sets, so proving emptiness requires exhaustive branching.
		if err := sl.AddMaskConstraint(0, 0x0000000F, 0x00000003); err != nil {
			t.Fatal(err)
		}
		if err := sl.AddMaskConstraint(1, 0x0000000F, 0x0000000C); err != nil {
			t.Fatal(err)
		}
		if err := sl.AddDifferenceConstraint(0, 1, 0); err != nil {
			t.Fatal(err)
		}
		return sl
	}

	tight := build()
	tight.SetBudget(16)
	if got := tight.Solve(); got.Status != StatusUnknown {
		t.Fatalf("want StatusUnknown under tight budget, got %+v", got)
	}

	full := build()
	full.SetBudget(defaultBudget)
	if got := full.Solve(); got.Status != StatusUnsatisfiable {
		t.Fatalf("want StatusUnsatisfiable with full budget, got %+v", got)
	}
}

// TestConstraintErrors covers the input-validation surface of the API.
func TestConstraintErrors(t *testing.T) {
	sl := NewSolver()
	if err := sl.AddIntervalConstraint(7, 0, 1); err == nil {
		t.Fatal("expected error for unknown variable")
	}
	if err := sl.AddMaskConstraint(0, 0x0F, 0x10); err == nil {
		t.Fatal("expected error for pattern bits outside mask")
	}
	if err := sl.AddDifferenceConstraint(0, 2, 0); err == nil {
		t.Fatal("expected error for unknown difference variable")
	}
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusUnknown:       "unknown",
		StatusSatisfiable:   "satisfiable",
		StatusUnsatisfiable: "unsatisfiable",
	}
	for st, want := range cases {
		if st.String() != want {
			t.Fatalf("status %d: want %q, got %q", st, want, st.String())
		}
	}
}

// TestSolverReusable verifies that a tight-budget Unknown answer does not
// poison the solver: raising the budget on the same accumulated constraints
// still yields the true result.
func TestSolverReusable(t *testing.T) {
	sl := NewSolver()
	if err := sl.AddIntervalConstraint(0, 0, 0xFFFF); err != nil {
		t.Fatal(err)
	}
	if err := sl.AddIntervalConstraint(1, 0, 0xFFFF); err != nil {
		t.Fatal(err)
	}
	if err := sl.AddDifferenceConstraint(0, 1, 42); err != nil {
		t.Fatal(err)
	}
	sl.SetBudget(4)
	if got := sl.Solve(); got.Status != StatusUnknown {
		t.Fatalf("first run: want unknown, got %+v", got)
	}
	sl.SetBudget(defaultBudget)
	got := sl.Solve()
	if got.Status != StatusSatisfiable || got.Addr0 != 42 || got.Addr1 != 0 {
		t.Fatalf("second run: want (42,0), got %+v", got)
	}
}

// TestNoSensitiveRegion checks the default behavior where every address in
// the 32-bit space is considered sensitive (the search still minimizes).
func TestNoSensitiveRegion(t *testing.T) {
	sl := NewSolver()
	if err := sl.AddDifferenceConstraint(1, 0, 5); err != nil {
		t.Fatal(err)
	}
	got := sl.Solve()
	if got.Status != StatusSatisfiable || got.Addr0 != 0 || got.Addr1 != 5 {
		t.Fatalf("want minimal pair (0,5), got %+v", got)
	}
}
