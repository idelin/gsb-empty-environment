package netsec

import (
	"testing"
	"time"
)

type fakeClock struct {
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func testBridge(priority uint16, lastByte byte) BridgeID {
	return BridgeID{
		Priority: priority,
		MAC:      [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, lastByte},
	}
}

func configBPDU(root BridgeID, cost uint32, sender BridgeID, port PortID) BPDU {
	return BPDU{
		Type:           ConfigurationBPDU,
		RootID:         root,
		RootPathCost:   cost,
		SenderBridgeID: sender,
		SenderPortID:   port,
		MessageAge:     time.Second,
	}
}

func TestPriorityVectorComparisonOrder(t *testing.T) {
	rootA := testBridge(0x1000, 1)
	rootB := testBridge(0x2000, 1)
	senderA := testBridge(0x3000, 1)
	senderB := testBridge(0x3000, 2)

	base := PriorityVector{
		RootID:         rootB,
		RootPathCost:   10,
		SenderBridgeID: senderB,
		SenderPortID:   2,
	}

	tests := []struct {
		name       string
		challenger PriorityVector
		want       int
	}{
		{
			name: "better root ID",
			challenger: PriorityVector{
				RootID: rootA, RootPathCost: 99, SenderBridgeID: senderB, SenderPortID: 2,
			},
			want: -1,
		},
		{
			name: "equal root, lower path cost",
			challenger: PriorityVector{
				RootID: rootB, RootPathCost: 9, SenderBridgeID: senderB, SenderPortID: 2,
			},
			want: -1,
		},
		{
			name: "equal root and cost, lower sender bridge ID",
			challenger: PriorityVector{
				RootID: rootB, RootPathCost: 10, SenderBridgeID: senderA, SenderPortID: 2,
			},
			want: -1,
		},
		{
			name: "equal root, cost and sender, lower port ID",
			challenger: PriorityVector{
				RootID: rootB, RootPathCost: 10, SenderBridgeID: senderB, SenderPortID: 1,
			},
			want: -1,
		},
		{
			name:       "identical",
			challenger: base,
			want:       0,
		},
		{
			name: "worse root ID",
			challenger: PriorityVector{
				RootID: testBridge(0x2000, 2), RootPathCost: 0, SenderBridgeID: senderA, SenderPortID: 1,
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.challenger.Compare(base); got != tt.want {
				t.Fatalf("Compare() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEdgePortBlocksForgedRootUntilQuietPeriodEnds(t *testing.T) {
	clock := newFakeClock()
	local := testBridge(0x8000, 1)
	attacker := testBridge(0x0000, 0x66)
	legitimate := testBridge(0x9000, 2)

	g := NewGuard(GuardConfig{
		LocalBridgeID: local,
		QuietPeriod:   30 * time.Second,
		Now:           clock.Now,
	})
	g.AddPort(PortConfig{ID: 1, Edge: true})

	forged := configBPDU(attacker, 0, attacker, 1)
	res := g.Receive(1, forged)
	if res.Accepted {
		t.Fatal("forged superior BPDU on edge port was accepted")
	}
	if res.Reason != ReasonEdgeGuard {
		t.Fatalf("drop reason = %v, want %v", res.Reason, ReasonEdgeGuard)
	}
	if res.State != StateBlocking || res.Forwarding {
		t.Fatalf("port state = %v forwarding=%v, want blocking", res.State, res.Forwarding)
	}
	wantUntil := clock.Now().Add(30 * time.Second)
	if !res.BlockedUntil.Equal(wantUntil) {
		t.Fatalf("BlockedUntil = %v, want %v", res.BlockedUntil, wantUntil)
	}
	if got := g.CurrentRootVector().RootID; got != local {
		t.Fatalf("forged BPDU changed current root to %v, want %v", got, local)
	}

	// A second superior BPDU refreshes the quiet period rather than allowing
	// the attacker to wait out a timer that started on its first frame.
	clock.Advance(10 * time.Second)
	res = g.Receive(1, forged)
	if res.Reason != ReasonEdgeGuard {
		t.Fatalf("second drop reason = %v, want %v", res.Reason, ReasonEdgeGuard)
	}
	wantUntil = clock.Now().Add(30 * time.Second)
	if !res.BlockedUntil.Equal(wantUntil) {
		t.Fatalf("refreshed BlockedUntil = %v, want %v", res.BlockedUntil, wantUntil)
	}

	clock.Advance(29 * time.Second)
	if got := g.PortState(1); got != StateBlocking {
		t.Fatalf("port state before quiet period end = %v, want blocking", got)
	}
	res = g.Receive(1, configBPDU(legitimate, 0, legitimate, 1))
	if res.Accepted || res.Reason != ReasonPortBlocked {
		t.Fatalf("non-superior BPDU during quiet period: %+v", res)
	}

	clock.Advance(time.Second)
	if got := g.PortState(1); got != StateForwarding {
		t.Fatalf("port state at quiet period end = %v, want forwarding", got)
	}

	// An inferior, legitimate BPDU is admitted after recovery.
	res = g.Receive(1, configBPDU(legitimate, 0, legitimate, 1))
	if !res.Accepted || !res.Forwarding || res.State != StateForwarding {
		t.Fatalf("legitimate BPDU after recovery: %+v", res)
	}

	stats := g.PortCounters(1)
	if stats.GuardViolations != 2 {
		t.Fatalf("guard violations = %d, want 2", stats.GuardViolations)
	}
	if stats.Dropped != 3 { // two forged frames and one frame during the quiet period
		t.Fatalf("dropped = %d, want 3", stats.Dropped)
	}
	if stats.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1", stats.Accepted)
	}
}

func TestRootGuardBlocksSuperiorAllowsInferior(t *testing.T) {
	clock := newFakeClock()
	local := testBridge(0x8000, 1)
	g := NewGuard(GuardConfig{
		LocalBridgeID: local,
		QuietPeriod:   time.Second,
		Now:           clock.Now,
	})
	g.AddPort(PortConfig{ID: 7, RootGuard: true})

	inferior := configBPDU(testBridge(0x9000, 2), 0, testBridge(0x9000, 2), 1)
	res := g.Receive(7, inferior)
	if !res.Accepted || !res.Forwarding {
		t.Fatalf("inferior BPDU on root-guard port: %+v", res)
	}

	attacker := testBridge(0x0100, 0xAA)
	res = g.Receive(7, configBPDU(attacker, 0, attacker, 1))
	if res.Accepted {
		t.Fatal("superior BPDU on root-guard port was accepted")
	}
	if res.Reason != ReasonRootGuard {
		t.Fatalf("drop reason = %v, want %v", res.Reason, ReasonRootGuard)
	}
	if res.State != StateBlocking {
		t.Fatalf("state = %v, want blocking", res.State)
	}
	if got := g.CurrentRootVector().RootID; got != local {
		t.Fatalf("forged BPDU changed current root to %v, want %v", got, local)
	}

	clock.Advance(time.Second)
	if got := g.PortState(7); got != StateForwarding {
		t.Fatalf("state after quiet period = %v, want forwarding", got)
	}
}

func TestBPDUWithMessageAgeAtMaxAgeIsDropped(t *testing.T) {
	clock := newFakeClock()
	local := testBridge(0x8000, 1)
	g := NewGuard(GuardConfig{
		LocalBridgeID: local,
		MaxAge:        20 * time.Second,
		Now:           clock.Now,
	})
	g.AddPort(PortConfig{ID: 2})

	atLimit := configBPDU(local, 0, local, 1)
	atLimit.MessageAge = 20 * time.Second
	res := g.Receive(2, atLimit)
	if res.Accepted || res.Reason != ReasonMessageAgeExpired {
		t.Fatalf("message age == max age: %+v", res)
	}
	if res.State != StateForwarding {
		t.Fatalf("age drop changed forwarding state to %v", res.State)
	}

	belowLimit := atLimit
	belowLimit.MessageAge = 20*time.Second - time.Nanosecond
	res = g.Receive(2, belowLimit)
	if !res.Accepted {
		t.Fatalf("message age just below max age was dropped: %+v", res)
	}

	// The advertised MaxAge can only make the local policy stricter.
	smallerMaxAge := atLimit
	smallerMaxAge.MessageAge = 5 * time.Second
	smallerMaxAge.MaxAge = 5 * time.Second
	res = g.Receive(2, smallerMaxAge)
	if res.Accepted || res.Reason != ReasonMessageAgeExpired {
		t.Fatalf("message age == advertised max age: %+v", res)
	}
}

func TestTCNFloodIsRateLimitedPerPort(t *testing.T) {
	clock := newFakeClock()
	g := NewGuard(GuardConfig{
		LocalBridgeID: testBridge(0x8000, 1),
		TCNRate:       1,
		TCNBurst:      1,
		Now:           clock.Now,
	})
	g.AddPort(PortConfig{ID: 1})
	g.AddPort(PortConfig{ID: 2})

	tcn := BPDU{Type: TCNBPDU}
	res := g.Receive(1, tcn)
	if !res.Accepted || !res.Forwarding {
		t.Fatalf("first TCN: %+v", res)
	}

	for i := 0; i < 100; i++ {
		res = g.Receive(1, tcn)
		if res.Accepted || res.Reason != ReasonTCNRateLimit {
			t.Fatalf("TCN %d during flood: %+v", i+2, res)
		}
		if !res.Forwarding || res.State != StateForwarding {
			t.Fatalf("TCN drop changed forwarding state: %+v", res)
		}
	}

	if res = g.Receive(2, tcn); !res.Accepted {
		t.Fatalf("TCN on independent port was limited: %+v", res)
	}

	clock.Advance(time.Second)
	if res = g.Receive(1, tcn); !res.Accepted {
		t.Fatalf("TCN after refill interval: %+v", res)
	}

	stats := g.PortCounters(1)
	if stats.TCNs != 102 {
		t.Fatalf("TCNs = %d, want 102", stats.TCNs)
	}
	if stats.TCNRateLimited != 100 {
		t.Fatalf("TCNRateLimited = %d, want 100", stats.TCNRateLimited)
	}
	if stats.Accepted != 2 {
		t.Fatalf("accepted = %d, want 2", stats.Accepted)
	}
}

func TestLegitimateTopologyChangeIsAcceptedAndLearnsRoot(t *testing.T) {
	clock := newFakeClock()
	local := testBridge(0x8000, 1)
	legitRoot := testBridge(0x4000, 10)
	legitSender := testBridge(0x5000, 11)

	g := NewGuard(GuardConfig{
		LocalBridgeID: local,
		TCNRate:       1,
		TCNBurst:      2,
		Now:           clock.Now,
	})
	g.AddPort(PortConfig{ID: 10})

	change := configBPDU(legitRoot, 4, legitSender, 2)
	change.TopologyChange = true
	res := g.Receive(10, change)
	if !res.Accepted || !res.Forwarding {
		t.Fatalf("legitimate topology change: %+v", res)
	}
	if got := g.CurrentRootVector(); got != change.Vector() {
		t.Fatalf("learned vector = %+v, want %+v", got, change.Vector())
	}

	clock.Advance(time.Second)
	if res = g.Receive(10, change); !res.Accepted {
		t.Fatalf("later legitimate topology change: %+v", res)
	}

	stats := g.PortCounters(10)
	if stats.TCNs != 2 || stats.TCNRateLimited != 0 {
		t.Fatalf("unexpected TCN stats: %+v", stats)
	}
}

func TestInferiorBPDUOnEdgePortDoesNotBlock(t *testing.T) {
	clock := newFakeClock()
	g := NewGuard(GuardConfig{
		LocalBridgeID: testBridge(0x8000, 1),
		Now:           clock.Now,
	})
	g.AddPort(PortConfig{ID: 3, Edge: true})

	root := testBridge(0x9000, 2)
	res := g.Receive(3, configBPDU(root, 0, root, 1))
	if !res.Accepted || !res.Forwarding || res.State != StateForwarding {
		t.Fatalf("inferior BPDU on edge port: %+v", res)
	}
}
