package impact

import "fmt"

func Example() {
	manager, _ := NewManager([]string{"fips", "race"})
	valid := manager.True()
	root := Provenance{Module: "example.com/app", Version: "v1.2.3", Package: "app", Object: "main.o", Symbol: "main", BuildID: "build-1"}
	dep := Provenance{Module: "example.com/lib", Version: "v1.0.0", Package: "lib", Object: "lib.o", Symbol: "Run", BuildID: "build-2"}

	ring := NewKeyRing()
	_ = ring.Add("release", []byte("shared release key"))
	sign := func(s Summary) SignedSummary {
		cert, _ := ring.Sign("release", manager, s)
		return SignedSummary{Summary: s, Certificate: cert}
	}

	snapshot, _ := BuildSnapshot(manager, valid, []SummaryKey{root.Key()}, []SignedSummary{
		sign(Summary{Provenance: root, Body: []Event{{
			Kind: EventCall, Site: "main-run", Target: SymbolRef{Name: "Run"},
			Guard: manager.Var("fips"),
		}}}),
		sign(Summary{Provenance: dep}),
	}, map[string][]Binding{
		"Run": {{Target: dep.Key(), Provenance: dep, Object: dep.ObjectID()}},
	})
	snapshot.SetKeyRing(ring)

	engine, _ := NewEngineWithRevocations(snapshot, dep.ObjectID())
	analysis, _ := engine.Analyze()
	result, _ := analysis.Result(root.Key())
	fmt.Println(result.Affected.SatCount(), result.Unknown.SatCount(), result.MustRevoke().SatCount())
	// Output: 2 0 2
}
