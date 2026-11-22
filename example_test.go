package changeaccept_test

import (
	"fmt"

	changeaccept "prescreen.local/ddd-1574-20261122-v2"
)

func ExampleInput_Check() {
	auth, _ := changeaccept.NewAuthorizer(
		[]changeaccept.TenantID{"payments", "search"},
		map[changeaccept.TenantPair]changeaccept.PairPolicy{
			{Source: "payments", Destination: "search"}: {{
				Source:      changeaccept.AnyLabel(),
				Destination: changeaccept.AnyLabel(),
				Action:      changeaccept.ActionAllow,
			}},
		},
	)
	input := changeaccept.Input{
		Topology: changeaccept.Topology{
			Endpoints: []changeaccept.Endpoint{
				{ID: "a", Tenant: "payments", Egress: changeaccept.Policy{{
					Match:  changeaccept.Labels("gold"),
					Action: changeaccept.ActionAllow,
				}}},
				{ID: "b", Tenant: "search", Ingress: changeaccept.Policy{{
					Match:  changeaccept.Labels("gold"),
					Action: changeaccept.ActionAllow,
				}}},
			},
			Edges: []changeaccept.Edge{{From: "a", To: "b"}},
		},
		Auth:   auth,
		Labels: []changeaccept.Label{"gold"},
		Pairs:  []changeaccept.TenantPair{{Source: "payments", Destination: "search"}},
	}
	report, _ := input.Check()
	fmt.Println(report.Outcome)
	// Output: compliant
}
