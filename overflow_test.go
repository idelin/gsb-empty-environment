package overflow

import (
	"math/big"
	"testing"
)

func rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

// model is an independent, deliberately naive per-container simulator used
// to cross-check the library. It repeatedly sweeps all containers, moving
// overflow to children and discharge, until nothing moves (fixpoint).
func modelTick(levels, caps map[string]*big.Rat, edges []Edge, adds map[string]*big.Rat) *big.Rat {
	for id, amt := range adds {
		levels[id].Add(levels[id], amt)
	}
	discharged := new(big.Rat)
	for {
		moved := false
		for id, lv := range levels {
			overflow := new(big.Rat).Sub(lv, caps[id])
			if overflow.Sign() <= 0 {
				continue
			}
			lv.Set(caps[id])
			assigned := new(big.Rat)
			for _, e := range edges {
				if e.From == id {
					flow := new(big.Rat).Mul(overflow, e.Fraction)
					levels[e.To].Add(levels[e.To], flow)
					assigned.Add(assigned, flow)
				}
			}
			discharged.Add(discharged, new(big.Rat).Sub(overflow, assigned))
			moved = true
		}
		if !moved {
			return discharged
		}
	}
}

func buildNetwork(t *testing.T, caps map[string]*big.Rat, edges [][3]interface{}) *Network {
	t.Helper()
	b := NewBuilder()
	ids := []string{}
	for id := range caps {
		ids = append(ids, id)
	}
	// deterministic insertion not required, but keeps errors reproducible
	for _, id := range ids {
		if err := b.AddContainer(id, caps[id]); err != nil {
			t.Fatalf("AddContainer(%s): %v", id, err)
		}
	}
	for _, e := range edges {
		if err := b.AddEdge(e[0].(string), e[1].(string), e[2].(*big.Rat)); err != nil {
			t.Fatalf("AddEdge(%v): %v", e, err)
		}
	}
	n, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return n
}

func copyLevels(m map[string]*big.Rat) map[string]*big.Rat {
	out := make(map[string]*big.Rat, len(m))
	for k, v := range m {
		out[k] = new(big.Rat).Set(v)
	}
	return out
}

func copyCaps(m map[string]*big.Rat) map[string]*big.Rat { return copyLevels(m) }

// TestConservationAndModel runs a multi-tick scenario on a DAG with
// multi-way inflow and checks, after every tick:
//  1. total conservation: prevTotal + adds == newTotal + discharged
//  2. the library result matches the independent fixpoint model
func TestConservationAndModel(t *testing.T) {
	//        a(10) --1/2--> c(6) --2/3--> e(4)
	//        b(8)  --1/3--> c
	//        c     --1/6--> d(5)
	// d and e are sinks; unassigned fractions discharge.
	caps := map[string]*big.Rat{
		"a": rat(10, 1), "b": rat(8, 1), "c": rat(6, 1),
		"d": rat(5, 1), "e": rat(4, 1),
	}
	edges := [][3]interface{}{
		{"a", "c", rat(1, 2)},
		{"b", "c", rat(1, 3)},
		{"c", "d", rat(1, 6)},
		{"c", "e", rat(2, 3)},
	}
	n := buildNetwork(t, caps, edges)

	edgeList := []Edge{
		{From: "a", To: "c", Fraction: rat(1, 2)},
		{From: "b", To: "c", Fraction: rat(1, 3)},
		{From: "c", To: "d", Fraction: rat(1, 6)},
		{From: "c", To: "e", Fraction: rat(2, 3)},
	}
	modelLevels := make(map[string]*big.Rat)
	for id := range caps {
		modelLevels[id] = new(big.Rat)
	}
	modelCaps := copyCaps(caps)
	modelDischarged := new(big.Rat)

	ticks := []map[string]*big.Rat{
		{"a": rat(25, 2), "b": rat(9, 1)},                 // both spill into c
		{"c": rat(7, 3)},                                  // direct fill of c
		{"a": rat(1, 4), "b": rat(1, 4), "d": rat(11, 2)}, // sink overflow
		{}, // quiescent tick
	}

	prevTotal := n.TotalLevel()
	for i, adds := range ticks {
		addSum := new(big.Rat)
		for _, v := range adds {
			addSum.Add(addSum, v)
		}

		discharged, err := n.Tick(adds)
		if err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		modelDischarged.Add(modelDischarged, modelTick(modelLevels, modelCaps, edgeList, adds))

		// 1. Conservation for this tick.
		lhs := new(big.Rat).Add(prevTotal, addSum)
		rhs := new(big.Rat).Add(n.TotalLevel(), discharged)
		if lhs.Cmp(rhs) != 0 {
			t.Errorf("tick %d: conservation violated: %s in, %s out", i, lhs.RatString(), rhs.RatString())
		}
		prevTotal = n.TotalLevel()

		// 2. Per-container agreement with the fixpoint model.
		for id, lv := range modelLevels {
			if got := n.Container(id).Level; got.Cmp(lv) != 0 {
				t.Errorf("tick %d: container %s level %s, model says %s", i, id, got.RatString(), lv.RatString())
			}
		}
		if n.Discharged.Cmp(modelDischarged) != 0 {
			t.Errorf("tick %d: discharged %s, model says %s", i, n.Discharged.RatString(), modelDischarged.RatString())
		}

		// 3. Stability: every container within capacity.
		for id, c := range map[string]*Container{
			"a": n.Container("a"), "b": n.Container("b"), "c": n.Container("c"),
			"d": n.Container("d"), "e": n.Container("e"),
		} {
			if c.Level.Cmp(c.Capacity) > 0 {
				t.Errorf("tick %d: container %s still over capacity after tick", i, id)
			}
		}
	}
}

