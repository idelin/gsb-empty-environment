package netsec_test

import (
	"fmt"
	"time"

	netsec "example.org/netsec/q0728"
)

// Example shows a select-before-execute point being armed and confirmed, and
// an attacker on a second connection being rejected.
func ExampleStateMachine() {
	start := time.Unix(1700000000, 0)
	clock := func() time.Time { return start }

	sm := netsec.NewStateMachine(netsec.WithClock(clock), netsec.RejectUnknownPoints())
	sm.AddPoint(300, true) // point 300 requires select then execute

	const trusted netsec.ConnID = "tcp-conn-1"
	const attacker netsec.ConnID = "tcp-conn-2"

	// 1) Trusted connection sends the select (S/E=1, ON).
	sel := netsec.SingleCommand{IOA: 300, Select: true, On: true}
	fmt.Println("select:", sm.HandleSingleCommand(trusted, netsec.COTActivation, sel).Kind)

	// 2) Attacker on another connection attempts the execute directly.
	exec := netsec.SingleCommand{IOA: 300, Select: false, On: true}
	attack := sm.HandleSingleCommand(attacker, netsec.COTActivation, exec)
	fmt.Println("cross-connection execute rejected:", attack.Rejected(), "-", attack.Reason)

	// 3) Trusted connection confirms with same IOA and value (S/E=0).
	fmt.Println("execute:", sm.HandleSingleCommand(trusted, netsec.COTActivation, exec).Kind)

	// Output:
	// select: Selected
	// cross-connection execute rejected: true - netsec: selection is owned by another connection
	// execute: Executed
}
