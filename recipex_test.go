package recipex

import (
	"math/rand"
	"testing"
)

func evidenceOf(t *testing.T, r Result, material string) Consumption {
	t.Helper()
	for _, c := range r.Evidence {
		if c.Material == material {
			return c
		}
	}
	t.Fatalf("no evidence for material %q", material)
	return Consumption{}
}

func TestSolveBasicEvidence(t *testing.T) {
	demands := []Demand{
		{ID: "A", Quantity: 2, Options: []Option{
			{Material: "steel", Ratio: 3, Aux: "flux", AuxRatio: 1, Cost: 5},
			{Material: "alum", Ratio: 4, Cost: 7},
		}},
		{ID: "B", Quantity: 1, Options: []Option{
			{Material: "steel", Ratio: 2, Cost: 4},
		}},
	}
	inv := map[string]int64{"steel": 20, "alum": 20, "flux": 20}
	r, err := Solve(demands, inv)
	if err != nil {
		t.Fatal(err)
	}
	if r.Orders != 2 {
		t.Fatalf("orders = %d, want 2", r.Orders)
	}
	// Cheapest: A via steel (2*3 steel, 2*3*1 flux, cost 10), B via steel
	// (2 steel, cost 4). Total steel 8, flux 6, cost 14.
	if r.Cost != 14 {
		t.Fatalf("cost = %d, want 14", r.Cost)
	}
	if got := evidenceOf(t, r, "steel"); got.Used != 8 || got.Remaining != 12 {
		t.Fatalf("steel evidence = %+v, want used 8 remaining 12", got)
	}
	if got := evidenceOf(t, r, "flux"); got.Used != 6 || got.Remaining != 14 {
		t.Fatalf("flux evidence = %+v, want used 6 remaining 14", got)
	}
	if got := evidenceOf(t, r, "alum"); got.Used != 0 || got.Remaining != 20 {
		t.Fatalf("alum evidence = %+v, want used 0 remaining 20", got)
	}
	var assignCost int64
	for _, a := range r.Assignments {
		assignCost += a.Cost
	}
	if assignCost != r.Cost {
		t.Fatalf("assignment cost sum %d != result cost %d", assignCost, r.Cost)
	}
}

// TestAuxCoupling is the core auxiliary-material coupling check: both demands
// have plenty of main material, but they share one auxiliary material whose
// stock only covers a single order, so at most one demand can be completed.
func TestAuxCoupling(t *testing.T) {
	demands := []Demand{
		{ID: "A", Quantity: 2, Options: []Option{
			{Material: "m1", Ratio: 1, Aux: "glue", AuxRatio: 3, Cost: 1},
			{Material: "m2", Ratio: 1, Cost: 9}, // aux-free but expensive
		}},
		{ID: "B", Quantity: 3, Options: []Option{
			{Material: "m3", Ratio: 2, Aux: "glue", AuxRatio: 1, Cost: 1},
		}},
	}
	// glue needed: A via m1 -> 2*1*3 = 6; B -> 3*2*1 = 6. Stock 6: only one
	// of the two aux-consuming routes fits.
	inv := map[string]int64{"m1": 100, "m2": 100, "m3": 100, "glue": 6}
	r, err := Solve(demands, inv)
	if err != nil {
		t.Fatal(err)
	}
	if r.Orders != 2 {
		t.Fatalf("orders = %d, want 2 (A can switch to aux-free m2)", r.Orders)
	}
	// Optimal: A via m2 (cost 18) + B via m3 (cost 3) = 21, OR A via m1
	// (cost 2) alone with glue... no: both orders wanted. A via m1 (glue 6)
	// blocks B. So A via m2 + B via m3, cost 2*9 + 3*1 = 21.
	if r.Cost != 21 {
		t.Fatalf("cost = %d, want 21", r.Cost)
	}
	if got := evidenceOf(t, r, "glue"); got.Used != 6 || got.Remaining != 0 {
		t.Fatalf("glue evidence = %+v, want used 6 remaining 0", got)
	}

	// Remove A's aux-free option: both demands now need 6 glue and the
	// shared stock of 6 couples them, so exactly one order fits.
	demands[0].Options = demands[0].Options[:1]
	r, err = Solve(demands, inv)
	if err != nil {
		t.Fatal(err)
	}
	if r.Orders != 1 {
		t.Fatalf("orders = %d, want 1 (glue couples A and B)", r.Orders)
	}
}

