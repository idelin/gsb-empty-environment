package netsec

import (
	"testing"
	"time"
)

var (
	rootBridge   = BridgeID{Priority: 0x8000, MAC: [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}}
	rogueBridge  = BridgeID{Priority: 0x0000, MAC: [6]byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01}}
	accessBridge = BridgeID{Priority: 0x8000, MAC: [6]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}}
)

func configBPDU(root, sender BridgeID, cost uint32) BPDU {
	return BPDU{
		Type:         BPDUTypeConfig,
		RootID:       root,
		RootPathCost: cost,
		SenderID:     sender,
		PortID:       PortID{Priority: 0x80, Number: 1},
		MessageAge:   1,
		MaxAge:       20,
	}
}

func TestPriorityVectorOrdering(t *testing.T) {
	base := configBPDU(rootBridge, accessBridge, 4).Vector()

	lowerRoot := configBPDU(rogueBridge, accessBridge, 100).Vector()
	if !lowerRoot.Superior(base) {
		t.Fatal("lower root bridge ID must win regardless of cost")
	}

	lowerCost := configBPDU(rootBridge, accessBridge, 2).Vector()
	if !lowerCost.Superior(base) {
		t.Fatal("same root, lower path cost must win")
	}

	lowerSender := configBPDU(rootBridge, rootBridge, 4).Vector()
	if !lowerSender.Superior(base) {
		t.Fatal("same root and cost, lower sender bridge ID must win")
	}

	lowerPort := base
	lowerPort.PortID = PortID{Priority: 0x80, Number: 0}
	if !lowerPort.Superior(base) {
		t.Fatal("same root, cost and sender, lower port ID must win")
	}

	if base.Compare(base) != 0 {
		t.Fatal("identical vectors must compare equal")
	}
	if base.Superior(lowerRoot) {
		t.Fatal("inferior vector must not report superiority")
	}
}

func TestForgedRootOnEdgePort(t *testing.T) {
	g := NewGuard()
	start := time.Now()
	g.ConfigurePort("ge-0/0/1", PortConfig{Edge: true, SilentPeriod: 30 * time.Second})

	// Attacker plugs in and claims priority 0 to become root.
	attack := configBPDU(rogueBridge, rogueBridge, 0)
	if v := g.Process("ge-0/0/1", attack, start); v != DropGuarded {
		t.Fatalf("forged root BPDU on edge port: got %v, want %v", v, DropGuarded)
	}
	if g.Forwarding("ge-0/0/1", start) {
		t.Fatal("edge port must stop forwarding after guard violation")
	}

	// Repeated attacks during the silent period are dropped silently
	// and must not extend the hold-down.
	mid := start.Add(29 * time.Second)
	if v := g.Process("ge-0/0/1", attack, mid); v != DropSilenced {
		t.Fatalf("BPDU during silent period: got %v, want %v", v, DropSilenced)
	}
	if g.Forwarding("ge-0/0/1", mid) {
		t.Fatal("port must stay non-forwarding until silent period ends")
	}

	// After the silent period the port forwards again.
	end := start.Add(30 * time.Second)
	if !g.Forwarding("ge-0/0/1", end) {
		t.Fatal("port must resume forwarding once silent period expires")
	}
}

func TestForgedRootOnRootGuardPort(t *testing.T) {
	g := NewGuard()
	start := time.Now()
	g.ConfigurePort("ge-0/0/2", PortConfig{RootGuard: true, SilentPeriod: 10 * time.Second})

	// Legitimate designated neighbor establishes the baseline.
	legit := configBPDU(rootBridge, accessBridge, 4)
	if v := g.Process("ge-0/0/2", legit, start); v != Accept {
		t.Fatalf("baseline BPDU: got %v, want %v", v, Accept)
	}

	// Attacker behind the port advertises a better root.
	attack := configBPDU(rogueBridge, rogueBridge, 0)
	if v := g.Process("ge-0/0/2", attack, start.Add(time.Second)); v != DropGuarded {
		t.Fatalf("superior BPDU on root-guard port: got %v, want %v", v, DropGuarded)
	}
	if g.Forwarding("ge-0/0/2", start.Add(time.Second)) {
		t.Fatal("root-guard port must stop forwarding after violation")
	}

	// An inferior BPDU (worse cost, same root) is not an attack.
	inferior := configBPDU(rootBridge, accessBridge, 19)
	if v := g.Process("ge-0/0/2", inferior, start.Add(11*time.Second)); v != Accept {
		t.Fatalf("inferior BPDU after recovery: got %v, want %v", v, Accept)
	}
}

