package sim

import (
	"testing"
)

func newCluster(t *testing.T, n int, seed int64) (*Simulator, *System, []string) {
	t.Helper()
	s := New(n, seed)
	sys := NewSystem(s, n)
	cmds := makeCommands(12)
	return s, sys, cmds
}

func makeCommands(k int) []string {
	c := make([]string, k)
	for i := range c {
		c[i] = cmdName(i + 1)
	}
	return c
}

func cmdName(i int) string {
	return "cmd-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
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

func settle(s *Simulator, t *testing.T, checker *SafetyChecker, limit int64) {
	t.Helper()
	s.RunUntil(limit, 1)
	if !checker.Ok() {
		for _, v := range checker.Violations() {
			t.Errorf("safety violation t=%d: %s", v.Time, v.Msg)
		}
	}
}

// TestSlowFollowerBlocksRecycle: while all members are active, the recyclable
// prefix is the min of durable positions; one slow follower pins it to 0.
func TestSlowFollowerBlocksRecycle(t *testing.T) {
	s, sys, cmds := newCluster(t, 3, 1)
	check := sys.NewChecker(cmds)

	// Isolate follower 2 BEFORE any traffic; follower 1 stays fast.
	s.Partition(2)
	for _, c := range cmds[:8] {
		sys.Submit(c)
	}
	settle(s, t, check, 500)

	if got := sys.DurableSnap(leaderID); got != 0 {
		t.Fatalf("recyclable prefix must stay 0 with slow active follower, got %d", got)
	}
	if got := sys.DurableLast(1); got != 8 {
		t.Fatalf("fast follower durable last = %d, want 8", got)
	}
	if got := sys.DurableLast(2); got != 0 {
		t.Fatalf("partitioned follower must receive nothing, durable last = %d", got)
	}
}

// TestEvictUnblocksAndRecords: explicit removal records the snapshot re-entry
// requirement and only then frees recycling up to remaining active members.
func TestEvictUnblocksAndRecords(t *testing.T) {
	s, sys, cmds := newCluster(t, 3, 2)
	check := sys.NewChecker(cmds)

	s.Partition(2)
	for _, c := range cmds[:8] {
		sys.Submit(c)
	}
	settle(s, t, check, 300)
	if sys.DurableSnap(leaderID) != 0 {
		t.Fatalf("recycling before eviction must be blocked")
	}

	sys.Evict(2, s.Now())
	settle(s, t, check, 700)

	if got := sys.DurableSnap(leaderID); got != 8 {
		t.Fatalf("after eviction recyclable prefix = %d, want 8", got)
	}
	rec, ok := sys.EvictRecordFor(2)
	if !ok {
		t.Fatalf("missing eviction record")
	}
	if rec.RequiredSnap != rec.ReclaimPoint {
		t.Fatalf("eviction record inconsistent: %+v", rec)
	}
	// At the moment of explicit removal nothing was reclaimable yet; the
	// record pins the re-entry snapshot baseline (monotonic thereafter).
	if rec.RequiredSnap < 0 || rec.RequiredSnap > 8 {
		t.Fatalf("eviction record baseline out of range: %+v", rec)
	}
	if lo, _ := sys.DurableLogRange(leaderID); lo <= 8 {
		t.Fatalf("leader still physically holds prefix from %d", lo)
	}
}

// TestRejoinUsesSnapshotNoDeletedLog: a wiped re-entering replica catches up
// strictly through snapshots/its-new-tail; it is never asked for deleted logs.
func TestRejoinUsesSnapshotNoDeletedLog(t *testing.T) {
	s, sys, cmds := newCluster(t, 3, 3)
	check := sys.NewChecker(cmds)

	s.Partition(2)
	for _, c := range cmds[:8] {
		sys.Submit(c)
	}
	settle(s, t, check, 300)
	sys.Evict(2, s.Now())
	settle(s, t, check, 700)
	if sys.DurableSnap(leaderID) != 8 {
		t.Fatalf("setup: want compacted to 8")
	}

	// Fresh entries AFTER compaction, so re-entry also exercises tail sync.
	for _, c := range cmds[8:10] {
		sys.Submit(c)
	}
	settle(s, t, check, 900)

	s.Heal(2)
	sys.Readd(2, s.Now()) // wiped node, re-enters only via snapshot
	settle(s, t, check, 1600)

	// Snapshot install starts from the leader's current reclaim point, which
	// may already cover the tail; it must be >= the recorded baseline 8.
	if got := sys.DurableSnap(2); got < 8 {
		t.Fatalf("rejoined node snapshot = %d, want >= 8", got)
	}
	if got := sys.DurableLast(2); got != 10 {
		t.Fatalf("rejoined node durable last = %d, want 10", got)
	}
	if sys.DurableHash(2) != CanonicalHash(cmds, 10) {
		t.Fatal("rejoined node state hash diverges")
	}
	if sys.DurableHash(leaderID) != sys.DurableHash(1) {
		t.Fatal("leader/follower hash diverge")
	}
	// After promotion it becomes a full member: recycling can include it.
	if got := sys.DurableSnap(leaderID); got < 10 {
		t.Fatalf("after full re-entry expected reclaim to 10, got %d", got)
	}
}

// TestMemoryIsNotAcked: a follower crash before flush loses in-memory entries;
// the leader never recorded them as durable, and restart converges correctly.
func TestMemoryIsNotAcked(t *testing.T) {
	s, sys, cmds := newCluster(t, 3, 4)
	check := sys.NewChecker(cmds)

	for _, c := range cmds[:6] {
		sys.Submit(c)
	}
	// Let the append batch reach follower 1 (delivered at t=25) but crash it
	// before its flush timer fires (t=28): entries are only in volatile memory
	// and therefore provisional - never acknowledgeable.
	crashed := false
	s.Schedule(27, -1, "crash1")
	prevCB := s.OnCallback
	s.OnCallback = func(t int64, name string) {
		if name == "crash1" && !crashed {
			crashed = true
			sys.Crash(1)
			s.ScheduleRestart(30, 1, "restart1")
			return
		}
		if prevCB != nil {
			prevCB(t, name)
		}
	}
	prevTimer := s.OnTimer
	s.OnTimer = func(t int64, id int, name string) {
		if name == "restart1" {
			sys.Restart(1)
			return
		}
		prevTimer(t, id, name)
	}
	settle(s, t, check, 500)

	if got := sys.DurableLast(1); got != 6 {
		t.Fatalf("follower after restart durable = %d, want 6", got)
	}
	for _, v := range sys.MatchSeries[1] {
		if v > 6 {
			t.Fatalf("leader recorded impossible match %d", v)
		}
	}
}

// TestMonotonicConfirmations: acknowledged positions never move backwards.
func TestMonotonicConfirmations(t *testing.T) {
	s, sys, cmds := newCluster(t, 3, 5)
	check := sys.NewChecker(cmds)

	for i, c := range cmds {
		sys.Submit(c)
		s.RunUntil(int64(40+i*15), 1)
		if i == 4 {
			sys.Crash(1)
			s.RunUntil(int64(55+i*15), 1)
			sys.Restart(1)
		}
	}
	s.RunUntil(900, 1)
	if !check.Ok() {
		for _, v := range check.Violations() {
			t.Errorf("safety: %s", v.Msg)
		}
	}
	for f := 1; f < 3; f++ {
		series := sys.MatchSeries[f]
		for k := 1; k < len(series); k++ {
			if series[k] < series[k-1] {
				t.Fatalf("follower %d ack regressed: %v", f, series)
			}
		}
	}
	for k := 1; k < len(sys.ReclaimSeries); k++ {
		if sys.ReclaimSeries[k] < sys.ReclaimSeries[k-1] {
			t.Fatalf("reclaim series not monotonic: %v", sys.ReclaimSeries)
		}
	}
}

// TestLivenessUnderLoss: with bounded-delay links and bounded loss, every
// submitted entry is eventually durable on every active node, and recycling
// eventually reaches the last entry (all-nodes-ack rule).
func TestLivenessUnderLoss(t *testing.T) {
	s, sys, cmds := newCluster(t, 3, 11)
	check := sys.NewChecker(cmds)
	for i := 1; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if l := s.Link(i, j); l != nil {
				l.LossPct = 30
				l.Jitter = 6
			}
			if l := s.Link(j, i); l != nil {
				l.LossPct = 30
				l.Jitter = 6
			}
		}
	}
	for _, c := range cmds {
		sys.SubmitWithRetry(c)
	}
	deadline := int64(4000)
	s.RunUntil(deadline, 20)
	if !check.Ok() {
		for _, v := range check.Violations() {
			t.Errorf("safety: %s", v.Msg)
		}
	}
	for i := 0; i < 3; i++ {
		if got := sys.DurableLast(i); got != len(cmds) {
			t.Fatalf("liveness: node%d durable last %d, want %d", i, got, len(cmds))
		}
		if sys.DurableHash(i) != CanonicalHash(cmds, len(cmds)) {
			t.Fatalf("liveness: node%d hash divergence", i)
		}
	}
	if got := sys.DurableSnap(leaderID); got != len(cmds) {
		t.Fatalf("liveness: recyclable prefix %d, want %d", got, len(cmds))
	}
}

