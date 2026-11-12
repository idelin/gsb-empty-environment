// Command msglang-demo demonstrates the handshake label-ambiguity analysis:
// it computes the smallest message tree accepted by both handshake phases and
// prints one parsing derivation per phase.
package main

import (
	"fmt"

	"example.org/netsec/q0470/msglang"
)

func main() {
	ack := msglang.AmbiguousAcknowledge()
	grant := msglang.AmbiguousAuthorization()

	result, err := msglang.MinimalIntersection(ack, grant)
	if err != nil {
		panic(err)
	}
	fmt.Println("== vulnerable design: shared encoding shape ==")
	fmt.Printf("common minimum tree (%d nodes, height %d):\n  %s\n\n",
		result.NodeCount, result.Height, result.Tree)

	fmt.Println("== parsing path in the acknowledgment phase ==")
	fmt.Print(result.LeftTrace.String())
	fmt.Println()
	fmt.Println("== parsing path in the authorization phase ==")
	fmt.Print(result.RightTrace.String())

	safe, err := msglang.MinimalIntersection(
		msglang.SafeAcknowledge(),
		msglang.SafeAuthorization(),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println()
	fmt.Println("== hardened design: phase-specific body labels ==")
	fmt.Println("intersection empty:", safe.Empty)
}
