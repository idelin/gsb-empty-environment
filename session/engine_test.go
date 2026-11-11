package session

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

var baseTime = time.Unix(1_700_000_000, 0)

func tcpFlow(srcIP string, srcPort uint16, dstIP string, dstPort uint16) FlowID {
	return FlowID{
		Protocol: 6,
		SrcAddr:  netip.MustParseAddr(srcIP),
		SrcPort:  srcPort,
		DstAddr:  netip.MustParseAddr(dstIP),
		DstPort:  dstPort,
	}
}

func newTestEngine(policy Policy) (*Engine, *ManualClock, *SerialScheduler) {
	clock := NewManualClock(baseTime)
	scheduler := NewSerialScheduler()
	return NewEngine(policy, clock, scheduler), clock, scheduler
}

func mustConnect(t *testing.T, engine *Engine, flow FlowID, dir Direction) Tuple {
	t.Helper()
	tuple, err := engine.Connect(flow, dir)
	if err != nil {
		t.Fatalf("Connect(%s, %s): unexpected error %v", flow, dir, err)
	}
	return tuple
}

func assertState(t *testing.T, engine *Engine, tuple Tuple, want State) {
	t.Helper()
	session, ok := engine.Lookup(tuple)
	if !ok {
		t.Fatalf("Lookup(%s): session missing", tuple)
	}
	if session.State != want {
		t.Fatalf("Lookup(%s): state = %s, want %s", tuple, session.State, want)
	}
}

func assertKindSeq(t *testing.T, events []Event, kinds ...EventKind) {
	t.Helper()
	if len(events) != len(kinds) {
		t.Fatalf("event count = %d, want %d", len(events), len(kinds))
	}
	for i, want := range kinds {
		if events[i].Kind != want {
			t.Fatalf("event %d kind = %s, want %s", i, events[i].Kind, want)
		}
		if events[i].Seq != int64(i+1) {
			t.Fatalf("event %d seq = %d, want %d", i, events[i].Seq, i+1)
		}
	}
}

// TestGenerationPinningAndUpdateOnlyNew verifies that an existing session
// keeps the generation used at establishment while the new policy only
// governs connections created after the update.
func TestGenerationPinningAndUpdateOnlyNew(t *testing.T) {
	flow := tcpFlow("10.0.0.1", 1234, "10.0.1.1", 80)
	engine, clock, _ := newTestEngine(NewRuleTable(DecisionAllow))

	oldTuple := mustConnect(t, engine, flow, DirectionEgress)
	if oldTuple.StartupGen != 1 {
		t.Fatalf("startup generation = %d, want 1", oldTuple.StartupGen)
	}
	if err := engine.SendFirstPacket(oldTuple); err != nil {
		t.Fatalf("SendFirstPacket: %v", err)
	}

	clock.Advance(time.Second)
	result, err := engine.Update(NewRuleTable(DecisionDeny))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.Generation != 2 {
		t.Fatalf("generation after update = %d, want 2", result.Generation)
	}

	// Grandfathered: the existing connection remains authorized at generation 1.
	if decision, err := engine.Authorize(oldTuple); err != nil || decision != DecisionAllow {
		t.Fatalf("Authorize(old): decision = %s, err = %v; want allow/nil", decision, err)
	}
	assertState(t, engine, oldTuple, StateEstablished)

	// New connections are evaluated against the new generation and denied.
	if _, err := engine.Connect(flow, DirectionEgress); !errors.Is(err, ErrConnectionDenied) {
		t.Fatalf("Connect after deny update: err = %v, want ErrConnectionDenied", err)
	}

	if err := engine.VerifyOrder(); err != nil {
		t.Fatalf("VerifyOrder: %v", err)
	}
	events := engine.Events()
	assertKindSeq(t, events, EventConnect, EventFirstPacket, EventPolicyUpdate, EventConnect)
	if events[3].Gen != 2 || events[3].Decision != DecisionDeny {
		t.Fatalf("denied connect event = %+v, want gen 2 deny", events[3])
	}
}

