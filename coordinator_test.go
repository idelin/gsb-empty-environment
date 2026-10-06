package wquantile

import (
	"math/rand"
	"testing"
)

type partGen struct {
	seq uint64
	own map[int64]int64 // net weights this partition has published
}

// genRound produces n random deltas followed by the round barrier, all
// with consecutive per-partition sequence numbers.
func (g *partGen) genRound(rng *rand.Rand, n int, barrier uint64) []streamMsg {
	var items []streamMsg
	for i := 0; i < n; i++ {
		var d Delta
		if rng.Intn(100) < 55 || len(g.own) == 0 {
			v := int64(rng.Intn(16) - 8)
			w := int64(rng.Intn(4)) // 0..3, zero-weight add is a no-op
			d = Delta{Seq: g.seq, Value: v, Weight: w}
			g.own[v] += w
			if g.own[v] == 0 {
				delete(g.own, v)
			}
		} else {
			var live []int64
			for v, c := range g.own {
				if c > 0 {
					live = append(live, v)
				}
			}
			v := live[rng.Intn(len(live))]
			w := int64(rng.Intn(int(g.own[v])) + 1)
			d = Delta{Seq: g.seq, Value: v, Weight: -w}
			g.own[v] -= w
			if g.own[v] == 0 {
				delete(g.own, v)
			}
		}
		g.seq++
		items = append(items, streamMsg{delta: d, isDelta: true})
	}
	items = append(items, streamMsg{barrier: Barrier{Seq: g.seq, ID: barrier}})
	g.seq++
	return items
}

type streamMsg struct {
	delta   Delta
	barrier Barrier
	isDelta bool
}

func applyToModel(samples []int64, d Delta) []int64 {
	if d.Weight >= 0 {
		for i := int64(0); i < d.Weight; i++ {
			samples = append(samples, d.Value)
		}
		return samples
	}
	w := -d.Weight
	for i := 0; i < len(samples) && w > 0; {
		if samples[i] == d.Value {
			samples = append(samples[:i], samples[i+1:]...)
			w--
		} else {
			i++
		}
	}
	return samples
}

