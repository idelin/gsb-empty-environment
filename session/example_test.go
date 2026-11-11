package session_test

import (
	"fmt"
	"net/netip"
	"time"

	"example.org/netsec/uid1289/session"
)

func ExampleEngine() {
	flow := session.FlowID{
		Protocol: 6,
		SrcAddr:  netip.MustParseAddr("10.0.0.10"),
		SrcPort:  40000,
		DstAddr:  netip.MustParseAddr("10.0.2.10"),
		DstPort:  443,
	}

	clock := session.NewManualClock(time.Unix(1_700_000_000, 0))
	engine := session.NewEngine(session.NewRuleTable(session.DecisionAllow), clock, session.NewSerialScheduler())

	// Generation 1: connection is allowed and pins that generation.
	established, err := engine.Connect(flow, session.DirectionEgress)
	if err != nil {
		panic(err)
	}
	if err := engine.SendFirstPacket(established); err != nil {
		panic(err)
	}

	// Update to a deny policy without revoking existing sessions.
	clock.Advance(time.Second)
	if _, err := engine.Update(session.NewRuleTable(session.DecisionDeny)); err != nil {
		panic(err)
	}

	// The grandfathered session stays authorized; a reused tuple is re-evaluated.
	decision, err := engine.Authorize(established)
	fmt.Printf("existing: %s (%v)\n", decision, err)

	if _, err := engine.Connect(flow, session.DirectionEgress); err != nil {
		fmt.Println("new connection: denied")
	}

	// A late end event for the old tuple cannot touch other generations.
	if err := engine.End(established); err != nil {
		panic(err)
	}
	fmt.Printf("ordering valid: %v\n", engine.VerifyOrder() == nil)

	// Output:
	// existing: allow (<nil>)
	// new connection: denied
	// ordering valid: true
}
