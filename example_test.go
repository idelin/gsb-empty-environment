package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/uid1292"
)

// ExampleCheckLeak demonstrates finding the minimal internal route that
// leaks to a forbidden neighbor after a sub-policy strips the secret
// community tag, together with the per-rule execution trace.
func ExamplePolicySet_CheckLeak() {
	const (
		secret netsec.Tag = 0
		egress netsec.Tag = 1
	)
	ps := &netsec.PolicySet{Policies: map[string]*netsec.Policy{
		"export": {
			Name: "export",
			Rules: []netsec.Rule{
				{Name: "scrub", Do: netsec.CallPolicy{Name: "scrub"}},
				{
					Name: "deny-secret",
					When: netsec.TagCondition{Op: netsec.TagAll, Tags: netsec.TagSet(0).With(secret)},
					Do:   netsec.Reject{},
				},
				{Name: "attach-egress", Do: netsec.AddTags{Tags: netsec.TagSet(0).With(egress)}},
				{Name: "accept", Do: netsec.Accept{}},
			},
		},
		"scrub": {
			Name: "scrub",
			Rules: []netsec.Rule{
				{
					Name: "strip-secret",
					When: netsec.PrefixRange{
						Network: netsec.MustParsePrefix("10.0.0.0/8"),
						MinLen:  16,
						MaxLen:  24,
					},
					Do: netsec.DelTags{Tags: netsec.TagSet(0).With(secret)},
				},
			},
		},
	}}

	report := ps.CheckLeak(netsec.LeakSpec{
		Entry:      "export",
		SecretTag:  secret,
		Neighbors:  []netsec.Neighbor{{Name: "forbidden-peer"}},
		MaxPathLen: 6,
	})

	fmt.Println("safe:", report.Safe)
	fmt.Println("minimal leaking route:", report.Leak.Route)
	fmt.Println("exported route:", report.Leak.ExportedRoute)
	fmt.Println("domain evaluations:", report.Stats.Evaluations)
	fmt.Print(netsec.RenderTrace(report.Leak.Trace))
	// Output:
	// safe: false
	// minimal leaking route: route{10.0.0.0/16 pathlen=0 tags[0]}
	// exported route: route{10.0.0.0/16 pathlen=0 tags[1]}
	// domain evaluations: 10
	// enter policy export tags=tags[0]
	//   rule scrub MATCH tags[0] -> tags[]
	//     enter policy scrub tags=tags[0]
	//       rule strip-secret MATCH tags[0] -> tags[]
	//     leave policy scrub verdict=FALLTHROUGH
	//   rule deny-secret skip
	//   rule attach-egress MATCH tags[] -> tags[1]
	//   rule accept MATCH tags[1] -> tags[1] ACCEPT
	// leave policy export verdict=ACCEPT
	// => ACCEPT
}
