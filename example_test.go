package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0728"
)

func Example() {
	state := netsec.New(netsec.WithSelectBeforeOperate(100))

	fmt.Println(state.HandleSCO("conn-1", 6, 100, 0x81))
	fmt.Println(state.HandleSCO("conn-1", 6, 100, 0x01))
	fmt.Println(state.HandleSCO("conn-2", 6, 100, 0x01))

	// Output:
	// {true select-accepted}
	// {true execute-accepted}
	// {false select-required}
}
