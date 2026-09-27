package reordersim

import "testing"

func drain(ch <-chan Packet) []Packet {
	var out []Packet
	for {
		select {
		case p := <-ch:
			out = append(out, p)
		default:
			return out
		}
	}
}

func seqs(ps []Packet) []uint64 {
	out := make([]uint64, len(ps))
	for i, p := range ps {
		out[i] = p.Seq
	}
	return out
}

func equalSeqs(got []uint64, want ...uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func pkt(seq uint64, eventTime int64) Packet {
	return Packet{Epoch: 0, Seq: seq, EventTime: eventTime}
}

func TestInOrderReleasePassesThrough(t *testing.T) {
	r := NewReleaser(ReleaserConfig{WaitLimit: 10, CacheLimit: 8})
	r.OnTick(0)
	r.Receive(pkt(0, 100))
	r.Receive(pkt(1, 101))
	r.Receive(pkt(2, 102))
	if got := seqs(drain(r.Released())); !equalSeqs(got, 0, 1, 2) {
		t.Fatalf("released %v, want [0 1 2]", got)
	}
	if r.Buffered() != 0 {
		t.Fatalf("buffered = %d, want 0", r.Buffered())
	}
}

func TestOutOfOrderReleasedByEventTimeThenSeq(t *testing.T) {
	r := NewReleaser(ReleaserConfig{WaitLimit: 10, CacheLimit: 8})
	r.OnTick(0)
	r.Receive(pkt(1, 100)) // contiguous with 0 once 0 arrives, but later event time
	r.Receive(pkt(2, 50))  // smaller event time than seq 1
	if n := len(drain(r.Released())); n != 0 {
		t.Fatalf("released %d packets before seq 0 arrived, want 0", n)
	}
	r.Receive(pkt(0, 10))
	// Contiguous prefix {0,1,2} released by (EventTime, Seq): 0,2,1.
	if got := seqs(drain(r.Released())); !equalSeqs(got, 0, 2, 1) {
		t.Fatalf("released %v, want [0 2 1]", got)
	}
}

func TestSameEventTimeBrokenBySeq(t *testing.T) {
	r := NewReleaser(ReleaserConfig{WaitLimit: 10, CacheLimit: 8})
	r.OnTick(0)
	r.Receive(pkt(2, 50))
	r.Receive(pkt(1, 50)) // same event time as seq 2
	r.Receive(pkt(0, 10))
	if got := seqs(drain(r.Released())); !equalSeqs(got, 0, 1, 2) {
		t.Fatalf("released %v, want [0 1 2]", got)
	}
}

func TestMissingSeqSkippedAfterWaitLimit(t *testing.T) {
	r := NewReleaser(ReleaserConfig{WaitLimit: 3, CacheLimit: 8})
	r.OnTick(0)
	r.Receive(pkt(1, 10)) // gap at seq 0 opens at tick 0
	for tick := int64(1); tick <= 2; tick++ {
		r.OnTick(tick)
		if n := len(drain(r.Released())); n != 0 {
			t.Fatalf("tick %d: released %d, want 0 (still waiting)", tick, n)
		}
	}
	r.OnTick(3) // wait limit hit: skip seq 0, release seq 1
	if got := seqs(drain(r.Released())); !equalSeqs(got, 1) {
		t.Fatalf("released %v, want [1]", got)
	}
	if r.Stats().Skipped != 1 {
		t.Fatalf("skipped = %d, want 1", r.Stats().Skipped)
	}
	// The missing packet finally arrives: it is late, not released.
	r.OnTick(4)
	r.Receive(pkt(0, 5))
	if n := len(drain(r.Released())); n != 0 {
		t.Fatalf("late packet modified the released sequence")
	}
	if got := seqs(drain(r.Late())); !equalSeqs(got, 0) {
		t.Fatalf("late %v, want [0]", got)
	}
}

func TestCacheLimitForcesProgress(t *testing.T) {
	r := NewReleaser(ReleaserConfig{WaitLimit: 100, CacheLimit: 2})
	r.OnTick(0)
	r.Receive(pkt(1, 10))
	r.Receive(pkt(2, 20))
	if n := len(drain(r.Released())); n != 0 {
		t.Fatalf("released before cache limit, want 0")
	}
	r.Receive(pkt(3, 30)) // buffer would exceed 2: skip gap, release 1,2,3
	if got := seqs(drain(r.Released())); !equalSeqs(got, 1, 2, 3) {
		t.Fatalf("released %v, want [1 2 3]", got)
	}
	if r.Buffered() != 0 {
		t.Fatalf("buffered = %d, want 0", r.Buffered())
	}
	r.Receive(pkt(0, 5)) // skipped position: late
	if got := seqs(drain(r.Late())); !equalSeqs(got, 0) {
		t.Fatalf("late %v, want [0]", got)
	}
}

func TestLatePacketNeverModifiesReleasedSequence(t *testing.T) {
	r := NewReleaser(ReleaserConfig{WaitLimit: 2, CacheLimit: 8})
	r.OnTick(0)
	r.Receive(pkt(0, 10))
	r.Receive(pkt(2, 20))
	r.OnTick(2) // skip seq 1, release seq 2
	before := drain(r.Released())
	r.OnTick(3)
	r.Receive(pkt(1, 15)) // late: skipped position
	r.Receive(pkt(0, 10)) // late: already released
	if after := drain(r.Released()); len(after) != 0 {
		t.Fatalf("released changed after late packets: %v", seqs(after))
	}
	if got := seqs(before); !equalSeqs(got, 0, 2) {
		t.Fatalf("released %v, want [0 2]", got)
	}
	if got := seqs(drain(r.Late())); !equalSeqs(got, 1, 0) {
		t.Fatalf("late %v, want [1 0]", got)
	}
}

func TestResetClearsOldQueueWithoutCrossStream(t *testing.T) {
	r := NewReleaser(ReleaserConfig{WaitLimit: 5, CacheLimit: 8})
	r.OnTick(0)
	r.Receive(pkt(1, 10)) // buffered in epoch 0, never released
	r.Reset(1, 100)
	if r.Buffered() != 0 {
		t.Fatalf("buffered = %d after reset, want 0", r.Buffered())
	}
	if r.Stats().Dropped != 1 {
		t.Fatalf("dropped = %d, want 1", r.Stats().Dropped)
	}
	r.OnTick(1)
	r.Receive(Packet{Epoch: 0, Seq: 0, EventTime: 5}) // stale epoch: late
	r.Receive(Packet{Epoch: 1, Seq: 101, EventTime: 60})
	r.Receive(Packet{Epoch: 1, Seq: 100, EventTime: 50})
	if got := drain(r.Released()); !equalSeqs(seqs(got), 100, 101) {
		t.Fatalf("released %v, want [100 101]", seqs(got))
	}
	for _, p := range drain(r.Released()) {
		if p.Epoch != 1 {
			t.Fatalf("old epoch packet leaked into new stream: %+v", p)
		}
	}
	if got := seqs(drain(r.Late())); !equalSeqs(got, 0) {
		t.Fatalf("late %v, want [0]", got)
	}
}

func TestDuplicateIsDropped(t *testing.T) {
	r := NewReleaser(ReleaserConfig{WaitLimit: 5, CacheLimit: 8})
	r.OnTick(0)
	r.Receive(pkt(1, 10))
	r.Receive(pkt(1, 10)) // duplicate of a buffered seq
	if r.Stats().Duplicates != 1 {
		t.Fatalf("duplicates = %d, want 1", r.Stats().Duplicates)
	}
	r.Receive(pkt(0, 5))
	if got := seqs(drain(r.Released())); !equalSeqs(got, 0, 1) {
		t.Fatalf("released %v, want [0 1]", got)
	}
}

func TestGapTimerReArmsPerMissingHead(t *testing.T) {
	r := NewReleaser(ReleaserConfig{WaitLimit: 3, CacheLimit: 8})
	r.OnTick(0)
	r.Receive(pkt(1, 10))
	r.Receive(pkt(3, 30))
	r.OnTick(3) // skip seq 0, release 1; new gap at seq 2 opens at tick 3
	if got := seqs(drain(r.Released())); !equalSeqs(got, 1) {
		t.Fatalf("released %v, want [1]", got)
	}
	r.OnTick(5)
	if n := len(drain(r.Released())); n != 0 {
		t.Fatalf("tick 5: released %d, want 0 (new head gets full wait)", n)
	}
	r.OnTick(6) // 3 ticks after the new gap opened
	if got := seqs(drain(r.Released())); !equalSeqs(got, 3) {
		t.Fatalf("released %v, want [3]", got)
	}
}
