package viewopt

import "testing"

func tinyDomain() Domain {
	return Domain{
		Keys: []Key{
			K(),
			K(Int(0)),
			K(Int(1)),
		},
		Filters:  []int64{0, 1, 5, 6},
		Measures: []Value{Null(), Int(0), Int(1)},
	}
}

func TestExhaustiveValidPlan(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 10)}
	views := []View{
		fullView("left", 1, Between(0, 5)),
		fullView("right", 1, Between(6, 10)),
	}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}, {View: 1, Coeff: 1}}}
	ok, witness, err := ExhaustiveVerify(q, views, p, tinyDomain(), EnumerateOptions{
		MaxMultiplicity: 1,
		MaxTotalRows:    3,
		Limit:           200000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("valid plan found a witness: %+v", witness)
	}
}

func TestExhaustiveFindsExtraEmptyGroup(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(5, 10)}
	views := []View{
		fullView("wide", 1, Between(0, 10)),
		fullView("prefix", 1, Between(0, 4)),
	}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}, {View: 1, Coeff: -1}}}
	ok, witness, err := ExhaustiveVerify(q, views, p, tinyDomain(), EnumerateOptions{
		MaxMultiplicity: 1,
		MaxTotalRows:    3,
		Limit:           200000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok || witness == nil {
		t.Fatal("expected exhaustive search to find the extra-empty-group counterexample")
	}
	tab := witness.Table
	direct, err := EvaluateQuery(q, tab)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := EvaluatePlan(q, views, p, tab)
	if err != nil {
		t.Fatal(err)
	}
	extra := false
	for k, a := range plan {
		if _, ok := direct[k]; !ok && a.Count == 0 && a.CountNonNull == 0 && a.Sum.Null {
			extra = true
		}
	}
	if !extra {
		t.Fatalf("witness %s is not an extra empty group (direct=%s plan=%s)", tab, direct, plan)
	}
	for k, a := range direct {
		b, ok := plan[k]
		if !ok || !a.Equal(b) {
			t.Fatalf("shared group %s has different aggregates: direct=%s plan=%s", k, a, plan[k])
		}
	}
}

func TestExhaustiveAgreesWhenHavingFiltersEmptyGroup(t *testing.T) {
	q := Query{
		Dims:   []int{0},
		Filter: Between(5, 10),
		Having: func(a Agg) bool { return a.Count > 0 },
	}
	views := []View{
		fullView("wide", 1, Between(0, 10)),
		fullView("prefix", 1, Between(0, 4)),
	}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}, {View: 1, Coeff: -1}}}
	ok, witness, err := ExhaustiveVerify(q, views, p, tinyDomain(), EnumerateOptions{
		MaxMultiplicity: 1,
		MaxTotalRows:    3,
		Limit:           200000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("HAVING filters the empty group, but exhaustive found %+v", witness)
	}
}

func TestExhaustiveStructuralWitness(t *testing.T) {
	q := Query{Dims: []int{0, 1}, Filter: Between(0, 10)}
	views := []View{{
		Name: "coarse", Dims: []int{0}, Filter: Between(0, 10), ReadCost: 1,
	}}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}}}
	domain := Domain{
		Keys:     []Key{K(), K(Int(0), Int(0), Null())},
		Filters:  []int64{0, 5},
		Measures: []Value{Null(), Int(1)},
	}
	ok, witness, err := ExhaustiveVerify(q, views, p, domain, EnumerateOptions{
		MaxMultiplicity: 1,
		MaxTotalRows:    2,
		Limit:           200000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok || witness == nil || len(witness.Table) != 1 {
		t.Fatalf("expected a one-row structural witness, got ok=%v witness=%+v", ok, witness)
	}
}
