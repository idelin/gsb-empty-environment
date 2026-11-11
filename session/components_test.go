package session

import (
	"net/netip"
	"testing"
	"time"
)

func TestRuleTableDirectionAware(t *testing.T) {
	flow := FlowID{
		Protocol: 6,
		SrcAddr:  netip.MustParseAddr("192.0.2.1"),
		SrcPort:  1111,
		DstAddr:  netip.MustParseAddr("192.0.2.2"),
		DstPort:  2222,
	}
	table := NewRuleTable(DecisionDeny)

	if got := table.Decide(flow, DirectionIngress); got != DecisionDeny {
		t.Fatalf("fallback decision = %s, want deny", got)
	}

	table.Allow(flow, DirectionIngress)
	table.Deny(flow, DirectionEgress)

	if got := table.Decide(flow, DirectionIngress); got != DecisionAllow {
		t.Fatalf("ingress decision = %s, want allow", got)
	}
	if got := table.Decide(flow, DirectionEgress); got != DecisionDeny {
		t.Fatalf("egress decision = %s, want deny", got)
	}

	other := flow
	other.DstPort = 9999
	if got := table.Decide(other, DirectionIngress); got != DecisionDeny {
		t.Fatalf("unrelated flow decision = %s, want fallback deny", got)
	}

	table.Set(flow, DirectionIngress, DecisionDeny)
	if got := table.Decide(flow, DirectionIngress); got != DecisionDeny {
		t.Fatalf("overridden decision = %s, want deny", got)
	}
}

func TestManualClockMonotonic(t *testing.T) {
	start := baseTime
	clock := NewManualClock(start)
	if !clock.Now().Equal(start) {
		t.Fatalf("initial time = %v, want %v", clock.Now(), start)
	}

	clock.Advance(5 * time.Second)
	if !clock.Now().Equal(start.Add(5 * time.Second)) {
		t.Fatalf("after advance time = %v", clock.Now())
	}
	clock.Advance(-time.Second)
	if !clock.Now().Equal(start.Add(5 * time.Second)) {
		t.Fatalf("negative advance changed time to %v", clock.Now())
	}

	if ok := clock.Set(start); ok {
		t.Fatal("Set accepted a past time")
	}
	later := start.Add(10 * time.Second)
	if ok := clock.Set(later); !ok {
		t.Fatal("Set rejected a future time")
	}
	if !clock.Now().Equal(later) {
		t.Fatalf("time after Set = %v, want %v", clock.Now(), later)
	}

	got := SystemClock{}.Now()
	if got.IsZero() {
		t.Fatal("SystemClock.Now returned zero time")
	}
}

func TestSerialSchedulerFIFO(t *testing.T) {
	scheduler := NewSerialScheduler()
	if scheduler.DispatchOne() {
		t.Fatal("DispatchOne on empty scheduler returned true")
	}

	var order []int
	scheduler.Schedule(func() { order = append(order, 1) })
	scheduler.Schedule(func() { order = append(order, 2) })
	scheduler.Schedule(func() { order = append(order, 3) })
	if scheduler.Pending() != 3 {
		t.Fatalf("pending = %d, want 3", scheduler.Pending())
	}

	if !scheduler.DispatchOne() {
		t.Fatal("DispatchOne returned false with queued tasks")
	}
	if len(order) != 1 || order[0] != 1 {
		t.Fatalf("order after one dispatch = %v, want [1]", order)
	}

	scheduler.DispatchAll()
	if scheduler.Pending() != 0 {
		t.Fatalf("pending = %d, want 0", scheduler.Pending())
	}
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("final order = %v, want [1 2 3]", order)
	}
	if scheduler.DispatchOne() {
		t.Fatal("DispatchOne after draining returned true")
	}
}

func TestStringers(t *testing.T) {
	flow := tcpFlow("198.51.100.1", 100, "198.51.100.2", 200)
	tuple := Tuple{Flow: flow, Direction: DirectionIngress, StartupGen: 7}

	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"direction", DirectionEgress.String(), "egress"},
		{"unknown direction", DirectionUnknown.String(), "unknown"},
		{"flow", flow.String(), "6/198.51.100.1:100 -> 198.51.100.2:200"},
		{"tuple", tuple.String(), "ingress 6/198.51.100.1:100 -> 198.51.100.2:200 gen=7"},
		{"state new", StateNew.String(), "new"},
		{"state established", StateEstablished.String(), "established"},
		{"state closed", StateClosed.String(), "closed"},
		{"state revoked", StateRevoked.String(), "revoked"},
		{"state none", StateNone.String(), "none"},
		{"decision allow", DecisionAllow.String(), "allow"},
		{"decision deny", DecisionDeny.String(), "deny"},
		{"event update", EventPolicyUpdate.String(), "policy_update"},
		{"event connect", EventConnect.String(), "connect"},
		{"event packet", EventFirstPacket.String(), "first_packet"},
		{"event end", EventEnd.String(), "end"},
		{"event revoke", EventRevoke.String(), "revoke"},
		{"event unknown", EventKind(99).String(), "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}
