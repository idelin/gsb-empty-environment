package viewopt

import (
	"math/rand"
	"testing"
)

func TestRandomSymbolicVersusExhaustive(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	domain := Domain{
		Keys:     []Key{K(), K(Int(0)), K(Int(1)), K(Int(0), Int(1), Null())},
		Filters:  []int64{0, 1, 2, 3, 4, 5, 6},
		Measures: []Value{Null(), Int(0), Int(1), Int(-1)},
	}
	opt := Options{MaxAbsCoeff: 1, Domain: Between(0, 6), MaxMultiplicity: 1, MaxTotalRows: 3}
	for iter := 0; iter < 150; iter++ {
		nv := 1 + rng.Intn(4)
		views := make([]View, nv)
		for i := range views {
			lo := int64(rng.Intn(7))
			hi := lo + int64(rng.Intn(7-int(lo)))
			dims := []int{rng.Intn(2)}
			if rng.Intn(3) == 0 {
				dims = []int{0, 1, 2}
			}
			views[i] = View{
				Name:     "v",
				Dims:     dims,
				Filter:   Between(lo, hi),
				ReadCost: int64(rng.Intn(5)),
			}
		}
		qlo := int64(rng.Intn(7))
		qhi := qlo + int64(rng.Intn(7-int(qlo)))
		q := Query{Dims: []int{rng.Intn(2)}, Filter: Between(qlo, qhi)}
		switch rng.Intn(3) {
		case 1:
			q.Having = func(a Agg) bool { return a.Count > 0 }
		case 2:
			q.Having = func(a Agg) bool { return a.CountNonNull > 0 }
		}

		np := 1 + rng.Intn(3)
		plan := Plan{}
		for i := 0; i < np; i++ {
			plan.Terms = append(plan.Terms, PlanTerm{View: rng.Intn(nv), Coeff: rng.Intn(3) - 1})
		}

		ver, err := VerifyPlan(q, views, plan, opt)
		if err != nil {
			t.Fatalf("VerifyPlan: %v", err)
		}
		ok, witness, err := ExhaustiveVerify(q, views, plan, domain, EnumerateOptions{
			MaxMultiplicity: 1, MaxTotalRows: 3, Limit: 400000,
		})
		if err != nil {
			t.Fatalf("ExhaustiveVerify: %v", err)
		}
		if ver.Equivalent && !ok {
			t.Fatalf("iter %d: symbolic said equivalent but exhaustive found %s (q=%+v views=%+v plan=%+v)",
				iter, witness.Table, q, views, plan)
		}
		if !ver.Equivalent && ok {
			// The symbolic failure may only be visible outside this
			// finite domain (for example an unbounded filter cell), so
			// only require that a found witness is a real failure.
			continue
		}
		if !ver.Equivalent && witness == nil && ver.Counterexample == nil {
			t.Fatalf("iter %d: no symbolic counterexample (%s)", iter, ver.Reason)
		}
		if !ver.Equivalent && ver.Counterexample != nil {
			q2 := q
			if ver.Counterexample.PreHaving {
				q2.Having = nil
			}
			d, err := EvaluateQuery(q2, ver.Counterexample.Table)
			if err != nil {
				t.Fatal(err)
			}
			p, err := EvaluatePlan(q2, views, plan, ver.Counterexample.Table)
			if err != nil {
				t.Fatal(err)
			}
			if d.Equal(p) {
				t.Fatalf("iter %d: returned counterexample %s does not differ (q=%+v views=%+v plan=%+v reason=%s)",
					iter, ver.Counterexample.Table, q, views, plan, ver.Reason)
			}
		}
	}
}

func TestRandomOptimizerPlansMatchDirect(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	opt := DefaultOptions()
	domain := Domain{
		Keys:     []Key{K(), K(Int(0)), K(Int(1))},
		Filters:  []int64{0, 1, 2, 3, 4, 5, 6},
		Measures: []Value{Null(), Int(0), Int(1), Int(-2)},
	}
	for iter := 0; iter < 60; iter++ {
		nv := 1 + rng.Intn(5)
		views := make([]View, nv)
		for i := range views {
			lo := int64(rng.Intn(7))
			hi := lo + int64(rng.Intn(7-int(lo)))
			views[i] = View{
				Name:     "v",
				Dims:     []int{0},
				Filter:   Between(lo, hi),
				ReadCost: int64(1 + rng.Intn(5)),
			}
		}
		qlo := int64(rng.Intn(7))
		qhi := qlo + int64(rng.Intn(7-int(qlo)))
		q := Query{Dims: []int{0}, Filter: Between(qlo, qhi)}
		sol, err := Optimize(Problem{Views: views, Query: q, Options: opt})
		if err != nil {
			t.Fatalf("Optimize: %v", err)
		}
		if !sol.Equivalent {
			continue
		}
		ok, witness, err := ExhaustiveVerify(q, views, sol.Plan, domain, EnumerateOptions{
			MaxMultiplicity: 2, MaxTotalRows: 3, Limit: 400000,
		})
		if err != nil {
			t.Fatalf("ExhaustiveVerify: %v", err)
		}
		if !ok {
			t.Fatalf("iter %d: optimizer plan %+v fails on %s (q=%+v views=%+v)",
				iter, sol.Plan, witness.Table, q, views)
		}
	}
}
