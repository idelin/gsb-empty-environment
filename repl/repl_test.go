package repl

import (
	"reflect"
	"testing"

	"snaprepl/sim"
)

// scenario wires a leader and a follower through a seeded simulator.
type scenario struct {
	eng      *sim.Engine
	net      *sim.Net
	leader   *Leader
	follower *Follower
	checker  *Checker
}

func newScenario(seed int64) *scenario {
	eng := sim.NewEngine()
	net := sim.NewNet(eng, seed)
	net.MinDelay, net.MaxDelay = 1, 6
	l := NewLeader("L", net, 16)
	f := NewFollower("F", net)
	l.Peers = []string{"F"}
	c := NewChecker(l, f)
	eng.OnStep = c.Check
	return &scenario{eng: eng, net: net, leader: l, follower: f, checker: c}
}

func (s *scenario) proposeN(from, to int) {
	for i := from; i <= to; i++ {
		s.leader.Propose(key(i), val(i))
	}
}

func key(i int) string { return "k" + itoa(i) }
func val(i int) string { return "v" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [8]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}

// TestSafetyPrefixInvariantUnderChaos: chunks and log entries interleave
// with loss, duplication and reordering. The prefix invariant must hold
// after EVERY event (safety), and periodic retransmission must
// eventually catch the follower up (liveness).
func TestSafetyPrefixInvariantUnderChaos(t *testing.T) {
	s := newScenario(7)
	s.net.DropProb = 0.2
	s.net.DupProb = 0.2

	// Follower is partitioned for the first 10 entries, then heals.
	s.net.Filter = func(from, to string, msg interface{}) (interface{}, bool) {
		return msg, to == "F"
	}
	s.proposeN(1, 10)
	s.net.Filter = nil

	// Snapshot of 1..8 plus live entries 11..20, all in flight together.
	s.leader.SendSnapshot("F", 8)
	s.proposeN(11, 20)

	// Retransmission loop: until caught up, leader periodically re-sends
	// the snapshot and the tail of the log.
	var heartbeat func()
	heartbeat = func() {
		if s.follower.LastApplied() < s.leader.CommitIndex() {
			ci := s.leader.CommitIndex()
			s.leader.SendSnapshot("F", ci-2)
			for i := ci - 1; i <= ci; i++ {
				s.net.Send("L", "F", "retry", s.leader.Log[i-1])
			}
			s.eng.After(30, "heartbeat", heartbeat)
		}
	}
	s.eng.After(30, "heartbeat", heartbeat)
	s.eng.Run()

	if !s.checker.OK() {
		t.Fatalf("SAFETY violated: %v", s.checker.Violations)
	}
	if s.follower.LastApplied() != s.leader.CommitIndex() {
		t.Fatalf("LIVENESS violated: lastApplied=%d, commit=%d",
			s.follower.LastApplied(), s.leader.CommitIndex())
	}
	if s.follower.Stats.SnapshotsInstalled == 0 {
		t.Fatal("expected at least one snapshot install")
	}
	t.Logf("SAFETY: %d invariant checks, 0 violations", s.checker.Checks)
	t.Logf("LIVENESS: caught up at t=%d, stats: %s", s.eng.Now(), s.follower.Stats)
}