// TestChaosSafety runs deterministic crash/partition/evict/rejoin scenarios
// and asserts the safety invariants hold at every observed time. Liveness is
// asserted after the system is given a stable healing window.
func TestChaosSafety(t *testing.T) {
	for seed := int64(100); seed < 108; seed++ {
		t.Run("seed-"+itoa64(seed), func(t *testing.T) {
			s := New(4, seed)
			sys := NewSystem(s, 4)
			cmds := makeCommands(40)
			check := sys.NewChecker(cmds)

			// Random loss everywhere for realism; links always heal eventually.
			for i := 0; i < 4; i++ {
				for j := 0; j < 4; j++ {
					if l := s.Link(i, j); l != nil {
						l.LossPct = int(seed % 25)
						l.Jitter = 8
					}
				}
			}

			ci := 0
			// Pace submissions via periodic client timers through the chaos.
			s.Schedule(0, -1, "feed")
			prevCB := s.OnCallback
			s.OnCallback = func(tt int64, name string) {
				if name == "feed" {
					if ci < len(cmds) {
						sys.SubmitWithRetry(cmds[ci])
						ci++
					}
					s.Schedule(25, -1, "feed")
					return
				}
				if prevCB != nil {
					prevCB(tt, name)
				}
			}

			s.Schedule(150, 2, "crashf")
			s.ScheduleRestart(260, 2, "restartf")
			s.Schedule(320, 3, "partitionf")
			s.Schedule(500, 3, "evictf")
			s.Schedule(700, 3, "readdf")
			s.Schedule(200, 1, "crashf")
			s.ScheduleRestart(300, 1, "restartf")

			prevTimer := s.OnTimer
			s.OnTimer = func(tt int64, id int, name string) {
				switch name {
				case "crashf":
					sys.Crash(id)
				case "restartf":
					sys.Restart(id)
				case "partitionf":
					s.Partition(id)
				case "evictf":
					sys.Evict(id, tt)
				case "readdf":
					s.Heal(id)
					sys.Readd(id, tt)
				default:
					prevTimer(tt, id, name)
				}
			}

			s.RunUntil(800, 10)

			// Stable recovery window: heals all links, everyone alive.
			for i := 1; i < 4; i++ {
				s.Heal(i)
				if !s.Alive(i) {
					sys.Restart(i)
				}
			}
			s.RunUntil(3000, 20)

			if !check.Ok() {
				for _, v := range check.Violations() {
					t.Errorf("safety violation t=%d: %s", v.Time, v.Msg)
				}
				return
			}
			// Liveness in the stable tail.
			for i := 0; i < 4; i++ {
				if got := sys.DurableLast(i); got != len(cmds) {
					t.Fatalf("node%d durable last %d, want %d", i, got, len(cmds))
				}
				if sys.DurableHash(i) != CanonicalHash(cmds, len(cmds)) {
					t.Fatalf("node%d divergent hash", i)
				}
			}
			if got := sys.DurableSnap(leaderID); got != len(cmds) {
				t.Fatalf("final reclaim %d, want %d", got, len(cmds))
			}
		})
	}
}

