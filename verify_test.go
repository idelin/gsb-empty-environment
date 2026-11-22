package impact

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestCallbackOriginMayBeDerivedFromTarget(t *testing.T) {
	m, _ := NewManager([]string{"x"})
	x := m.Var("x")
	a := prov("m", "p", "a.o", "A")
	cb := prov("m", "p", "cb.o", "Cb")
	d := prov("m", "p", "d.o", "D")
	snap := mustSnapshot(t, m, m.True(), []SummaryKey{a.Key()}, []SignedSummary{
		sign(t, m, Summary{Provenance: a, Body: []Event{
			{Kind: EventRegister, Site: "reg", Target: SymbolRef{Name: "cb"}, Handle: "h", Guard: x},
		}}),
		sign(t, m, Summary{Provenance: cb, Body: []Event{call("cb1", "d", m.True())}}),
		sign(t, m, Summary{Provenance: d}),
	}, map[string][]Binding{
		"cb": {bindTo(cb)},
		"d":  {bindTo(d)},
	})
	res := mustAnalyze(t, snap, d.ObjectID())
	r, _ := res.Result(a.Key())
	assertBDD(t, r.Affected, "x")
	assertBDD(t, r.Unknown, "0")
}

func TestRandomSmallProgramsMatchEnumeration(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed-%02d", seed), func(t *testing.T) {
			runRandomProgram(t, seed)
		})
	}
}

func runRandomProgram(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	m, err := NewManager([]string{"a", "b", "c", "d"})
	if err != nil {
		t.Fatal(err)
	}
	vars := []BDD{m.Var("a"), m.Var("b"), m.Var("c"), m.Var("d")}
	randomGuard := func() BDD {
		switch rng.Intn(6) {
		case 0:
			return m.True()
		case 1:
			return m.False()
		case 2:
			return vars[rng.Intn(len(vars))].Not()
		case 3:
			return vars[rng.Intn(len(vars))].And(vars[rng.Intn(len(vars))])
		case 4:
			return vars[rng.Intn(len(vars))].Or(vars[rng.Intn(len(vars))])
		default:
			return vars[rng.Intn(len(vars))]
		}
	}

	const n = 4
	provenances := make([]Provenance, n)
	for i := range provenances {
		provenances[i] = prov("m", "p", fmt.Sprintf("o%d.o", i), fmt.Sprintf("S%d", i))
	}
	summaries := make([]SignedSummary, n)
	for i := 0; i < n; i++ {
		var body []Event
		eventCount := rng.Intn(5)
		for j := 0; j < eventCount; j++ {
			switch rng.Intn(4) {
			case 0:
				body = append(body, call(SiteID(fmt.Sprintf("s%d-c%d", i, j)),
					fmt.Sprintf("sym%d", rng.Intn(n)), randomGuard()))
			case 1:
				target := provenances[rng.Intn(n)]
				body = append(body, Event{
					Kind:   EventRegister,
					Site:   SiteID(fmt.Sprintf("s%d-r%d", i, j)),
					Target: SymbolRef{Name: fmt.Sprintf("sym%d", rng.Intn(n))},
					Handle: HandleID(fmt.Sprintf("h%d-%d", i, j)),
					Origin: &target,
					Guard:  randomGuard(),
				})
			case 2:
				body = append(body, Event{
					Kind:   EventUnregister,
					Site:   SiteID(fmt.Sprintf("s%d-u%d", i, j)),
					Handle: HandleID(fmt.Sprintf("h%d-%d", i, rng.Intn(j+1))),
					Guard:  randomGuard(),
				})
			default:
				body = append(body, Event{Kind: EventReturn, Site: SiteID(fmt.Sprintf("s%d-ret%d", i, j)), Guard: randomGuard()})
			}
		}
		summary := Summary{Provenance: provenances[i], Body: body}
		if rng.Intn(4) == 0 {
			summary.OptionalSymbols = []OptionalSymbol{{
				Name:  fmt.Sprintf("sym%d", rng.Intn(n)),
				Guard: randomGuard(),
			}}
		}
		summaries[i] = sign(t, m, summary)
	}
	bindings := make(map[string][]Binding)
	for i := 0; i < n; i++ {
		symbol := fmt.Sprintf("sym%d", i)
		primary := provenances[rng.Intn(n)]
		binding := Binding{Target: primary.Key(), Provenance: primary, Object: primary.ObjectID(), Guard: randomGuard()}
		bindings[symbol] = append(bindings[symbol], binding)
		if rng.Intn(3) == 0 {
			weak := provenances[rng.Intn(n)]
			bindings[symbol] = append(bindings[symbol], Binding{
				Target: weak.Key(), Provenance: weak, Object: weak.ObjectID(),
				Guard: randomGuard(), Weak: true,
			})
		}
	}
	// Sometimes omit a summary to exercise missing-evidence propagation.
	omit := -1
	if rng.Intn(3) == 0 {
		omit = 1 + rng.Intn(n-1)
	}
	var present []SignedSummary
	for i := range summaries {
		if i != omit {
			present = append(present, summaries[i])
		}
	}
	valid := randomGuard()
	snap := mustSnapshot(t, m, valid, []SummaryKey{provenances[0].Key()}, present, bindings)
	revoked := make([]ObjectID, 0, 2)
	for i := 0; i < n; i++ {
		if rng.Intn(3) == 0 {
			revoked = append(revoked, provenances[i].ObjectID())
		}
	}
	analysis, err := Analyze(snap, revoked)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if err := NewVerifier().VerifyAnalysis(snap, revoked, analysis); err != nil {
		t.Fatalf("verify: %v", err)
	}
}
