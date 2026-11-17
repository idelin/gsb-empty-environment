package netsec_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	netsec "example.org/netsec/q0308"
)

const (
	localPriority = 0x8000
	rootPriority  = 0x1000
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func bridge(priority uint16, lastMACByte byte) netsec.BridgeID {
	return netsec.BridgeID{
		Priority: priority,
		MAC:      [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, lastMACByte},
	}
}

func port(priority, number uint16) netsec.PortID {
	return netsec.PortID{Priority: priority, Number: number}
}

func configBPDU(root netsec.BridgeID, cost uint32, sender netsec.BridgeID, senderPort netsec.PortID) netsec.BPDU {
	return netsec.BPDU{
		Type:           netsec.ConfigBPDU,
		RootID:         root,
		RootPathCost:   cost,
		SenderBridgeID: sender,
		SenderPortID:   senderPort,
		MessageAge:     time.Second,
		MaxAge:         20 * time.Second,
	}
}

func tcnBPDU() netsec.BPDU {
	return netsec.BPDU{
		Type:       netsec.TCNBPDU,
		MessageAge: time.Second,
		MaxAge:     20 * time.Second,
	}
}

func newGuard(clock *fakeClock, config netsec.GuardConfig) *netsec.Guard {
	config.LocalBridgeID = bridge(localPriority, 0x01)
	config.Clock = clock.Now
	return netsec.NewGuard(config)
}

func mustHandle(t *testing.T, guard *netsec.Guard, p netsec.PortID, bpdu netsec.BPDU) netsec.BPDUResult {
	t.Helper()
	result, err := guard.HandleBPDU(p, bpdu)
	if err != nil {
		t.Fatalf("HandleBPDU(%s): %v", p, err)
	}
	return result
}

func mustState(t *testing.T, guard *netsec.Guard, p netsec.PortID) netsec.PortState {
	t.Helper()
	state, err := guard.PortState(p)
	if err != nil {
		t.Fatalf("PortState(%s): %v", p, err)
	}
	return state
}

func TestPriorityVectorComparisonOrder(t *testing.T) {
	base := netsec.PriorityVector{
		RootID:         bridge(0x8000, 0x01),
		RootPathCost:   10,
		SenderBridgeID: bridge(0x8000, 0x02),
		SenderPortID:   port(0x80, 1),
	}

	tests := []struct {
		name string
		got  netsec.PriorityVector
		want int
	}{
		{
			name: "identical",
			got:  base,
			want: 0,
		},
		{
			name: "better root priority",
			got: netsec.PriorityVector{
				RootID:         bridge(0x7000, 0x01),
				RootPathCost:   base.RootPathCost,
				SenderBridgeID: base.SenderBridgeID,
				SenderPortID:   base.SenderPortID,
			},
			want: -1,
		},
		{
			name: "better root MAC",
			got: netsec.PriorityVector{
				RootID:         bridge(0x8000, 0x00),
				RootPathCost:   base.RootPathCost,
				SenderBridgeID: base.SenderBridgeID,
				SenderPortID:   base.SenderPortID,
			},
			want: -1,
		},
		{
			name: "better path cost",
			got: netsec.PriorityVector{
				RootID:         base.RootID,
				RootPathCost:   9,
				SenderBridgeID: base.SenderBridgeID,
				SenderPortID:   base.SenderPortID,
			},
			want: -1,
		},
		{
			name: "better sender bridge priority",
			got: netsec.PriorityVector{
				RootID:         base.RootID,
				RootPathCost:   base.RootPathCost,
				SenderBridgeID: bridge(0x7000, 0x02),
				SenderPortID:   base.SenderPortID,
			},
			want: -1,
		},
		{
			name: "better sender bridge MAC",
			got: netsec.PriorityVector{
				RootID:         base.RootID,
				RootPathCost:   base.RootPathCost,
				SenderBridgeID: bridge(0x8000, 0x01),
				SenderPortID:   base.SenderPortID,
			},
			want: -1,
		},
		{
			name: "better sender port priority",
			got: netsec.PriorityVector{
				RootID:         base.RootID,
				RootPathCost:   base.RootPathCost,
				SenderBridgeID: base.SenderBridgeID,
				SenderPortID:   port(0x70, 1),
			},
			want: -1,
		},
		{
			name: "better sender port number",
			got: netsec.PriorityVector{
				RootID:         base.RootID,
				RootPathCost:   base.RootPathCost,
				SenderBridgeID: base.SenderBridgeID,
				SenderPortID:   port(0x80, 0),
			},
			want: -1,
		},
		{
			name: "worse root dominates better remaining fields",
			got: netsec.PriorityVector{
				RootID:         bridge(0x9000, 0x01),
				RootPathCost:   0,
				SenderBridgeID: bridge(0x7000, 0x01),
				SenderPortID:   port(0x70, 0),
			},
			want: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.got.Compare(base); got != test.want {
				t.Fatalf("Compare() = %d, want %d", got, test.want)
			}
			if got := base.Compare(test.got); got != -test.want {
				t.Fatalf("reverse Compare() = %d, want %d", got, -test.want)
			}
		})
	}
}