func TestTCNFloodRateLimited(t *testing.T) {
	g := NewGuard()
	start := time.Now()
	g.ConfigurePort("ge-0/0/3", PortConfig{TCNRate: 2, TCNBurst: 3})

	tcn := BPDU{Type: BPDUTypeTCN, MaxAge: 20}

	// Burst of 3 fits the bucket, the 4th is dropped.
	for i := 0; i < 3; i++ {
		if v := g.Process("ge-0/0/3", tcn, start); v != Accept {
			t.Fatalf("TCN %d within burst: got %v, want %v", i+1, v, Accept)
		}
	}
	if v := g.Process("ge-0/0/3", tcn, start); v != DropRateLimited {
		t.Fatalf("TCN beyond burst: got %v, want %v", v, DropRateLimited)
	}

	// Sustained flood stays dropped; one token refills after 500ms.
	flood := start.Add(100 * time.Millisecond)
	if v := g.Process("ge-0/0/3", tcn, flood); v != DropRateLimited {
		t.Fatalf("sustained flood: got %v, want %v", v, DropRateLimited)
	}
	later := start.Add(500 * time.Millisecond)
	if v := g.Process("ge-0/0/3", tcn, later); v != Accept {
		t.Fatalf("TCN after token refill: got %v, want %v", v, Accept)
	}
}

func TestLegitimateTopologyChange(t *testing.T) {
	g := NewGuard()
	start := time.Now()
	g.ConfigurePort("ge-0/0/4", PortConfig{TCNRate: 4, TCNBurst: 2})
	g.ConfigurePort("ge-0/0/5", PortConfig{RootGuard: true})

	// Ordinary config BPDUs on an unprotected port are accepted.
	legit := configBPDU(rootBridge, accessBridge, 4)
	if v := g.Process("ge-0/0/4", legit, start); v != Accept {
		t.Fatalf("legitimate config BPDU: got %v, want %v", v, Accept)
	}

	// A genuine topology change sends a TCN within the rate limit.
	tcn := BPDU{Type: BPDUTypeTCN, MaxAge: 20}
	if v := g.Process("ge-0/0/4", tcn, start); v != Accept {
		t.Fatalf("legitimate TCN: got %v, want %v", v, Accept)
	}

	// The new root information propagates on the root-guard port as
	// long as it is not superior to the established baseline.
	g.Process("ge-0/0/5", legit, start)
	sameRoot := configBPDU(rootBridge, accessBridge, 8)
	if v := g.Process("ge-0/0/5", sameRoot, start.Add(time.Second)); v != Accept {
		t.Fatalf("non-superior update on root-guard port: got %v, want %v", v, Accept)
	}

	// Port keeps forwarding throughout.
	if !g.Forwarding("ge-0/0/4", start) || !g.Forwarding("ge-0/0/5", start) {
		t.Fatal("legitimate traffic must not affect forwarding state")
	}
}

func TestStaleBPDUDropped(t *testing.T) {
	g := NewGuard()
	start := time.Now()
	g.ConfigurePort("ge-0/0/6", PortConfig{})

	stale := configBPDU(rootBridge, accessBridge, 4)
	stale.MessageAge = stale.MaxAge
	if v := g.Process("ge-0/0/6", stale, start); v != DropStale {
		t.Fatalf("MessageAge == MaxAge: got %v, want %v", v, DropStale)
	}

	stale.MessageAge = stale.MaxAge + 3
	if v := g.Process("ge-0/0/6", stale, start); v != DropStale {
		t.Fatalf("MessageAge > MaxAge: got %v, want %v", v, DropStale)
	}

	// Stale check also applies to unmanaged ports.
	if v := g.Process("ge-9/9/9", stale, start); v != DropStale {
		t.Fatalf("stale BPDU on unmanaged port: got %v, want %v", v, DropStale)
	}
}