// TestMissingChunkBlocksPublish: with one chunk permanently withheld the
// snapshot must never be published and the old state must stay intact.
func TestMissingChunkBlocksPublish(t *testing.T) {
	s := newScenario(1)
	s.proposeN(1, 5)
	s.eng.Run() // follower applies 1..5 directly

	before, laBefore, _ := s.follower.ReadAll()

	// Withhold chunk 2 of the next snapshot.
	s.net.Filter = func(from, to string, msg interface{}) (interface{}, bool) {
		if c, ok := msg.(Chunk); ok && c.Offset == 2 {
			return msg, true
		}
		return msg, false
	}
	s.proposeN(6, 12)
	meta := s.leader.SendSnapshot("F", 10)
	s.eng.Run()

	if s.follower.Stats.SnapshotsInstalled != 0 {
		t.Fatal("snapshot published despite missing chunk")
	}
	got, la, _ := s.follower.ReadAll()
	if la != laBefore || !reflect.DeepEqual(got, before) {
		t.Fatalf("old state disturbed during blocked install: la=%d", la)
	}
	if !s.follower.Installing() {
		t.Fatal("expected install to be in progress (waiting for chunk 2)")
	}

	// Release the missing chunk: install must complete.
	s.net.Filter = nil
	for _, c := range split(EncodeState(s.leader.StateAt(10)), s.leader.ChunkSize) {
		if c != nil {
			// re-send only chunk 2
		}
	}
	data := EncodeState(s.leader.StateAt(10))
	chunks := split(data, s.leader.ChunkSize)
	s.net.Send("L", "F", "chunk2", Chunk{Meta: meta, Offset: 2, Data: chunks[2]})
	s.eng.Run()

	if s.follower.Stats.SnapshotsInstalled != 1 {
		t.Fatal("snapshot not installed after missing chunk arrived")
	}
	// Snapshot@10 published, then buffered entries 11,12 drain on top.
	if s.follower.LastApplied() != 12 {
		t.Fatalf("lastApplied=%d, want 12", s.follower.LastApplied())
	}
	if !s.checker.OK() {
		t.Fatalf("SAFETY violated: %v", s.checker.Violations)
	}
}

// TestDigestMismatchRollback: a corrupted chunk must cause a rollback
// that leaves the applied state byte-for-byte untouched; a clean retry
// must then succeed.
func TestDigestMismatchRollback(t *testing.T) {
	s := newScenario(2)
	s.proposeN(1, 4)
	s.eng.Run()
	before, _, _ := s.follower.ReadAll()

	s.proposeN(5, 9)
	corrupt := true
	s.net.Filter = func(from, to string, msg interface{}) (interface{}, bool) {
		if c, ok := msg.(Chunk); ok && corrupt && c.Offset == 0 {
			corrupt = false
			bad := append([]byte(nil), c.Data...)
			bad[0] ^= 0xff
			c.Data = bad
			return c, false
		}
		return msg, false
	}
	s.leader.SendSnapshot("F", 9)
	s.eng.Run()

	if s.follower.Stats.Rollbacks != 1 {
		t.Fatalf("rollbacks=%d, want 1", s.follower.Stats.Rollbacks)
	}
	got, la, _ := s.follower.ReadAll()
	if la != 4 || !reflect.DeepEqual(got, before) {
		t.Fatalf("rollback touched applied state: la=%d state=%v", la, got)
	}
	if s.follower.Installing() {
		t.Fatal("staging not discarded after rollback")
	}

	// Clean retry succeeds.
	s.net.Filter = nil
	s.leader.SendSnapshot("F", 9)
	s.eng.Run()
	if s.follower.LastApplied() != 9 || s.follower.Stats.SnapshotsInstalled != 1 {
		t.Fatalf("retry failed: la=%d installed=%d",
			s.follower.LastApplied(), s.follower.Stats.SnapshotsInstalled)
	}
	if !s.checker.OK() {
		t.Fatalf("SAFETY violated: %v", s.checker.Violations)
	}
	t.Logf("rollback evidence: rollbacks=%d, state preserved across rollback", s.follower.Stats.Rollbacks)
}

// TestSkipSnapshotCoveredEntries: after install, log entries at or below
// the snapshot's LastIndex must be skipped, never re-applied.
func TestSkipSnapshotCoveredEntries(t *testing.T) {
	s := newScenario(3)
	s.proposeN(1, 6)
	s.leader.SendSnapshot("F", 6)
	s.eng.Run()
	if s.follower.LastApplied() != 6 {
		t.Fatalf("la=%d, want 6", s.follower.LastApplied())
	}
	appliedBefore := s.follower.Stats.Applied

	// Replay entries 1..6 (e.g. delayed duplicates) plus new entry 7.
	for i := 1; i <= 6; i++ {
		s.net.Send("L", "F", "stale", s.leader.Log[i-1])
	}
	s.leader.Propose("k7", "v7")
	s.eng.Run()

	if s.follower.Stats.Applied != appliedBefore+1 {
		t.Fatalf("covered entries were re-applied: applied=%d, want %d",
			s.follower.Stats.Applied, appliedBefore+1)
	}
	if s.follower.Stats.DuplicatesSkipped < 6 {
		t.Fatalf("duplicates not counted: %d", s.follower.Stats.DuplicatesSkipped)
	}
	if s.follower.LastApplied() != 7 {
		t.Fatalf("la=%d, want 7", s.follower.LastApplied())
	}
	if !s.checker.OK() {
		t.Fatalf("SAFETY violated: %v", s.checker.Violations)
	}
}

