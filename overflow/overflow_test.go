package overflow

import (
	"math/big"
	"testing"
)

func edges(n int) [][]Edge { return make([][]Edge, n) }

func rats(vals ...*big.Rat) []*big.Rat { return vals }

func assertRat(t *testing.T, name string, got, want *big.Rat) {
	t.Helper()
	if got.Cmp(want) != 0 {
		t.Fatalf("%s: got %s, want %s", name, got, want)
	}
}

// Two sources overflow into one shared downstream container; the
// downstream level must equal the exact sum of both contributions.
func TestMultiSourceMerge(t *testing.T) {
	e := edges(3)
	e[0] = []Edge{{To: 2, Fraction: Rat(1, 1)}}
	e[1] = []Edge{{To: 2, Fraction: Rat(1, 1)}}
	n, err := NewNetwork(3,
		rats(Rat(1, 1), Rat(1, 1), Rat(10, 1)),
		rats(Rat(3, 1), Rat(4, 1), Rat(0, 1)),
		e)
	if err != nil {
		t.Fatal(err)
	}
	n.Tick()
	assertRat(t, "level[0]", n.Levels[0], Rat(1, 1))
	assertRat(t, "level[1]", n.Levels[1], Rat(1, 1))
	assertRat(t, "level[2]", n.Levels[2], Rat(5, 1)) // 2 + 3 merged
	assertRat(t, "total", n.Total(), Rat(7, 1))
	assertRat(t, "discharged", n.TotalDischarged(), Rat(0, 1))
}

// A downstream container with spare capacity absorbs only what it can
// hold; the remainder keeps flowing and is discharged at the sink.
func TestEmptyCapacityPartialAbsorption(t *testing.T) {
	e := edges(2)
	e[0] = []Edge{{To: 1, Fraction: Rat(1, 1)}}
	n, err := NewNetwork(2,
		rats(Rat(1, 1), Rat(1, 2)),
		rats(Rat(2, 1), Rat(0, 1)),
		e)
	if err != nil {
		t.Fatal(err)
	}
	n.Tick()
	assertRat(t, "level[0]", n.Levels[0], Rat(1, 1))
	assertRat(t, "level[1]", n.Levels[1], Rat(1, 2)) // absorbed 1, kept 1/2
	assertRat(t, "discharged[1]", n.Discharged[1], Rat(1, 2))
	assertRat(t, "total", n.Total(), Rat(2, 1))
}

// Overflow splits by rational fractions; the unallocated remainder is
// recorded as discharge.
func TestRationalSplitWithDischarge(t *testing.T) {
	e := edges(3)
	e[0] = []Edge{{To: 1, Fraction: Rat(1, 3)}, {To: 2, Fraction: Rat(1, 4)}}
	n, err := NewNetwork(3,
		rats(Rat(0, 1), Rat(100, 1), Rat(100, 1)),
		rats(Rat(1, 1), Rat(0, 1), Rat(0, 1)),
		e)
	if err != nil {
		t.Fatal(err)
	}
	n.Tick()
	assertRat(t, "level[0]", n.Levels[0], Rat(0, 1))
	assertRat(t, "level[1]", n.Levels[1], Rat(1, 3))
	assertRat(t, "level[2]", n.Levels[2], Rat(1, 4))
	assertRat(t, "discharged[0]", n.Discharged[0], Rat(5, 12)) // 1 - 1/3 - 1/4
	assertRat(t, "total", n.Total(), Rat(1, 1))
}

// Overflow must cascade along the chain A -> B -> C until every
// container is within capacity.
func TestChainPropagatesToStability(t *testing.T) {
	e := edges(3)
	e[0] = []Edge{{To: 1, Fraction: Rat(1, 1)}}
	e[1] = []Edge{{To: 2, Fraction: Rat(1, 1)}}
	n, err := NewNetwork(3,
		rats(Rat(1, 1), Rat(1, 1), Rat(1, 1)),
		rats(Rat(7, 2), Rat(0, 1), Rat(0, 1)),
		e)
	if err != nil {
		t.Fatal(err)
	}
	n.Tick()
	assertRat(t, "level[0]", n.Levels[0], Rat(1, 1))
	assertRat(t, "level[1]", n.Levels[1], Rat(1, 1))
	assertRat(t, "level[2]", n.Levels[2], Rat(1, 1))
	assertRat(t, "discharged[2]", n.Discharged[2], Rat(1, 2))
	assertRat(t, "total", n.Total(), Rat(7, 2))
}

// Diamond DAG: one source feeds two mids, both feed one sink. Verifies
// multi-input merging at the sink and exact conservation over many
// ticks with fractional inflows.
func TestDiamondConservationOverTicks(t *testing.T) {
	e := edges(4)
	e[0] = []Edge{{To: 1, Fraction: Rat(2, 3)}, {To: 2, Fraction: Rat(1, 6)}}
	e[1] = []Edge{{To: 3, Fraction: Rat(1, 1)}}
	e[2] = []Edge{{To: 3, Fraction: Rat(1, 1)}}
	inflows := rats(Rat(5, 4), Rat(1, 3), Rat(1, 5), Rat(0, 1))
	n, err := NewNetwork(4,
		rats(Rat(1, 1), Rat(1, 2), Rat(1, 2), Rat(2, 1)),
		inflows,
		e)
	if err != nil {
		t.Fatal(err)
	}
	inflowSum := new(big.Rat)
	for _, f := range inflows {
		inflowSum.Add(inflowSum, f)
	}
	want := new(big.Rat)
	for tick := 1; tick <= 20; tick++ {
		n.Tick()
		want.Add(want, inflowSum)
		assertRat(t, "total after tick", n.Total(), want)
		for i, lvl := range n.Levels {
			if lvl.Cmp(n.Capacities[i]) > 0 {
				t.Fatalf("tick %d: container %d over capacity: %s > %s", tick, i, lvl, n.Capacities[i])
			}
		}
	}
	// Source keeps 1. Excess is 1/4 on the first tick (starts empty) and
	// 5/4 afterwards; 1/6 of each excess is unallocated and discharged:
	// 1/24 + 19*(5/24) = 4.
	assertRat(t, "level[0]", n.Levels[0], Rat(1, 1))
	assertRat(t, "discharged[0]", n.Discharged[0], Rat(4, 1))
}

// Invalid models must be rejected: cycles and fractions summing above 1.
func TestValidation(t *testing.T) {
	cyc := edges(2)
	cyc[0] = []Edge{{To: 1, Fraction: Rat(1, 1)}}
	cyc[1] = []Edge{{To: 0, Fraction: Rat(1, 1)}}
	if _, err := NewNetwork(2, rats(Rat(1, 1), Rat(1, 1)), rats(Rat(0, 1), Rat(0, 1)), cyc); err == nil {
		t.Fatal("expected cycle to be rejected")
	}

	over := edges(2)
	over[0] = []Edge{{To: 1, Fraction: Rat(3, 4)}, {To: 1, Fraction: Rat(1, 2)}}
	if _, err := NewNetwork(2, rats(Rat(1, 1), Rat(1, 1)), rats(Rat(0, 1), Rat(0, 1)), over); err == nil {
		t.Fatal("expected fraction sum > 1 to be rejected")
	}
}