// TestMultiwayInflow: two parents overflow into one child in the same tick;
// the child absorbs up to its empty capacity and re-overflows the rest.
func TestMultiwayInflow(t *testing.T) {
	n := buildNetwork(t,
		map[string]*big.Rat{"p1": rat(4, 1), "p2": rat(4, 1), "c": rat(3, 1)},
		[][3]interface{}{{"p1", "c", rat(1, 1)}, {"p2", "c", rat(1, 1)}},
	)
	// p1 overflows by 2, p2 overflows by 3 -> c receives 5, keeps 3, discharges 2.
	d, err := n.Tick(map[string]*big.Rat{"p1": rat(6, 1), "p2": rat(7, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if got := n.Container("c").Level; got.Cmp(rat(3, 1)) != 0 {
		t.Errorf("c level = %s, want 3", got.RatString())
	}
	if d.Cmp(rat(2, 1)) != 0 {
		t.Errorf("discharged = %s, want 2", d.RatString())
	}
}

// TestEmptyCapacity: overflow lands in a child with spare (empty) capacity
// and is fully absorbed, so nothing is discharged.
func TestEmptyCapacity(t *testing.T) {
	n := buildNetwork(t,
		map[string]*big.Rat{"src": rat(2, 1), "dst": rat(10, 1)},
		[][3]interface{}{{"src", "dst", rat(3, 4)}},
	)
	// src overflows by 3; 3/4 * 3 = 9/4 flows to dst (fits in 10), 3/4 discharges.
	d, err := n.Tick(map[string]*big.Rat{"src": rat(5, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if got := n.Container("dst").Level; got.Cmp(rat(9, 4)) != 0 {
		t.Errorf("dst level = %s, want 9/4", got.RatString())
	}
	if d.Cmp(rat(3, 4)) != 0 {
		t.Errorf("discharged = %s, want 3/4", d.RatString())
	}

	// Second tick: dst still has empty capacity 10 - 9/4 = 31/4, absorbing
	// another 9/4 from the src overflow of 3; 3/4 discharges.
	d, err = n.Tick(map[string]*big.Rat{"src": rat(3, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if got := n.Container("dst").Level; got.Cmp(rat(9, 2)) != 0 {
		t.Errorf("dst level = %s, want 9/2", got.RatString())
	}
	if d.Cmp(rat(3, 4)) != 0 { // src overflow 3, 9/4 flows down, 3/4 discharges
		t.Errorf("discharged = %s, want 3/4", d.RatString())
	}
}

// TestCascadeStability: a long chain must settle completely in one tick.
func TestCascadeStability(t *testing.T) {
	n := buildNetwork(t,
		map[string]*big.Rat{"n1": rat(1, 1), "n2": rat(1, 1), "n3": rat(1, 1), "n4": rat(1, 1)},
		[][3]interface{}{
			{"n1", "n2", rat(1, 1)},
			{"n2", "n3", rat(1, 1)},
			{"n3", "n4", rat(1, 1)},
		},
	)
	// 10 into n1: each node keeps 1, 6 discharges past n4.
	d, err := n.Tick(map[string]*big.Rat{"n1": rat(10, 1)})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"n1", "n2", "n3", "n4"} {
		if got := n.Container(id).Level; got.Cmp(rat(1, 1)) != 0 {
			t.Errorf("%s level = %s, want 1", id, got.RatString())
		}
	}
	if d.Cmp(rat(6, 1)) != 0 {
		t.Errorf("discharged = %s, want 6", d.RatString())
	}
}

func TestValidation(t *testing.T) {
	// Cycle detection.
	b := NewBuilder()
	b.AddContainer("x", rat(1, 1))
	b.AddContainer("y", rat(1, 1))
	b.AddEdge("x", "y", rat(1, 1))
	b.AddEdge("y", "x", rat(1, 1))
	if _, err := b.Build(); err == nil {
		t.Error("expected cycle error")
	}

	// Fraction sum > 1.
	b = NewBuilder()
	b.AddContainer("x", rat(1, 1))
	b.AddContainer("y", rat(1, 1))
	b.AddContainer("z", rat(1, 1))
	b.AddEdge("x", "y", rat(2, 3))
	b.AddEdge("x", "z", rat(2, 3))
	if _, err := b.Build(); err == nil {
		t.Error("expected fraction-sum error")
	}

	// Negative inflow.
	n := buildNetwork(t, map[string]*big.Rat{"a": rat(1, 1)}, nil)
	if _, err := n.Tick(map[string]*big.Rat{"a": rat(-1, 1)}); err == nil {
		t.Error("expected negative-inflow error")
	}
	if _, err := n.Tick(map[string]*big.Rat{"ghost": rat(1, 1)}); err == nil {
		t.Error("expected unknown-container error")
	}
}