func TestBridgeAndPortIDOrdering(t *testing.T) {
	if got := bridge(0x7000, 0xff).Compare(bridge(0x8000, 0x00)); got != -1 {
		t.Fatalf("lower bridge priority should win, got %d", got)
	}
	if got := bridge(0x8000, 0x01).Compare(bridge(0x8000, 0x02)); got != -1 {
		t.Fatalf("lower bridge MAC should win, got %d", got)
	}
	if got := port(0x70, 0xffff).Compare(port(0x80, 0x00)); got != -1 {
		t.Fatalf("lower port priority should win, got %d", got)
	}
	if got := port(0x80, 1).Compare(port(0x80, 2)); got != -1 {
		t.Fatalf("lower port number should win, got %d", got)
	}
	if got := port(0x80, 1).Compare(port(0x80, 1)); got != 0 {
		t.Fatalf("equal ports should compare equal, got %d", got)
	}
}

func TestForgedRootBlocksEdgePortUntilQuietPeriod(t *testing.T) {
	clock := newFakeClock()
	guard := newGuard(clock, netsec.GuardConfig{QuietPeriod: 30 * time.Second})
	edge := port(0x80, 1)
	guard.ConfigurePort(edge, netsec.PortConfig{Edge: true})

	forged := configBPDU(bridge(0x0000, 0xaa), 0, bridge(0x0000, 0xaa), port(0x80, 1))
	blockedAt := clock.Now()
	result := mustHandle(t, guard, edge, forged)

	if result.Accepted {
		t.Fatal("forged superior BPDU was accepted")
	}
	if result.Reason != netsec.DropSuperiorBPDU {
		t.Fatalf("drop reason = %s, want %s", result.Reason, netsec.DropSuperiorBPDU)
	}
	if result.Forwarding {
		t.Fatal("edge port remained forwarding")
	}
	if want := blockedAt.Add(30 * time.Second); !result.QuietUntil.Equal(want) {
		t.Fatalf("quiet until = %v, want %v", result.QuietUntil, want)
	}
	if state := mustState(t, guard, edge); state != netsec.PortNonForwarding {
		t.Fatalf("state = %s, want non-forwarding", state)
	}

	// A legitimate, inferior advertisement during the quiet period is
	// accepted for processing but must not re-enable the port early.
	clock.Advance(10 * time.Second)
	legit := configBPDU(bridge(0x9000, 0x10), 10, bridge(0x9000, 0x10), port(0x80, 1))
	result = mustHandle(t, guard, edge, legit)
	if !result.Accepted {
		t.Fatal("legitimate inferior BPDU was rejected")
	}
	if result.Forwarding {
		t.Fatal("legitimate BPDU ended the quiet period early")
	}

	clock.Advance(19 * time.Second)
	if state := mustState(t, guard, edge); state != netsec.PortNonForwarding {
		t.Fatalf("state before quiet expiry = %s, want non-forwarding", state)
	}
	clock.Advance(time.Second)
	if state := mustState(t, guard, edge); state != netsec.PortForwarding {
		t.Fatalf("state at quiet expiry = %s, want forwarding", state)
	}

	if got := guard.BestVector().RootID; got != bridge(localPriority, 0x01) {
		t.Fatalf("forged root was learned: %s", got)
	}
	if quietUntil, err := guard.QuietUntil(edge); err != nil || !quietUntil.IsZero() {
		t.Fatalf("QuietUntil after expiry = %v, %v", quietUntil, err)
	}
}

