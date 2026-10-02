package swimtrust

import (
	"strings"
	"testing"
)

// Acceptance scenario: a Byzantine silent node accepts PingReqs but never
// relays them. Observers record consecutive violations, its trust score
// slides 100 -> 20, and peers cut direct probing and declare isolation.
func TestSilentNodeEvictedAfterTrustDecay(t *testing.T) {
	c := NewCluster()
	c.AddNode("A", BehaviorHonest, true)
	c.AddNode("B", BehaviorSilentRelay, true) // Byzantine silent node
	c.AddNode("C", BehaviorHonest, true)
	c.AddNode("D", BehaviorHonest, true)
	// T is up but partitioned from direct probes: forces the PingReq path.
	c.AddNode("T", BehaviorHonest, false)

	wantScores := []int{90, 80, 70, 60, 50, 40, 30, 20}
	for _, want := range wantScores {
		out := c.Probe("A", "T")
		if !out.Reached || !out.ViaIndirect {
			t.Fatalf("round %d: T should be reached indirectly, got %+v", c.Round, out)
		}
		if got := c.Node("A").Scorer.Score("B"); got != want {
			t.Fatalf("round %d: B score = %d, want %d", c.Round, got, want)
		}
		c.Tick()
	}

	if !c.Node("A").Scorer.IsQuarantined("B") {
		t.Fatal("A should have quarantined B at score 20")
	}

	// After quarantine A must not probe B directly anymore.
	out := c.Probe("A", "B")
	if out.DirectProbed {
		t.Fatal("A still direct-probes quarantined B")
	}

	// C and D independently observe B's silence and declare isolation.
	for _, obs := range []string{"C", "D"} {
		for i := 0; i < 8; i++ {
			c.Probe(obs, "T")
			c.Tick()
		}
	}
	ann := c.QuarantineAnnouncements("B")
	if len(ann) != 3 {
		t.Fatalf("want 3 isolation announcements for B, got %v", ann)
	}
	joined := strings.Join(ann, "\n")
	for _, obs := range []string{"A", "C", "D"} {
		if !strings.Contains(joined, obs+" declares B ISOLATED") {
			t.Errorf("missing isolation declaration from %s", obs)
		}
	}
}

// A node forging Alive status for a dead peer is penalized harder (-20)
// and quarantined after 5 confirmed forgeries (100 -> 0... crosses 20).
func TestForgeAliveDetectedAndQuarantined(t *testing.T) {
	c := NewCluster()
	c.AddNode("A", BehaviorHonest, true)
	c.AddNode("F", BehaviorForgeAlive, true)
	c.AddNode("C", BehaviorHonest, true)
	c.AddNode("D", BehaviorHonest, true)
	c.AddNode("T", BehaviorHonest, false)
	c.Crash("T") // ground truth: T is dead

	wantScores := []int{80, 60, 40, 20}
	for _, want := range wantScores {
		out := c.Probe("A", "T")
		if out.Reached {
			t.Fatalf("round %d: dead T must not be reported reached", c.Round)
		}
		if got := c.Node("A").Scorer.Score("F"); got != want {
			t.Fatalf("round %d: F score = %d, want %d", c.Round, got, want)
		}
		c.Tick()
	}
	if !c.Node("A").Scorer.IsQuarantined("F") {
		t.Fatal("A should have quarantined forger F")
	}
}

// Honest helpers never lose trust and are never quarantined.
func TestHonestNodesKeepFullTrust(t *testing.T) {
	c := NewCluster()
	c.AddNode("A", BehaviorHonest, true)
	c.AddNode("C", BehaviorHonest, true)
	c.AddNode("D", BehaviorHonest, true)
	c.AddNode("T", BehaviorHonest, false)

	for i := 0; i < 20; i++ {
		c.Probe("A", "T")
		c.Tick()
	}
	for _, id := range []string{"C", "D"} {
		if got := c.Node("A").Scorer.Score(id); got != InitialScore {
			t.Errorf("honest %s score = %d, want %d", id, got, InitialScore)
		}
		if c.Node("A").Scorer.IsQuarantined(id) {
			t.Errorf("honest %s must never be quarantined", id)
		}
	}
}

// Unit-level scorer checks: penalties, threshold boundary, floor, and
// exactly-once quarantine callback.
func TestTrustScorerBoundaries(t *testing.T) {
	quarantined := 0
	s := NewTrustScorer("self")
	s.OnQuarantine = func(string, int) { quarantined++ }

	s.Record("X", ViolationRefusePingReq, 1, "") // 90
	s.Record("X", ViolationForgeAlive, 2, "")    // 70
	if got := s.Score("X"); got != 70 {
		t.Fatalf("score = %d, want 70", got)
	}
	if s.IsQuarantined("X") {
		t.Fatal("70 must not quarantine")
	}

	// Drive to exactly the threshold: 70 -> 20 is 5 refusals.
	for i := 0; i < 5; i++ {
		s.Record("X", ViolationRefusePingReq, 3+i, "")
	}
	if got := s.Score("X"); got != QuarantineThreshold {
		t.Fatalf("score = %d, want %d", got, QuarantineThreshold)
	}
	if !s.IsQuarantined("X") || quarantined != 1 {
		t.Fatalf("quarantine at threshold: flag=%v calls=%d", s.IsQuarantined("X"), quarantined)
	}

	// Further violations keep score >= 0 and never re-fire the callback.
	for i := 0; i < 10; i++ {
		s.Record("X", ViolationForgeAlive, 10+i, "")
	}
	if got := s.Score("X"); got != MinScore {
		t.Fatalf("score = %d, want floor %d", got, MinScore)
	}
	if quarantined != 1 {
		t.Fatalf("quarantine callback fired %d times, want 1", quarantined)
	}
	if got := s.Score("unseen"); got != InitialScore {
		t.Fatalf("unseen peer score = %d, want %d", got, InitialScore)
	}
}
