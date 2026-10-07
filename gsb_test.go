package gsb

import (
	"strings"
	"testing"
)

// Two demands share a limited auxiliary. Each demand alone could reach 3
// orders (2 aux-capped cheap units + 4 expensive units = 14 <= 15 stock),
// but the shared aux stock of 2 forces coordination: together they can only
// reach 2 orders, and the cost optimum spends the 2 aux units on the
// cheapest substitutions.
func TestAuxiliaryCoupling(t *testing.T) {
	p := &Problem{
		Materials: []Material{
			{Name: "M1", Stock: 15, UnitCost: 1},
			{Name: "M2", Stock: 15, UnitCost: 1},
			{Name: "AUX", Stock: 2, UnitCost: 0},
		},
		Demands: []Demand{
			{Name: "D1", Quantity: 2, Options: []Option{
				{Material: 0, Ratio: 1, Aux: []AuxUse{{Material: 2, Amount: 1}}},
				{Material: 0, Ratio: 3},
			}},
			{Name: "D2", Quantity: 2, Options: []Option{
				{Material: 1, Ratio: 1, Aux: []AuxUse{{Material: 2, Amount: 1}}},
				{Material: 1, Ratio: 3},
			}},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.CompleteOrders != 2 {
		t.Errorf("CompleteOrders = %d, want 2", s.CompleteOrders)
	}
	if s.Cost != 20 {
		t.Errorf("Cost = %d, want 20", s.Cost)
	}
	if got := s.Usage[2].Used; got != 2 {
		t.Errorf("aux used = %d, want 2 (fully consumed shared auxiliary)", got)
	}
	checkEvidence(t, p, s)

	b, err := BruteForce(p)
	if err != nil {
		t.Fatal(err)
	}
	if b.CompleteOrders != s.CompleteOrders || b.Cost != s.Cost {
		t.Errorf("BruteForce = (%d, %d), Solve = (%d, %d)",
			b.CompleteOrders, b.Cost, s.CompleteOrders, s.Cost)
	}
}

func TestSingleDemand(t *testing.T) {
	p := &Problem{
		Materials: []Material{{Name: "M1", Stock: 10, UnitCost: 3}},
		Demands: []Demand{
			{Name: "D1", Quantity: 2, Options: []Option{{Material: 0, Ratio: 2}}},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.CompleteOrders != 2 {
		t.Errorf("CompleteOrders = %d, want 2", s.CompleteOrders)
	}
	if s.Cost != 24 {
		t.Errorf("Cost = %d, want 24", s.Cost)
	}
	checkEvidence(t, p, s)
}

func TestZeroOrdersNoOptions(t *testing.T) {
	p := &Problem{
		Materials: []Material{{Name: "M1", Stock: 5, UnitCost: 1}},
		Demands:   []Demand{{Name: "D1", Quantity: 1}},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.CompleteOrders != 0 || s.Cost != 0 || len(s.Lines) != 0 {
		t.Errorf("got %+v, want empty zero-cost solution", s)
	}
}

func TestZeroStock(t *testing.T) {
	p := &Problem{
		Materials: []Material{{Name: "M1", Stock: 0, UnitCost: 1}},
		Demands: []Demand{
			{Name: "D1", Quantity: 1, Options: []Option{{Material: 0, Ratio: 1}}},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.CompleteOrders != 0 {
		t.Errorf("CompleteOrders = %d, want 0", s.CompleteOrders)
	}
}

func TestTenDemands(t *testing.T) {
	p := &Problem{
		Materials: []Material{
			{Name: "M1", Stock: 4, UnitCost: 1},
			{Name: "M2", Stock: 3, UnitCost: 2},
			{Name: "AUX", Stock: 2, UnitCost: 1},
		},
	}
	for i := 0; i < MaxDemands; i++ {
		p.Demands = append(p.Demands, Demand{
			Quantity: 1,
			Options: []Option{
				{Material: 0, Ratio: 1, Aux: []AuxUse{{Material: 2, Amount: 1}}},
				{Material: 1, Ratio: 1},
			},
		})
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	checkEvidence(t, p, s)
	b, err := BruteForce(p)
	if err != nil {
		t.Fatal(err)
	}
	if b.CompleteOrders != s.CompleteOrders || b.Cost != s.Cost {
		t.Errorf("BruteForce = (%d, %d), Solve = (%d, %d)",
			b.CompleteOrders, b.Cost, s.CompleteOrders, s.Cost)
	}
}

func TestValidate(t *testing.T) {
	good := func() *Problem {
		return &Problem{
			Materials: []Material{{Stock: 1, UnitCost: 1}},
			Demands: []Demand{
				{Quantity: 1, Options: []Option{{Material: 0, Ratio: 1}}},
			},
		}
	}
	cases := map[string]*Problem{
		"no demands": {Materials: []Material{{Stock: 1}}},
		"too many demands": func() *Problem {
			p := good()
			for i := 0; i < MaxDemands; i++ {
				p.Demands = append(p.Demands, p.Demands[0])
			}
			return p
		}(),
		"negative stock": func() *Problem {
			p := good()
			p.Materials[0].Stock = -1
			return p
		}(),
		"negative cost": func() *Problem {
			p := good()
			p.Materials[0].UnitCost = -1
			return p
		}(),
		"zero quantity": func() *Problem {
			p := good()
			p.Demands[0].Quantity = 0
			return p
		}(),
		"bad material index": func() *Problem {
			p := good()
			p.Demands[0].Options[0].Material = 7
			return p
		}(),
		"zero ratio": func() *Problem {
			p := good()
			p.Demands[0].Options[0].Ratio = 0
			return p
		}(),
		"bad aux index": func() *Problem {
			p := good()
			p.Demands[0].Options[0].Aux = []AuxUse{{Material: 3, Amount: 1}}
			return p
		}(),
		"zero aux amount": func() *Problem {
			p := good()
			p.Demands[0].Options[0].Aux = []AuxUse{{Material: 0, Amount: 0}}
			return p
		}(),
	}
	for name, p := range cases {
		if _, err := Solve(p); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
		if _, err := BruteForce(p); err == nil {
			t.Errorf("%s: BruteForce expected validation error", name)
		}
	}
	if _, err := Solve(good()); err != nil {
		t.Errorf("valid problem rejected: %v", err)
	}
}

func TestReport(t *testing.T) {
	p := &Problem{
		Materials: []Material{{Name: "M1", Stock: 4, UnitCost: 1}},
		Demands: []Demand{
			{Name: "D1", Quantity: 2, Options: []Option{{Material: 0, Ratio: 1}}},
		},
	}
	s, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	rep := s.Report(p)
	for _, want := range []string{
		"complete orders: 2",
		"total cost: 4",
		`demand "D1": 4 unit(s) via option 0 [M1 x4]`,
		"M1: 4/4",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report missing %q:\n%s", want, rep)
		}
	}
}

// checkEvidence verifies that a solution's consumption evidence is
// internally consistent and respects inventory.
func checkEvidence(t *testing.T, p *Problem, s *Solution) {
	t.Helper()
	e := newEngine(p)
	used := make([]int64, len(p.Materials))
	filled := make([]int64, len(p.Demands))
	var cost int64
	for _, ln := range s.Lines {
		filled[ln.Demand] += ln.Units
		cost += ln.Units * e.ocost[ln.Demand][ln.Option]
		for m, c := range e.cons[ln.Demand][ln.Option] {
			used[m] += ln.Units * c
		}
	}
	for d := range p.Demands {
		if want := s.CompleteOrders * p.Demands[d].Quantity; filled[d] != want {
			t.Errorf("demand %d fulfilled %d units, want %d", d, filled[d], want)
		}
	}
	if cost != s.Cost {
		t.Errorf("evidence cost %d != solution cost %d", cost, s.Cost)
	}
	for m := range used {
		if s.Usage[m].Used != used[m] {
			t.Errorf("material %d usage %d, evidence says %d", m, s.Usage[m].Used, used[m])
		}
		if s.Usage[m].Stock != p.Materials[m].Stock {
			t.Errorf("material %d stock %d, want %d", m, s.Usage[m].Stock, p.Materials[m].Stock)
		}
		if used[m] > p.Materials[m].Stock {
			t.Errorf("material %d over-consumed: %d > %d", m, used[m], p.Materials[m].Stock)
		}
	}
}