func TestRepeatedSuperiorBPDURestartsQuietPeriod(t *testing.T) {
	clock := newFakeClock()
	guard := newGuard(clock, netsec.GuardConfig{QuietPeriod: 20 * time.Second})
	edge := port(0x80, 1)
	guard.ConfigurePort(edge, netsec.PortConfig{Edge: true})
	forged := configBPDU(bridge(0x0000, 0xaa), 0, bridge(0x0000, 0xaa), port(0x80, 1))

	start := clock.Now()
	mustHandle(t, guard, edge, forged)
	clock.Advance(15 * time.Second)
	mustHandle(t, guard, edge, forged)

	quietUntil, err := guard.QuietUntil(edge)
	if err != nil {
		t.Fatal(err)
	}
	if want := start.Add(35 * time.Second); !quietUntil.Equal(want) {
		t.Fatalf("quiet until = %v, want %v", quietUntil, want)
	}

	clock.Advance(19 * time.Second)
	if state := mustState(t, guard, edge); state != netsec.PortNonForwarding {
		t.Fatalf("state = %s, want non-forwarding", state)
	}
	clock.Advance(time.Second)
	if state := mustState(t, guard, edge); state != netsec.PortForwarding {
		t.Fatalf("state = %s, want forwarding", state)
	}
}

func TestRootGuardBlocksVectorsSuperiorToCurrentRoot(t *testing.T) {
	clock := newFakeClock()
	guard := newGuard(clock, netsec.GuardConfig{QuietPeriod: 10 * time.Second})
	uplink := port(0x80, 1)
	protected := port(0x80, 2)
	guard.ConfigurePort(uplink, netsec.PortConfig{})
	guard.ConfigurePort(protected, netsec.PortConfig{RootGuard: true})

	trueRoot := bridge(rootPriority, 0x10)
	learned := configBPDU(trueRoot, 5, bridge(rootPriority, 0x11), port(0x80, 1))
	if result := mustHandle(t, guard, uplink, learned); !result.Accepted || !result.Forwarding {
		t.Fatalf("normal port did not accept real root: %+v", result)
	}
	if got := guard.BestVector().RootID; got != trueRoot {
		t.Fatalf("best root = %s, want %s", got, trueRoot)
	}

	// A lower root ID is always superior, regardless of path cost.
	forged := configBPDU(bridge(0x0100, 0xaa), 1000, bridge(0x0100, 0xaa), port(0x80, 1))
	result := mustHandle(t, guard, protected, forged)
	if result.Accepted || result.Reason != netsec.DropSuperiorBPDU || result.Forwarding {
		t.Fatalf("root-guard result = %+v, want dropped superior BPDU", result)
	}

	clock.Advance(10 * time.Second)
	if state := mustState(t, guard, protected); state != netsec.PortForwarding {
		t.Fatalf("state after quiet period = %s, want forwarding", state)
	}

	// Same root and worse cost is inferior and must be accepted.
	inferior := configBPDU(trueRoot, 6, bridge(0x2000, 0x22), port(0x80, 1))
	result = mustHandle(t, guard, protected, inferior)
	if !result.Accepted || !result.Forwarding {
		t.Fatalf("inferior BPDU result = %+v, want accepted", result)
	}

	// Same root with a lower cost is superior to the current root vector.
	betterCost := configBPDU(trueRoot, 4, bridge(0x2000, 0x22), port(0x80, 1))
	result = mustHandle(t, guard, protected, betterCost)
	if result.Accepted || result.Reason != netsec.DropSuperiorBPDU || result.Forwarding {
		t.Fatalf("better-cost result = %+v, want dropped superior BPDU", result)
	}
}