// TestUpdateWithRevocation verifies the second update mode: an update can
// revoke specific active sessions while leaving others untouched.
func TestUpdateWithRevocation(t *testing.T) {
	victimFlow := tcpFlow("10.0.0.2", 2000, "10.0.1.2", 443)
	survivorFlow := tcpFlow("10.0.0.3", 3000, "10.0.1.3", 443)
	engine, clock, _ := newTestEngine(NewRuleTable(DecisionAllow))

	victim := mustConnect(t, engine, victimFlow, DirectionEgress)
	survivor := mustConnect(t, engine, survivorFlow, DirectionEgress)

	clock.Advance(2 * time.Second)
	result, err := engine.Update(NewRuleTable(DecisionAllow), WithRevocation(victim))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(result.Revoked) != 1 || result.Revoked[0] != victim {
		t.Fatalf("revoked = %+v, want only %s", result.Revoked, victim)
	}
	if len(result.Missing) != 0 {
		t.Fatalf("missing = %+v, want empty", result.Missing)
	}

	assertState(t, engine, victim, StateRevoked)
	assertState(t, engine, survivor, StateNew)

	if _, err := engine.Authorize(victim); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("Authorize(victim): err = %v, want ErrSessionRevoked", err)
	}
	if err := engine.SendFirstPacket(victim); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("SendFirstPacket(victim): err = %v, want ErrSessionRevoked", err)
	}
	if decision, err := engine.Authorize(survivor); err != nil || decision != DecisionAllow {
		t.Fatalf("Authorize(survivor): decision = %s err = %v, want allow/nil", decision, err)
	}

	// Revoking an unknown/inactive tuple is reported, not fatal.
	stale := Tuple{Flow: victimFlow, Direction: DirectionEgress, StartupGen: 99}
	result, err = engine.Update(NewRuleTable(DecisionAllow), WithRevocation(stale))
	if err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if len(result.Missing) != 1 || result.Missing[0] != stale {
		t.Fatalf("missing = %+v, want %s", result.Missing, stale)
	}
	if err := engine.VerifyOrder(); err != nil {
		t.Fatalf("VerifyOrder: %v", err)
	}
}

// TestTupleReuseNoInheritedAuthorization verifies that closing a connection
// and reusing the same transport 5-tuple starts a fresh generation-pinned
// session which is re-evaluated and cannot inherit the old authorization.
func TestTupleReuseNoInheritedAuthorization(t *testing.T) {
	flow := tcpFlow("10.0.0.4", 4000, "10.0.1.4", 22)
	engine, clock, _ := newTestEngine(NewRuleTable(DecisionAllow))

	first := mustConnect(t, engine, flow, DirectionIngress)
	if first.StartupGen != 1 {
		t.Fatalf("first tuple generation = %d, want 1", first.StartupGen)
	}
	if err := engine.End(first); err != nil {
		t.Fatalf("End(first): %v", err)
	}
	assertState(t, engine, first, StateClosed)

	clock.Advance(time.Second)
	if _, err := engine.Update(NewRuleTable(DecisionDeny)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Same transport 5-tuple and direction, but the new tuple is generation 2.
	_, err := engine.Connect(flow, DirectionIngress)
	if !errors.Is(err, ErrConnectionDenied) {
		t.Fatalf("reused tuple Connect: err = %v, want ErrConnectionDenied", err)
	}

	// Re-allowing at generation 3 permits the reused tuple as a brand-new session.
	clock.Advance(time.Second)
	if _, err := engine.Update(NewRuleTable(DecisionAllow)); err != nil {
		t.Fatalf("second Update: %v", err)
	}
	second, err := engine.Connect(flow, DirectionIngress)
	if err != nil {
		t.Fatalf("reused tuple Connect after allow: %v", err)
	}
	if second.StartupGen != 3 {
		t.Fatalf("reused tuple generation = %d, want 3", second.StartupGen)
	}
	if second == first {
		t.Fatalf("reused tuple %s inherited old tuple %s", second, first)
	}
	assertState(t, engine, second, StateNew)

	if err := engine.VerifyOrder(); err != nil {
		t.Fatalf("VerifyOrder: %v", err)
	}
}

// TestLateEndDoesNotRemoveNewConnection verifies that an end event carrying
// an old generation is keyed exactly and cannot remove a newer session on the
// same transport tuple.
func TestLateEndDoesNotRemoveNewConnection(t *testing.T) {
	flow := tcpFlow("10.0.0.5", 5000, "10.0.1.5", 53)
	engine, _, _ := newTestEngine(NewRuleTable(DecisionAllow))

	old := mustConnect(t, engine, flow, DirectionEgress)
	if err := engine.End(old); err != nil {
		t.Fatalf("End(old): %v", err)
	}

	result, err := engine.Update(NewRuleTable(DecisionAllow))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.Generation != 2 {
		t.Fatalf("generation = %d, want 2", result.Generation)
	}

	fresh := mustConnect(t, engine, flow, DirectionEgress)
	if fresh.StartupGen != 2 {
		t.Fatalf("fresh tuple generation = %d, want 2", fresh.StartupGen)
	}

	// The late end event still targets the generation-1 tuple.
	if err := engine.End(old); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("late End(old): err = %v, want ErrSessionClosed", err)
	}
	assertState(t, engine, fresh, StateNew)
	if decision, err := engine.Authorize(fresh); err != nil || decision != DecisionAllow {
		t.Fatalf("Authorize(fresh) after late end: decision = %s err = %v", decision, err)
	}

	// A fabricated stale generation tuple cannot affect anything.
	stale := Tuple{Flow: flow, Direction: DirectionEgress, StartupGen: 42}
	if err := engine.End(stale); !errors.Is(err, ErrSessionUnknown) {
		t.Fatalf("End(stale): err = %v, want ErrSessionUnknown", err)
	}
	assertState(t, engine, fresh, StateNew)

	// Ending the exact fresh tuple still works.
	if err := engine.End(fresh); err != nil {
		t.Fatalf("End(fresh): %v", err)
	}
	assertState(t, engine, fresh, StateClosed)
}

