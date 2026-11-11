package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/uid1290"
)

func ExampleAnalyze() {
	g, err := netsec.NewGraph(
		[]netsec.Node{
			{
				ID: 1,
				Filters: []netsec.FilterRule{
					{Name: "dmz-only", Field: netsec.Src, Match: interval(10, 19), Allow: true},
					{Name: "deny-all", Field: netsec.Src, Match: interval(0, ^uint32(0)), Allow: false},
				},
			},
			{
				ID: 2,
				Translates: []netsec.TranslateRule{
					{Name: "snat", Target: netsec.Src, Match: interval(10, 19), Delta: 90},
				},
			},
			{
				ID: 3,
				Filters: []netsec.FilterRule{
					{Name: "mgmt", Field: netsec.Dst, Match: interval(80, 89), Allow: true},
					{Name: "deny", Field: netsec.Dst, Match: interval(0, ^uint32(0)), Allow: false},
				},
			},
		},
		[]netsec.Edge{{Src: 1, Dst: 2}, {Src: 2, Dst: 3}},
	)
	if err != nil {
		panic(err)
	}

	report, err := netsec.Analyze(g, netsec.AnalysisConfig{
		Entry: 1,
		Exit:  3,
		Ingress: []netsec.Constraint{
			{Field: netsec.Src, Range: interval(10, 19)},
			{Field: netsec.Dst, Range: interval(80, 89)},
		},
		Egress: []netsec.Constraint{
			{Field: netsec.Src, Range: interval(100, 109)},
			{Field: netsec.Dst, Range: interval(80, 89)},
		},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("reachable:", report.Reachable())
	fmt.Println("mismatches:", len(report.Mismatches))
	w := report.Witnesses[0]
	fmt.Printf("witness: %d->%d along %v\n", w.Packet.Src, w.Packet.Dst, w.Path)
	final, err := w.Replay(g)
	if err != nil {
		panic(err)
	}
	fmt.Printf("replayed final packet: %d->%d\n", final.Src, final.Dst)
	// Output:
	// reachable: true
	// mismatches: 0
	// witness: 10->80 along [1 2 3]
	// replayed final packet: 100->80
}

func interval(lo, hi uint32) netsec.Interval {
	iv, err := netsec.NewInterval(lo, hi)
	if err != nil {
		panic(err)
	}
	return iv
}