func TestNormalPortAcceptsAndLearnsSuperiorRoot(t *testing.T) {
	clock := newFakeClock()
	guard := newGuard(clock, netsec.GuardConfig{})
	upstream := port(0x80, 1)
	guard.ConfigurePort(upstream, netsec.PortConfig{})

	betterRoot := bridge(rootPriority, 0x10)
	result := mustHandle(t, guard, upstream, configBPDU(
		betterRoot, 4, bridge(rootPriority, 0x11), port(0x80, 1),
	))
	if !result.Accepted || !result.Forwarding {
		t.Fatalf("normal port result = %+v, want accepted", result)
	}
	if got := guard.BestVector(); got.RootID != betterRoot || got.RootPathCost != 4 {
		t.Fatalf("best vector = %+v", got)
	}
}

func TestEdgePortRejectsAForcedRootEvenAfterLearningRealRoot(t *testing.T) {
	clock := newFakeClock()
	guard := newGuard(clock, netsec.GuardConfig{QuietPeriod: 10 * time.Second})
	uplink := port(0x80, 1)
	edge := port(0x80, 2)
	guard.ConfigurePort(uplink, netsec.PortConfig{})
	guard.ConfigurePort(edge, netsec.PortConfig{Edge: true})

	// Learn a genuine root better than the local bridge from a normal
	// uplink.
	realRoot := bridge(0x1000, 0x10)
	mustHandle(t, guard, uplink, configBPDU(realRoot, 4, bridge(0x1000, 0x11), port(0x80, 1)))

	// This forged root is worse than the genuine root but still better
	// than the local bridge, so it must never be accepted on an edge port.
	forged := configBPDU(bridge(0x2000, 0xaa), 0, bridge(0x2000, 0xaa), port(0x80, 1))
	result := mustHandle(t, guard, edge, forged)
	if result.Accepted || result.Reason != netsec.DropSuperiorBPDU || result.Forwarding {
		t.Fatalf("edge result = %+v, want dropped superior BPDU", result)
	}
}

func TestLegitimateTopologyChangePasses(t *testing.T) {
	clock := newFakeClock()
	guard := newGuard(clock, netsec.GuardConfig{
		TCNRateLimit:    2,
		TCNRateInterval: time.Second,
	})
	trunk := port(0x80, 1)
	guard.ConfigurePort(trunk, netsec.PortConfig{})

	topologyChange := configBPDU(
		bridge(localPriority, 0x01),
		4,
		bridge(0x9000, 0x20),
		port(0x80, 4),
	)
	result := mustHandle(t, guard, trunk, topologyChange)
	if !result.Accepted || !result.Forwarding || result.Reason != netsec.DropNone {
		t.Fatalf("legitimate configuration change = %+v", result)
	}

	result = mustHandle(t, guard, trunk, tcnBPDU())
	if !result.Accepted || !result.Forwarding || result.Reason != netsec.DropNone {
		t.Fatalf("legitimate TCN = %+v", result)
	}
}

func TestAgedBPDUsAreDroppedBeforeGuardAndRateLimit(t *testing.T) {
	clock := newFakeClock()
	guard := newGuard(clock, netsec.GuardConfig{
		MaxAge:          20 * time.Second,
		TCNRateLimit:    1,
		TCNRateInterval: time.Hour,
	})
	trunk := port(0x80, 1)
	guard.ConfigurePort(trunk, netsec.PortConfig{})

	aged := configBPDU(bridge(0x0000, 0xaa), 0, bridge(0x0000, 0xaa), port(0x80, 1))
	aged.MessageAge = 20 * time.Second
	aged.MaxAge = 20 * time.Second

	result := mustHandle(t, guard, trunk, aged)
	if result.Accepted || result.Reason != netsec.DropAgedBPDU {
		t.Fatalf("aged BPDU result = %+v", result)
	}
	if !result.Forwarding {
		t.Fatal("aged BPDU should not block the port")
	}
	if got := guard.BestVector().RootID; got != bridge(localPriority, 0x01) {
		t.Fatalf("aged BPDU updated best vector: %s", got)
	}

	// An aged TCN must not consume the port's rate allowance.
	agedTCN := tcnBPDU()
	agedTCN.MessageAge = 20 * time.Second
	result = mustHandle(t, guard, trunk, agedTCN)
	if result.Accepted || result.Reason != netsec.DropAgedBPDU {
		t.Fatalf("aged TCN result = %+v", result)
	}
	result = mustHandle(t, guard, trunk, tcnBPDU())
	if !result.Accepted {
		t.Fatalf("valid TCN rejected after aged TCN: %+v", result)
	}

	// Zero MaxAge falls back to GuardConfig.MaxAge, and equality with the
	// configured maximum is still aged.
	withoutMaxAge := configBPDU(bridge(0x9000, 0x10), 5, bridge(0x9000, 0x10), port(0x80, 1))
	withoutMaxAge.MaxAge = 0
	withoutMaxAge.MessageAge = 20 * time.Second
	result = mustHandle(t, guard, trunk, withoutMaxAge)
	if result.Accepted || result.Reason != netsec.DropAgedBPDU {
		t.Fatalf("BPDU using default max age = %+v", result)
	}
}

