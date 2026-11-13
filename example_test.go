package netsec_test

import (
	"fmt"
	"net/netip"

	netsec "example.org/netsec/q0734"
)

func ExampleTable_Check() {
	table, err := netsec.NewTable([]netsec.Route{
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Egress: "eth0"},
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Egress: "eth1"},
	}, netsec.Config{})
	if err != nil {
		panic(err)
	}

	decision := table.Check(netip.MustParseAddr("192.0.2.55"), "eth1", netsec.ModeStrict)
	fmt.Println(decision.Allowed)
	// Output:
	// true
}
