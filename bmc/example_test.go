package bmc_test

import (
	"fmt"

	"example.org/netsec/q0759/bmc"
)

// This example shows how a boundary clock admits authenticated, authorized
// Announce messages and elects the best grandmaster while rejecting an
// attacker whose priority1 is zero.
func ExampleElector() {
	primary := bmc.ClockIdentity{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}
	attacker := bmc.ClockIdentity{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0xff}

	elector := bmc.NewElector(
		[]bmc.ClockIdentity{primary},
		func(source bmc.PortIdentity) bool {
			// Wire in the caller's port-level authentication (e.g. ICV/MAC).
			return true
		},
	)

	spoofed := bmc.Announce{
		GrandmasterPriority1: 0,
		GrandmasterIdentity:  attacker,
		StepsRemoved:         0,
		SourcePort:           bmc.PortIdentity{ClockIdentity: attacker, PortNumber: 1},
	}
	admitted, reason := elector.HandleAnnounce(spoofed)
	fmt.Printf("spoofed: admitted=%v reason=%s\n", admitted, reason)

	legitimate := bmc.Announce{
		GrandmasterPriority1:               128,
		GrandmasterClockClass:              248,
		GrandmasterClockAccuracy:           0x21,
		GrandmasterOffsetScaledLogVariance: 0x4e5d,
		GrandmasterPriority2:               128,
		GrandmasterIdentity:                primary,
		StepsRemoved:                       1,
		SourcePort:                         bmc.PortIdentity{ClockIdentity: primary, PortNumber: 1},
	}
	admitted, reason = elector.HandleAnnounce(legitimate)
	fmt.Printf("legitimate: admitted=%v reason=%s\n", admitted, reason)

	best := elector.Elect()
	fmt.Printf("elected grandmaster: %x\n", best.Announce.GrandmasterIdentity)

	// Output:
	// spoofed: admitted=false reason=grandmaster identity not authorized
	// legitimate: admitted=true reason=admitted
	// elected grandmaster: 0001020304050607
}