func TestTCNFloodIsRateLimitedPerPort(t *testing.T) {
	clock := newFakeClock()
	guard := newGuard(clock, netsec.GuardConfig{
		TCNRateLimit:    2,
		TCNRateInterval: time.Second,
	})
	first := port(0x80, 1)
	second := port(0x80, 2)
	guard.ConfigurePort(first, netsec.PortConfig{})
	guard.ConfigurePort(second, netsec.PortConfig{})

	for i := 0; i < 5; i++ {
		result := mustHandle(t, guard, first, tcnBPDU())
		if i < 2 {
			if !result.Accepted || result.Reason != netsec.DropNone {
				t.Fatalf("TCN %d result = %+v, want accepted", i, result)
			}
			continue
		}
		if result.Accepted || result.Reason != netsec.DropTCNRateLimit {
			t.Fatalf("TCN %d result = %+v, want rate-limited", i, result)
		}
		if !result.Forwarding {
			t.Fatal("TCN rate limiting must not change forwarding state")
		}
	}

	// The limit is per port, not global.
	if result := mustHandle(t, guard, second, tcnBPDU()); !result.Accepted {
		t.Fatalf("second port TCN result = %+v, want accepted", result)
	}

	clock.Advance(time.Second - time.Nanosecond)
	if result := mustHandle(t, guard, first, tcnBPDU()); result.Accepted {
		t.Fatal("TCN accepted before the rate window ended")
	}
	clock.Advance(time.Nanosecond)
	if result := mustHandle(t, guard, first, tcnBPDU()); !result.Accepted {
		t.Fatalf("TCN rejected after the rate window ended: %+v", result)
	}
}

func TestConcurrentTCNFloodCannotExceedLimit(t *testing.T) {
	clock := newFakeClock()
	const limit = 8
	guard := newGuard(clock, netsec.GuardConfig{
		TCNRateLimit:    limit,
		TCNRateInterval: time.Second,
	})
	trunk := port(0x80, 1)
	guard.ConfigurePort(trunk, netsec.PortConfig{})

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := guard.HandleBPDU(trunk, tcnBPDU())
			if err != nil {
				t.Errorf("HandleBPDU: %v", err)
				return
			}
			if result.Accepted {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if accepted != limit {
		t.Fatalf("accepted %d TCNs, want %d", accepted, limit)
	}
}

func TestUnknownPortIsRejected(t *testing.T) {
	clock := newFakeClock()
	guard := newGuard(clock, netsec.GuardConfig{})

	if _, err := guard.HandleBPDU(port(0x80, 1), tcnBPDU()); !errors.Is(err, netsec.ErrUnknownPort) {
		t.Fatalf("HandleBPDU error = %v, want ErrUnknownPort", err)
	}
	if _, err := guard.PortState(port(0x80, 1)); !errors.Is(err, netsec.ErrUnknownPort) {
		t.Fatalf("PortState error = %v, want ErrUnknownPort", err)
	}
}

func TestUnsupportedBPDUTypeIsDropped(t *testing.T) {
	clock := newFakeClock()
	guard := newGuard(clock, netsec.GuardConfig{})
	trunk := port(0x80, 1)
	guard.ConfigurePort(trunk, netsec.PortConfig{})

	result := mustHandle(t, guard, trunk, netsec.BPDU{Type: 0x42})
	if result.Accepted || result.Reason != netsec.DropUnsupportedBPDU || !result.Forwarding {
		t.Fatalf("unsupported BPDU result = %+v", result)
	}
}
