package cluster

import (
	"strings"
	"testing"

	"byzguard/trust"
)

func newAcceptanceCluster() *Cluster {
	return New(trust.DefaultConfig(), map[trust.NodeID]Behavior{
		"M":  Honest,
		"H1": Honest,
		"H2": Honest,
		"B":  ByzantineDropper,
		"T":  Honest,
	})
}

// Acceptance boundary: B refuses to relay PingReq; the monitor's trust
// score for B slides 100 -> 20 and the cluster declares B isolated.
func TestByzantineDropperIsEvicted(t *testing.T) {
	c := newAcceptanceCluster()

	wantScores := []float64{80, 60, 40, 20}
	for round, want := range wantScores {
		if acked := c.IndirectProbe("M", "B", "T"); acked {
			t.Fatalf("round %d: probe unexpectedly acked through Byzantine B", round+1)
		}
		if got := c.Node("M").Scorer().Score("B"); got != want {
			t.Fatalf("round %d: trust(B)=%.0f want %.0f", round+1, got, want)
		}
	}

	m := c.Node("M")
	if !m.Scorer().IsQuarantined("B") {
		t.Fatal("B should be quarantined in M's local view")
	}

	// Other honest nodes cut direct probing and declare the isolation.
	for _, id := range []trust.NodeID{"H1", "H2"} {
		if !c.Node(id).IsIsolated("B") {
			t.Fatalf("%s did not isolate B", id)
		}
		if got := c.Node(id).Scorer().Score("B"); got != 20 {
			t.Fatalf("%s trust(B)=%.0f, want pinned at 20", id, got)
		}
	}

	// Direct probes to B are blocked locally: none are sent anymore.
	if c.DirectProbe("M", "B") {
		t.Fatal("direct probe to isolated B should be suppressed")
	}
	if n := m.ProbeAttempts("B"); n != 0 {
		t.Fatalf("M sent %d direct probes to isolated B, want 0", n)
	}

	// PingReq via B is skipped; an honest relay still works.
	if acked := c.IndirectProbe("M", "B", "T"); acked {
		t.Fatal("probe via isolated B should be skipped, not acked")
	}
	if acked := c.IndirectProbe("M", "H1", "T"); !acked {
		t.Fatal("probe via honest H1 should be acked")
	}

	// The isolation was announced in the protocol log.
	joined := strings.Join(c.Logs, "\n")
	if !strings.Contains(joined, "QUARANTINE B") {
		t.Fatal("missing quarantine announcement in logs")
	}
	if !strings.Contains(joined, "declares B ISOLATED") {
		t.Fatal("missing isolation declaration in logs")
	}
}

func TestHonestRelayKeepsFullTrust(t *testing.T) {
	c := newAcceptanceCluster()
	for round := 0; round < 6; round++ {
		if acked := c.IndirectProbe("M", "H1", "T"); !acked {
			t.Fatalf("round %d: honest relay should ack", round+1)
		}
	}
	if got := c.Node("M").Scorer().Score("H1"); got != 100 {
		t.Fatalf("trust(H1)=%.0f, want capped at 100", got)
	}
	if c.Node("M").Scorer().IsQuarantined("H1") {
		t.Fatal("honest node must never be quarantined")
	}
}

func TestForgedAliveIsDetectedAndQuarantined(t *testing.T) {
	c := New(trust.DefaultConfig(), map[trust.NodeID]Behavior{
		"M":  Honest,
		"H1": Honest,
		"B":  ByzantineForger,
		"D":  Honest,
	})
	c.SetReachable("D", false) // D is dead; B will claim it is alive

	// Forgery penalty 25: 100 -> 75 -> 50 -> 25 -> 0 (quarantine at <= 20).
	want := []float64{75, 50, 25, 0}
	for round, w := range want {
		c.ReceiveGossip("M", "B")
		if got := c.Node("M").Scorer().Score("B"); got != w {
			t.Fatalf("round %d: trust(B)=%.0f want %.0f", round+1, got, w)
		}
	}
	if !c.Node("M").Scorer().IsQuarantined("B") {
		t.Fatal("forger B should be quarantined")
	}
	if !c.Node("H1").IsIsolated("B") {
		t.Fatal("H1 should isolate the forger after the announcement")
	}
}

func TestTruthfulGossipIsNotPunished(t *testing.T) {
	c := New(trust.DefaultConfig(), map[trust.NodeID]Behavior{
		"M":  Honest,
		"H1": Honest,
		"D":  Honest,
	})
	c.SetReachable("D", false)
	c.ReceiveGossip("M", "H1") // H1 truthfully reports D as not alive
	if got := c.Node("M").Scorer().Score("H1"); got != 100 {
		t.Fatalf("trust(H1)=%.0f, want 100 for truthful gossip", got)
	}
}
