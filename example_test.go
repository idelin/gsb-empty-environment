package netsec_test

import (
	"fmt"

	"example.org/netsec/q0822"
)

func ExamplePort_singleHost() {
	port := netsec.NewPort(netsec.SingleHost, netsec.Shutdown)
	first := netsec.MustParseMAC("02:00:00:00:00:01")
	second := netsec.MustParseMAC("02:00:00:00:00:02")

	port.Authenticate(first)
	fmt.Println(port.Evaluate(first))
	fmt.Println(port.Evaluate(second))
	fmt.Println(port.Disabled())
	// Output:
	// 02:00:00:00:00:01 forward: authorized (allowed)
	// 02:00:00:00:00:02 shutdown: foreign-source (denied)
	// true
}