// TestNoDoubleApplyInterleaved: duplicated chunks and entries arriving
// interleaved must each take effect exactly once.
func TestNoDoubleApplyInterleaved(t *testing.T) {
	s := newScenario(4)
	s.net.DupProb = 0.5 // heavy duplication
	s.proposeN(1, 8)
	s.leader.SendSnapshot("F", 5)
	s.eng.Run()

	if s.follower.LastApplied() != 8 {
		t.Fatalf("la=%d, want 8", s.follower.LastApplied())
	}
	// Exactly entries 6,7,8 applied on top of the snapshot: 3, no more.
	if s.follower.Stats.Applied != 3 {
		t.Fatalf("applied=%d, want exactly 3 (double apply!)", s.follower.Stats.Applied)
	}
	if s.follower.Stats.ChunkDuplicates == 0 && s.follower.Stats.DuplicatesSkipped == 0 {
		t.Fatal("expected duplicates to have occurred and been skipped")
	}
	if !s.checker.OK() {
		t.Fatalf("SAFETY violated: %v", s.checker.Violations)
	}
	t.Logf("dedup evidence: %s", s.follower.Stats)
}

// TestCrashRestartRecovery: a crash mid-install loses volatile staging
// but keeps durable state; retransmission completes the install.
func TestCrashRestartRecovery(t *testing.T) {
	s := newScenario(5)
	s.proposeN(1, 3)
	s.eng.Run()
	before, _, _ := s.follower.ReadAll()

	// Deliver only part of the snapshot, then crash. Entries 4..10 are
	// dropped so the follower cannot catch up via the log alone.
	delivered := 0
	s.net.Filter = func(from, to string, msg interface{}) (interface{}, bool) {
		if _, ok := msg.(Chunk); ok {
			delivered++
			return msg, delivered > 1 // keep only the first chunk
		}
		return msg, true // drop log entries
	}
	s.proposeN(4, 10)
	s.leader.SendSnapshot("F", 10)
	s.eng.Run()
	if !s.follower.Installing() {
		t.Fatal("expected partial install in progress")
	}

	s.follower.Crash()
	if s.follower.Installing() {
		t.Fatal("staging survived crash")
	}
	got, la, _ := s.follower.ReadAll()
	if la != 3 || !reflect.DeepEqual(got, before) {
		t.Fatal("durable state lost across crash")
	}

	s.net.Filter = nil
	s.leader.SendSnapshot("F", 10)
	s.eng.Run()
	if s.follower.LastApplied() != 10 {
		t.Fatalf("la=%d, want 10 after recovery", s.follower.LastApplied())
	}
	if !s.checker.OK() {
		t.Fatalf("SAFETY violated: %v", s.checker.Violations)
	}
}

// TestLivenessBoundedCatchUp: under bounded loss with retransmission the
// follower must catch up within a bounded virtual time.
func TestLivenessBoundedCatchUp(t *testing.T) {
	s := newScenario(9)
	s.net.DropProb = 0.3
	s.proposeN(1, 15)
	s.leader.SendSnapshot("F", 15)

	const bound = 500
	var heartbeat func()
	heartbeat = func() {
		if s.follower.LastApplied() >= s.leader.CommitIndex() {
			return
		}
		s.leader.SendSnapshot("F", s.leader.CommitIndex())
		s.eng.After(25, "heartbeat", heartbeat)
	}
	s.eng.After(25, "heartbeat", heartbeat)
	s.eng.Run()

	if s.follower.LastApplied() != s.leader.CommitIndex() {
		t.Fatalf("LIVENESS violated: la=%d commit=%d", s.follower.LastApplied(), s.leader.CommitIndex())
	}
	if s.eng.Now() > bound {
		t.Fatalf("catch-up took %d ticks, bound %d", s.eng.Now(), bound)
	}
	if !s.checker.OK() {
		t.Fatalf("SAFETY violated: %v", s.checker.Violations)
	}
	t.Logf("LIVENESS: caught up at t=%d (bound %d), rollbacks=%d",
		s.eng.Now(), bound, s.follower.Stats.Rollbacks)
}