// TestCrossCheckBruteForce compares Solve against full enumeration on many
// small random instances with shared main and auxiliary materials.
func TestCrossCheckBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	mats := []string{"m0", "m1", "m2"}
	auxes := []string{"", "g0", "g1"}
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(5)
		demands := make([]Demand, n)
		for i := range demands {
			nOpts := 1 + rng.Intn(3)
			opts := make([]Option, nOpts)
			for j := range opts {
				aux := auxes[rng.Intn(len(auxes))]
				o := Option{
					Material: mats[rng.Intn(len(mats))],
					Ratio:    1 + rng.Intn(3),
					Aux:      aux,
					Cost:     int64(rng.Intn(10)),
				}
				if aux != "" {
					o.AuxRatio = 1 + rng.Intn(2)
				}
				opts[j] = o
			}
			demands[i] = Demand{
				ID:       string(rune('A' + i)),
				Quantity: 1 + rng.Intn(3),
				Options:  opts,
			}
		}
		inv := map[string]int64{}
		for _, m := range mats {
			inv[m] = int64(rng.Intn(15))
		}
		for _, a := range auxes[1:] {
			inv[a] = int64(rng.Intn(12))
		}

		fast, err := Solve(demands, inv)
		if err != nil {
			t.Fatalf("trial %d: Solve error: %v", trial, err)
		}
		slow, err := BruteForceSolve(demands, inv)
		if err != nil {
			t.Fatalf("trial %d: BruteForceSolve error: %v", trial, err)
		}
		if fast.Orders != slow.Orders || fast.Cost != slow.Cost {
			t.Fatalf("trial %d: Solve=(%d,%d) brute=(%d,%d)\ndemands=%+v\ninv=%v",
				trial, fast.Orders, fast.Cost, slow.Orders, slow.Cost, demands, inv)
		}
		// Evidence must be internally consistent and within inventory.
		for _, c := range fast.Evidence {
			if c.Remaining < 0 {
				t.Fatalf("trial %d: negative remaining for %q", trial, c.Material)
			}
			if c.Used+c.Remaining != inv[c.Material] {
				t.Fatalf("trial %d: evidence for %q does not reconcile with inventory", trial, c.Material)
			}
		}
	}
}

func TestTenDemandsLimit(t *testing.T) {
	demands := make([]Demand, MaxDemands)
	for i := range demands {
		demands[i] = Demand{
			ID:       string(rune('A' + i)),
			Quantity: 1,
			Options:  []Option{{Material: "m", Ratio: 1, Cost: 1}},
		}
	}
	if _, err := Solve(demands, map[string]int64{"m": 10}); err != nil {
		t.Fatalf("10 demands should be accepted: %v", err)
	}
	demands = append(demands, Demand{ID: "K", Quantity: 1, Options: []Option{{Material: "m", Ratio: 1}}})
	if _, err := Solve(demands, map[string]int64{"m": 11}); err == nil {
		t.Fatal("11 demands should be rejected")
	}
}

func TestValidation(t *testing.T) {
	good := []Demand{{ID: "A", Quantity: 1, Options: []Option{{Material: "m", Ratio: 1}}}}
	cases := []struct {
		name    string
		demands []Demand
		inv     map[string]int64
	}{
		{"no demands", nil, nil},
		{"empty id", []Demand{{Quantity: 1, Options: []Option{{Material: "m", Ratio: 1}}}}, nil},
		{"bad qty", []Demand{{ID: "A", Quantity: 0, Options: []Option{{Material: "m", Ratio: 1}}}}, nil},
		{"no options", []Demand{{ID: "A", Quantity: 1}}, nil},
		{"zero ratio", []Demand{{ID: "A", Quantity: 1, Options: []Option{{Material: "m"}}}}, nil},
		{"aux without material", []Demand{{ID: "A", Quantity: 1, Options: []Option{{Material: "m", Ratio: 1, AuxRatio: 2}}}}, nil},
		{"negative inventory", good, map[string]int64{"m": -1}},
	}
	for _, c := range cases {
		if _, err := Solve(c.demands, c.inv); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}
