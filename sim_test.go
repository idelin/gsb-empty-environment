package logrec

import (
	"math/rand"
	"testing"
)

func replicateN(s *Sim, n int) uint64 {
	var last uint64
	for i := 0; i < n; i++ {
		last = s.Append("cmd", 1, 1, 1)
	}
	return last
}

// Safety: a slow (not removed) replica blocks recycling at its persisted
// position even after a quorum confirms far beyond it.
func TestSlowReplicaBlocksRecycling(t *testing.T) {
	c := NewCluster("A", "B", "C")
	s := NewSim(c)
	replicateN(s, 5)
	s.Run()
	if c.Log.Base() != 5 {
		t.Fatalf("base = %d, want 5", c.Log.Base())
	}
	s.Crash("C")
	replicateN(s, 5) // entries 6..10 confirmed by quorum {A,B}
	s.Run()
	if c.Confirmed != 10 {
		t.Fatalf("confirmed = %d, want 10", c.Confirmed)
	}
	if c.Log.Base() != 5 {
		t.Fatalf("slow replica must block recycling: base = %d, want 5", c.Log.Base())
	}
	if len(s.Violations) != 0 {
		t.Fatalf("safety violations: %v", s.Violations)
	}
}

// Safety: entries received in memory but never persisted do not confirm.
func TestInMemoryReceivedIsNotConfirmed(t *testing.T) {
	c := NewCluster("A", "B", "C")
	s := NewSim(c)
	s.Append("cmd", 1, 10, 1) // delivered at t=1, would persist at t=11
	s.After(5, "crash-all", func() { s.Crash("A"); s.Crash("B"); s.Crash("C") })
	s.Run()
	for _, id := range c.IDs() {
		r := c.Replica(id)
		if r.Received != 1 {
			t.Fatalf("replica %s received = %d, want 1 (in memory)", id, r.Received)
		}
		if r.Persisted != 0 {
			t.Fatalf("replica %s persisted = %d, want 0", id, r.Persisted)
		}
	}
	if c.Confirmed != 0 || c.Log.Base() != 0 {
		t.Fatalf("in-memory receipt confirmed: confirmed=%d base=%d", c.Confirmed, c.Log.Base())
	}
	if len(s.Violations) != 0 {
		t.Fatalf("safety violations: %v", s.Violations)
	}
}

// Safety: removing an offline replica unblocks recycling and records the
// snapshot re-entry requirement; rejoin installs a snapshot and never
// requires deleted log entries.
func TestRemovalSnapshotRejoin(t *testing.T) {
	c := NewCluster("A", "B", "C")
	s := NewSim(c)
	replicateN(s, 5)
	s.Run()
	s.Crash("C")
	replicateN(s, 5)
	s.Run()
	if c.Log.Base() != 5 {
		t.Fatalf("base = %d, want 5 (blocked by C)", c.Log.Base())
	}

	if err := s.Remove("C"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	s.Run()
	if c.Log.Base() != 10 {
		t.Fatalf("removal must unblock recycling: base = %d, want 10", c.Log.Base())
	}
	if len(c.Removals) != 1 || c.Removals[0].ReplicaID != "C" || c.Removals[0].RemovedAt != 10 {
		t.Fatalf("snapshot re-entry requirement not recorded: %+v", c.Removals)
	}

	if err := s.Rejoin("C"); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	s.Run()
	if len(s.Snapshots) != 1 || s.Snapshots[0].Index != 10 {
		t.Fatalf("expected snapshot install at 10, got %+v", s.Snapshots)
	}
	if s.Snapshots[0].Index < c.Log.Base() {
		t.Fatalf("rejoin required deleted log: snapshot %d < base %d", s.Snapshots[0].Index, c.Log.Base())
	}
	if got := c.Replica("C").Persisted; got != 10 {
		t.Fatalf("C persisted = %d after snapshot, want 10", got)
	}
	if len(s.Violations) != 0 {
		t.Fatalf("safety violations: %v", s.Violations)
	}
}

// Liveness: when every member persists everything, the recycled prefix
// reaches the last appended index.
func TestLivenessEventuallyRecycles(t *testing.T) {
	c := NewCluster("A", "B", "C")
	s := NewSim(c)
	last := replicateN(s, 20)
	s.Run()
	if c.Confirmed != last {
		t.Fatalf("liveness: confirmed = %d, want %d", c.Confirmed, last)
	}
	if c.Log.Base() != last {
		t.Fatalf("liveness: recycled prefix = %d, want %d", c.Log.Base(), last)
	}
	if len(s.Violations) != 0 {
		t.Fatalf("safety violations: %v", s.Violations)
	}
}

// Safety (randomized): monotonic confirmation, monotonic prefix, and
// recycled <= persisted for every member, across crashes, recoveries,
// removals and rejoins. Deterministic seed.
func TestRandomizedSafetyAndMonotonicity(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	c := NewCluster("A", "B", "C", "D", "E")
	s := NewSim(c)
	ids := c.IDs()
	removed := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := ids[rng.Intn(len(ids))]
		switch rng.Intn(6) {
		case 0, 1, 2:
			s.Append("cmd", rng.Intn(3), rng.Intn(3), rng.Intn(2))
		case 3:
			s.Crash(id)
		case 4:
			if removed[id] {
				if err := s.Rejoin(id); err != nil {
					t.Fatalf("rejoin %s: %v", id, err)
				}
				delete(removed, id)
			} else {
				s.Recover(id)
			}
		case 5:
			if r := c.Replica(id); !r.Online && !r.Removed {
				if err := s.Remove(id); err == nil {
					removed[id] = true
				}
			}
		}
		s.Run()
	}
	if len(s.Violations) != 0 {
		t.Fatalf("safety violations (%d): %v", len(s.Violations), s.Violations[:min(3, len(s.Violations))])
	}
	// Every snapshot install covered the recycled prefix at rejoin time.
	for _, snap := range s.Snapshots {
		if snap.Index > c.Confirmed {
			t.Fatalf("snapshot %+v beyond confirmed %d", snap, c.Confirmed)
		}
	}
}
