package flowspec_test

import (
	"fmt"
	"net/netip"

	"example.org/netsec/q0716/flowspec"
)

type alertSink []flowspec.Alert

func (a *alertSink) FlowSpecAlert(alert flowspec.Alert) {
	*a = append(*a, alert)
}

func Example() {
	rib := flowspec.NewMemoryRIB()
	rib.Set(flowspec.UnicastRoute{
		Prefix: netip.MustParsePrefix("192.0.2.0/24"),
		PeerAS: 64510,
	})

	alerts := alertSink{}
	manager := flowspec.NewManager(rib, &alerts)

	dest, err := flowspec.PrefixComponent(flowspec.TypeDestinationPrefix,
		netip.MustParsePrefix("192.0.2.0/24"))
	if err != nil {
		panic(err)
	}

	// Rule from the same neighbor AS: installed.
	installed, err := manager.Receive(flowspec.Rule{
		Key:        "same-as",
		NeighborAS: 64510,
		NLRI: flowspec.NLRI{
			AFI:        flowspec.AFIIPv4,
			Components: []flowspec.Component{dest},
		},
	})
	fmt.Println("same-AS installed:", installed, "err:", err)

	// Rule from a different neighbor AS: rejected, alert emitted.
	installed, err = manager.Receive(flowspec.Rule{
		Key:        "foreign",
		NeighborAS: 64520,
		NLRI: flowspec.NLRI{
			AFI:        flowspec.AFIIPv4,
			Components: []flowspec.Component{dest},
		},
	})
	fmt.Println("foreign installed:", installed)
	fmt.Println("alert code:", alerts[0].Code)

	// Output:
	// same-AS installed: true err: <nil>
	// foreign installed: false
	// alert code: foreign_origin
}