func checkCoordinator(t *testing.T, c *Coordinator, samples []int64) {
	t.Helper()
	if c.Total() != uint64(len(samples)) {
		t.Fatalf("total: got %d want %d", c.Total(), len(samples))
	}
	for _, q := range qs {
		got, err := c.Quantile(q)
		want, ok := modelQuantile(samples, q)
		if !ok {
			if err != ErrEmpty {
				t.Fatalf("q=%v: expected ErrEmpty, got %v (%v)", q, got, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("q=%v: unexpected error %v", q, err)
		}
		if got != want {
			t.Fatalf("q=%v: got %d want %d", q, got, want)
		}
	}
}

// TestRandomPartitions feeds several partitions' deltas out of order and
// duplicated, commits barrier cuts, and cross-checks against the expanded
// raw-sample model. Midway it snapshots and restores, then re-delivers
// everything to prove dedup and undo state survive.
func TestRandomPartitions(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	c := NewCoordinator()
	ids := []string{"a", "b", "c"}
	for _, id := range ids {
		if err := c.AddPartition(id); err != nil {
			t.Fatal(err)
		}
	}
	gens := map[string]*partGen{}
	for _, id := range ids {
		gens[id] = &partGen{own: map[int64]int64{}}
	}
	var samples []int64
	const rounds = 12
	for r := uint64(1); r <= rounds; r++ {
		type tagged struct {
			id  string
			msg streamMsg
		}
		var flat []tagged
		roundDeltas := map[string][]Delta{}
		for _, id := range ids {
			items := gens[id].genRound(rng, 8, r)
			for _, it := range items {
				flat = append(flat, tagged{id, it})
				if it.isDelta {
					roundDeltas[id] = append(roundDeltas[id], it.delta)
				}
			}
		}
		// Shuffle for out-of-order delivery across the whole round.
		for i := range flat {
			j := rng.Intn(i + 1)
			flat[i], flat[j] = flat[j], flat[i]
		}
		send := func(list []tagged) {
			for _, e := range list {
				times := 1 + rng.Intn(2) // randomly duplicate deliveries
				for k := 0; k < times; k++ {
					var err error
					if e.msg.isDelta {
						err = c.PublishDelta(e.id, e.msg.delta)
					} else {
						err = c.PublishBarrier(e.id, e.msg.barrier)
					}
					if err != nil {
						t.Fatalf("round %d: %v", r, err)
					}
				}
			}
		}
		if r == 6 {
			// Snapshot mid-round, restore, re-deliver everything sent so
			// far (all duplicates: must be ignored), then continue.
			half := len(flat) / 2
			send(flat[:half])
			data, err := c.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			c, err = RestoreCoordinator(data)
			if err != nil {
				t.Fatal(err)
			}
			send(flat[:half])
			send(flat[half:])
		} else {
			send(flat)
		}
		cut, ok := c.LatestCut()
		if !ok || cut != r {
			t.Fatalf("round %d: latest cut = %d,%v", r, cut, ok)
		}
		if !c.CutComplete(r) {
			t.Fatalf("round %d: cut not complete", r)
		}
		if _, err := c.QuantileAt(r+1, 0.5); err != ErrIncompleteCut {
			t.Fatalf("round %d: future cut: got %v want ErrIncompleteCut", r, err)
		}
		for _, id := range ids {
			for _, d := range roundDeltas[id] {
				samples = applyToModel(samples, d)
			}
		}
		checkCoordinator(t, c, samples)
	}
}

// TestMissingPartitionNoMixing verifies that a lagging partition makes new
// cuts report incomplete while queries keep serving the last complete cut.
func TestMissingPartitionNoMixing(t *testing.T) {
	c := NewCoordinator()
	for _, id := range []string{"p1", "p2"} {
		if err := c.AddPartition(id); err != nil {
			t.Fatal(err)
		}
	}
	pub := func(id string, d Delta) {
		if err := c.PublishDelta(id, d); err != nil {
			t.Fatal(err)
		}
	}
	bar := func(id string, b Barrier) {
		if err := c.PublishBarrier(id, b); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Quantile(0.5); err != ErrNoCompleteCut {
		t.Fatalf("before any cut: got %v want ErrNoCompleteCut", err)
	}
	// Cut 1: p1 has {10}, p2 has {20}.
	pub("p1", Delta{Seq: 0, Value: 10, Weight: 1})
	bar("p1", Barrier{Seq: 1, ID: 1})
	pub("p2", Delta{Seq: 0, Value: 20, Weight: 1})
	bar("p2", Barrier{Seq: 1, ID: 1})
	if got, err := c.Quantile(0.5); err != nil || got != 10 {
		t.Fatalf("cut 1 median: got %d,%v want 10,nil", got, err)
	}
	// Cut 2: p1 adds {30} and publishes barrier 2; p2 lags (disconnected).
	pub("p1", Delta{Seq: 2, Value: 30, Weight: 1})
	bar("p1", Barrier{Seq: 3, ID: 2})
	if err := c.Disconnect("p2"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.QuantileAt(2, 0.5); err != ErrIncompleteCut {
		t.Fatalf("incomplete cut: got %v want ErrIncompleteCut", err)
	}
	// Still the old consistent cut: no old/new mixing.
	if got, err := c.Quantile(0.5); err != nil || got != 10 {
		t.Fatalf("stale-consistent median: got %d,%v want 10,nil", got, err)
	}
	if err := c.PublishDelta("p2", Delta{Seq: 2, Value: 40, Weight: 1}); err != ErrPartitionDown {
		t.Fatalf("publish while down: got %v want ErrPartitionDown", err)
	}
	if err := c.Reconnect("p2"); err != nil {
		t.Fatal(err)
	}
	pub("p2", Delta{Seq: 2, Value: 40, Weight: 1})
	bar("p2", Barrier{Seq: 3, ID: 2})
	if got, err := c.QuantileAt(2, 0.5); err != nil || got != 20 {
		t.Fatalf("cut 2 median: got %d,%v want 20,nil", got, err)
	}
	if cut, ok := c.LatestCut(); !ok || cut != 2 {
		t.Fatalf("latest cut: got %d,%v want 2,true", cut, ok)
	}
}

// TestSnapshotDedupAndUndo checks that a restored coordinator keeps
// ignoring duplicates and applies removes exactly once.
func TestSnapshotDedupAndUndo(t *testing.T) {
	c := NewCoordinator()
	if err := c.AddPartition("x"); err != nil {
		t.Fatal(err)
	}
	pub := func(d Delta) {
		if err := c.PublishDelta("x", d); err != nil {
			t.Fatal(err)
		}
	}
	bar := func(seq, id uint64) {
		if err := c.PublishBarrier("x", Barrier{Seq: seq, ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	pub(Delta{Seq: 0, Value: 1, Weight: 2})
	pub(Delta{Seq: 1, Value: 2, Weight: 2})
	bar(2, 1)
	// Out-of-order: seq 4 before 3; duplicate seq 2 (barrier, consumed).
	pub(Delta{Seq: 4, Value: 3, Weight: 1})
	if err := c.PublishBarrier("x", Barrier{Seq: 2, ID: 1}); err != nil {
		t.Fatal(err) // duplicate of an already-consumed seq: ignored
	}
	data, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	c, err = RestoreCoordinator(data)
	if err != nil {
		t.Fatal(err)
	}
	// Re-send everything already sent: all duplicates, must be ignored.
	for _, d := range []Delta{{Seq: 0, Value: 1, Weight: 2}, {Seq: 1, Value: 2, Weight: 2}, {Seq: 4, Value: 3, Weight: 1}} {
		if err := c.PublishDelta("x", d); err != nil {
			t.Fatal(err)
		}
	}
	// Undo: remove one unit of value 1, then close cut 2 at seq 5.
	pub(Delta{Seq: 3, Value: 1, Weight: -1})
	bar(5, 2)
	// Committed: {1:1, 2:2, 3:1} -> sorted [1,2,2,3].
	samples := []int64{1, 2, 2, 3}
	checkCoordinator(t, c, samples)
	if cut, ok := c.LatestCut(); !ok || cut != 2 {
		t.Fatalf("latest cut: got %d,%v want 2,true", cut, ok)
	}
}

// TestEmptyCommittedCut: a cut with no deltas yields an empty set.
func TestEmptyCommittedCut(t *testing.T) {
	c := NewCoordinator()
	if err := c.AddPartition("only"); err != nil {
		t.Fatal(err)
	}
	if err := c.PublishBarrier("only", Barrier{Seq: 0, ID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.LatestCut(); !ok {
		t.Fatal("cut 1 should be complete")
	}
	if _, err := c.Quantile(0.5); err != ErrEmpty {
		t.Fatalf("empty committed set: got %v want ErrEmpty", err)
	}
	if c.Total() != 0 {
		t.Fatalf("total: got %d want 0", c.Total())
	}
}

// TestUnknownAndDuplicatePartitions covers registration errors.
func TestUnknownAndDuplicatePartitions(t *testing.T) {
	c := NewCoordinator()
	if err := c.PublishDelta("ghost", Delta{Seq: 0, Value: 1, Weight: 1}); err != ErrUnknownPartition {
		t.Fatalf("got %v want ErrUnknownPartition", err)
	}
	if err := c.AddPartition("a"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPartition("a"); err != ErrDuplicatePartition {
		t.Fatalf("got %v want ErrDuplicatePartition", err)
	}
	if err := c.Disconnect("ghost"); err != ErrUnknownPartition {
		t.Fatalf("got %v want ErrUnknownPartition", err)
	}
}