// TestLinearOrderWithScheduler verifies the linear ordering between update,
// connect and first-packet events using the injected clock and scheduler.
func TestLinearOrderWithScheduler(t *testing.T) {
	flow := tcpFlow("10.0.0.6", 6000, "10.0.1.6", 8080)
	engine, clock, scheduler := newTestEngine(NewRuleTable(DecisionAllow))

	engine.ScheduledConnect(flow, DirectionEgress)
	if scheduler.Pending() != 1 {
		t.Fatalf("pending = %d, want 1", scheduler.Pending())
	}

	clock.Advance(time.Second)
	engine.ScheduledUpdate(NewRuleTable(DecisionDeny))

	clock.Advance(time.Second)
	// First packet targets the generation-1 tuple captured by the connect task.
	firstTuple := Tuple{Flow: flow, Direction: DirectionEgress, StartupGen: 1}
	engine.ScheduledFirstPacket(firstTuple)

	if scheduler.Pending() != 3 {
		t.Fatalf("pending = %d, want 3", scheduler.Pending())
	}
	// Dispatch one event at a time and move the injected clock between events
	// so the recorded timestamps are strictly increasing.
	if !scheduler.DispatchOne() {
		t.Fatal("first DispatchOne returned false")
	}
	clock.Advance(time.Second)
	if !scheduler.DispatchOne() {
		t.Fatal("second DispatchOne returned false")
	}
	clock.Advance(time.Second)
	if !scheduler.DispatchOne() {
		t.Fatal("third DispatchOne returned false")
	}
	if scheduler.Pending() != 0 {
		t.Fatalf("pending after dispatch = %d, want 0", scheduler.Pending())
	}

	events := engine.Events()
	assertKindSeq(t, events,
		EventConnect,
		EventPolicyUpdate,
		EventFirstPacket,
	)
	if !events[0].At.Before(events[1].At) || !events[1].At.Before(events[2].At) {
		t.Fatalf("event timestamps not strictly increasing: %+v", events)
	}
	if events[0].Gen != 1 || events[1].Gen != 2 || events[2].Gen != 2 {
		t.Fatalf("event generations = %d,%d,%d, want 1,2,2",
			events[0].Gen, events[1].Gen, events[2].Gen)
	}
	if events[2].Tuple != firstTuple {
		t.Fatalf("first packet tuple = %s, want %s", events[2].Tuple, firstTuple)
	}

	// The grandfathered session established before the update still sends its
	// first packet successfully even though new connections are now denied.
	assertState(t, engine, firstTuple, StateEstablished)
	if _, err := engine.Connect(flow, DirectionEgress); !errors.Is(err, ErrConnectionDenied) {
		t.Fatalf("new connect after scheduled update: err = %v", err)
	}
	if err := engine.VerifyOrder(); err != nil {
		t.Fatalf("VerifyOrder: %v", err)
	}

	// ManualClock refuses to move backwards, preserving the linear timeline.
	if clock.Set(baseTime) {
		t.Fatal("ManualClock.Set accepted a time in the past")
	}
}

// TestDirectionAndInvalidInputs verifies that direction is part of the tuple
// and that invalid inputs are rejected explicitly.
func TestDirectionAndInvalidInputs(t *testing.T) {
	flow := tcpFlow("10.0.0.7", 7000, "10.0.1.7", 25)
	engine, _, _ := newTestEngine(NewRuleTable(DecisionAllow))

	ingress := mustConnect(t, engine, flow, DirectionIngress)
	egress := mustConnect(t, engine, flow, DirectionEgress)
	if ingress == egress {
		t.Fatalf("ingress tuple %s must differ from egress tuple %s", ingress, egress)
	}
	assertState(t, engine, ingress, StateNew)
	assertState(t, engine, egress, StateNew)

	invalid := FlowID{Protocol: 6, SrcAddr: netip.MustParseAddr("10.0.0.8"), SrcPort: 1}
	if _, err := engine.Connect(invalid, DirectionEgress); !errors.Is(err, ErrInvalidTuple) {
		t.Fatalf("Connect invalid flow: err = %v, want ErrInvalidTuple", err)
	}
	if _, err := engine.Connect(flow, DirectionUnknown); !errors.Is(err, ErrInvalidTuple) {
		t.Fatalf("Connect unknown direction: err = %v, want ErrInvalidTuple", err)
	}

	unknown := Tuple{Flow: flow, Direction: DirectionEgress, StartupGen: 77}
	if _, err := engine.Authorize(unknown); !errors.Is(err, ErrSessionUnknown) {
		t.Fatalf("Authorize unknown: err = %v, want ErrSessionUnknown", err)
	}
	if err := engine.SendFirstPacket(unknown); !errors.Is(err, ErrSessionUnknown) {
		t.Fatalf("SendFirstPacket unknown: err = %v, want ErrSessionUnknown", err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("NewEngine(nil, ...) should panic")
			}
		}()
		NewEngine(nil, nil, nil)
	}()

	if err := engine.VerifyOrder(); err != nil {
		t.Fatalf("VerifyOrder: %v", err)
	}
}
