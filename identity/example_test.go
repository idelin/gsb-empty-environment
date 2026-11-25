package identity_test

import (
	"fmt"

	"prescreen.local/ps-2889f92c94/identity"
)

func ExampleAnalyze() {
	in := identity.Input{Events: []identity.Event{
		{ID: 1, Chain: 0, Thread: 1, Kind: identity.EnterRequest, Token: identity.Token{Tenant: "acme", Generation: 1}},
		{ID: 2, Chain: 0, Thread: 1, Kind: identity.Spawn, Slot: 7, HandleGeneration: 3},
		{ID: 10, Chain: 1, Thread: 2, Kind: identity.ResumeHandle, Slot: 7, HandleGeneration: 3, After: []int{2}},
		{ID: 11, Chain: 1, Thread: 2, Kind: identity.ProtectedRead,
			Policy: identity.Policy{Tenant: "acme", Generation: 1}, After: []int{10}},
	}}

	res, err := identity.Analyze(in, identity.Budget{})
	if err != nil {
		panic(err)
	}
	read := res.Reads[11]
	fmt.Println(read.Class == identity.AllConform, read.ReachableConform)
	if err := res.VerifyWitness(11, identity.Conform, read.ConformPrefix); err != nil {
		panic(err)
	}
	if cert := res.Coverage[11]; cert != nil {
		fmt.Println(res.VerifyCoverage(11, cert) == nil)
	}
	// Output:
	// true true
	// true
}