func itoa64(i int64) string { return itoa(int(i)) }

// TestNeverAsksForDeletedLog installs a spy on delivery that fails the test if
// a follower ever receives an Append whose predecessor (or any entry index)
// falls into a snapshot-covered prefix. InstallSnapshot is the only legal way
// to cross the compaction boundary.
func TestNeverAsksForDeletedLog(t *testing.T) {
	s, sys, cmds := newCluster(t, 3, 7)
	check := sys.NewChecker(cmds)

	bad := 0
	orig := s.OnDeliver
	s.OnDeliver = func(tt int64, to int, m *Message) {
		if to != leaderID && m.Kind == TypeAppend {
			d := sys.dur[to]
			if m.PrevIndex < d.snap.LastIndex {
				bad++
			}
			for _, e := range m.Entries {
				if e.Index <= d.snap.LastIndex {
					bad++
				}
			}
		}
		orig(tt, to, m)
	}

	for _, c := range cmds {
		sys.Submit(c)
	}
	s.RunUntil(400, 5)
	s.Partition(2)
	s.RunUntil(500, 5)
	sys.Evict(2, s.Now())
	s.RunUntil(800, 5)
	s.Heal(2)
	sys.Readd(2, s.Now())
	s.RunUntil(1500, 5)
	if !check.Ok() {
		for _, v := range check.Violations() {
			t.Error(v.Msg)
		}
	}
	if bad != 0 {
		t.Fatalf("observed %d append messages touching deleted prefix", bad)
	}
}
