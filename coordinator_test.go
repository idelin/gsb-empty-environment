package stats

import (
	"errors"
	"math/rand"
	"sort"
	"testing"
)

// rawModel is the reference implementation: it summarizes the raw samples
// published by every partition up to a barrier cut, with no dedup, ordering
// or undo cleverness.
type rawModel struct {
	deltas map[string][]Delta
}

func newRawModel() *rawModel { return &rawModel{deltas: make(map[string][]Delta)} }

func (m *rawModel) publish(partition string, d Delta) {
	m.deltas[partition] = append(m.deltas[partition], d)
}

// quantileAt replays each partition's deltas in sequence order and applies
// every delta up to and including the one that completed the barrier.
func (m *rawModel) quantileAt(partitions []string, barrier uint64, q float64) (int64, bool) {
	var samples []int64
	for _, id := range partitions {
		ds := append([]Delta(nil), m.deltas[id]...)
		sort.Slice(ds, func(i, j int) bool { return ds[i].Seq < ds[j].Seq })
		counts := make(map[int64]uint64)
		for _, d := range ds {
			for _, op := range d.Ops {
				if op.Remove {
					counts[op.Value] -= op.Weight
					if counts[op.Value] == 0 {
						delete(counts, op.Value)
					}
				} else {
					counts[op.Value] += op.Weight
				}
			}
			if b := d.Barrier; b != 0 && b >= barrier {
				break
			}
		}
		for v, w := range counts {
			for i := uint64(0); i < w; i++ {
				samples = append(samples, v)
			}
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return modelQuantile(samples, q)
}

// stream generates a per-partition delta stream with valid adds/removes and
// a barrier every few deltas.
type stream struct {
	deltas []Delta
	counts map[int64]uint64
}

func genStream(rng *rand.Rand, barriers, maxDeltaPerBarrier int) stream {
	s := stream{counts: make(map[int64]uint64)}
	var seq uint64
	for b := uint64(1); b <= uint64(barriers); b++ {
		n := 1 + rng.Intn(maxDeltaPerBarrier)
		for i := 0; i < n; i++ {
			d := Delta{Seq: seq}
			seq++
			ops := 1 + rng.Intn(3)
			for j := 0; j < ops; j++ {
				if len(s.counts) > 0 && rng.Intn(3) == 0 {
					var v int64
					for k := range s.counts {
						v = k
						break
					}
					weight := uint64(1 + rng.Intn(int(s.counts[v])))
					d.Ops = append(d.Ops, Op{Value: v, Weight: weight, Remove: true})
					s.counts[v] -= weight
					if s.counts[v] == 0 {
						delete(s.counts, v)
					}
				} else {
					v := int64(rng.Intn(13) - 6)
					weight := uint64(rng.Intn(4)) // includes zero weights
					d.Ops = append(d.Ops, Op{Value: v, Weight: weight})
					if weight > 0 {
						s.counts[v] += weight
					}
				}
			}
			if i == n-1 {
				d.Barrier = b
			}
			s.deltas = append(s.deltas, d)
		}
	}
	return s
}

// shuffledDelivery returns the deltas in random order, each repeated a
// random number of times to exercise dedup.
func shuffledDelivery(rng *rand.Rand, deltas []Delta) []Delta {
	var out []Delta
	for _, d := range deltas {
		for i := 0; i < 1+rng.Intn(3); i++ {
			out = append(out, d)
		}
	}
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

var queryQuantiles = []float64{0, 0.25, 0.5, 0.75, 1}

func checkAllBarriers(t *testing.T, c *Coordinator, m *rawModel, partitions []string, barriers int) {
	t.Helper()
	for b := uint64(1); b <= uint64(barriers); b++ {
		for _, q := range queryQuantiles {
			got, err := c.Query(b, q)
			want, ok := m.quantileAt(partitions, b, q)
			if !ok {
				if !errors.Is(err, ErrEmpty) {
					t.Fatalf("barrier %d q=%v: got err=%v, want ErrEmpty", b, q, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("barrier %d q=%v: unexpected err %v", b, q, err)
			}
			if got != want {
				t.Fatalf("barrier %d q=%v: got %d, want %d", b, q, got, want)
			}
		}
	}
}

func TestBarrierCutConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	partitions := []string{"p0", "p1", "p2"}
	const barriers = 6
	streams := make(map[string]stream)
	m := newRawModel()
	c := NewCoordinator()
	for _, id := range partitions {
		c.Track(id)
		streams[id] = genStream(rng, barriers, 2)
		for _, d := range streams[id].deltas {
			m.publish(id, d)
		}
	}

	// Missing partition: nothing published yet, every query is incomplete
	// rather than answered from a partial mix.
	if _, err := c.Query(1, 0.5); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("want ErrIncomplete before any delta, got %v", err)
	}

	// Publish p0 fully, p1 up to barrier 2, nothing for p2.
	for _, d := range shuffledDelivery(rng, streams["p0"].deltas) {
		if err := c.Apply("p0", d); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range streams["p1"].deltas {
		if d.Barrier != 0 && d.Barrier > 2 {
			break
		}
		if err := c.Apply("p1", d); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range []uint64{1, 2, 3} {
		if _, err := c.Query(b, 0.5); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("barrier %d: want ErrIncomplete with missing partitions, got %v", b, err)
		}
	}

	// Finish p1 and p2 with shuffled, duplicated delivery.
	type tagged struct {
		id string
		d  Delta
	}
	var rest []tagged
	for _, id := range []string{"p1", "p2"} {
		for _, d := range shuffledDelivery(rng, streams[id].deltas) {
			rest = append(rest, tagged{id, d})
		}
	}
	rng.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	for _, td := range rest {
		if err := c.Apply(td.id, td.d); err != nil {
			t.Fatal(err)
		}
	}
	if !c.Complete(barriers) {
		t.Fatal("all barriers must be complete after full delivery")
	}
	checkAllBarriers(t, c, m, partitions, barriers)
}

func TestOutOfOrderAndDuplicatesSinglePartition(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	const barriers = 8
	s := genStream(rng, barriers, 3)
	m := newRawModel()
	for _, d := range s.deltas {
		m.publish("p", d)
	}
	c := NewCoordinator()
	// Deliver strictly reversed, each delta twice.
	for i := len(s.deltas) - 1; i >= 0; i-- {
		for rep := 0; rep < 2; rep++ {
			if err := c.Apply("p", s.deltas[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	checkAllBarriers(t, c, m, []string{"p"}, barriers)
}

func TestSnapshotRestorePreservesDedupAndUndo(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	partitions := []string{"a", "b"}
	const barriers = 5
	streams := make(map[string]stream)
	m := newRawModel()
	for _, id := range partitions {
		streams[id] = genStream(rng, barriers, 2)
		for _, d := range streams[id].deltas {
			m.publish(id, d)
		}
	}

	c := NewCoordinator()
	// Deliver the first half of each stream.
	half := make(map[string]int)
	for _, id := range partitions {
		half[id] = len(streams[id].deltas) / 2
		for _, d := range streams[id].deltas[:half[id]] {
			if err := c.Apply(id, d); err != nil {
				t.Fatal(err)
			}
		}
	}

	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(snap)
	if err != nil {
		t.Fatal(err)
	}

	// Replay already-applied deltas: dedup state must drop them.
	for _, id := range partitions {
		for _, d := range shuffledDelivery(rng, streams[id].deltas[:half[id]]) {
			if err := restored.Apply(id, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Deliver the remainder out of order with duplicates.
	for _, id := range partitions {
		for _, d := range shuffledDelivery(rng, streams[id].deltas[half[id]:]) {
			if err := restored.Apply(id, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Undo state must survive the snapshot: old barrier cuts stay queryable.
	checkAllBarriers(t, restored, m, partitions, barriers)

	// A second snapshot/restore round trip must not change anything.
	snap2, err := restored.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Restore(snap2)
	if err != nil {
		t.Fatal(err)
	}
	checkAllBarriers(t, again, m, partitions, barriers)
}

func TestSnapshotRestoreMidBarrierKeepsPending(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	s := genStream(rng, 3, 3)
	m := newRawModel()
	for _, d := range s.deltas {
		m.publish("p", d)
	}
	c := NewCoordinator()
	// Apply only the even-numbered deltas: everything odd stays pending.
	for _, d := range s.deltas {
		if d.Seq%2 == 0 {
			continue
		}
		if err := c.Apply("p", d); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range s.deltas {
		if err := restored.Apply("p", d); err != nil {
			t.Fatal(err)
		}
	}
	checkAllBarriers(t, restored, m, []string{"p"}, 3)
}

func TestQueryEmptyCut(t *testing.T) {
	c := NewCoordinator()
	c.Track("p")
	if err := c.Apply("p", Delta{Seq: 0, Barrier: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Query(1, 0.5); !errors.Is(err, ErrEmpty) {
		t.Fatalf("want ErrEmpty on empty cut, got %v", err)
	}
}

func TestRemoveUnderflowLeavesStateIntact(t *testing.T) {
	c := NewCoordinator()
	if err := c.Apply("p", Delta{Seq: 0, Ops: []Op{{Value: 1, Weight: 2}}, Barrier: 1}); err != nil {
		t.Fatal(err)
	}
	bad := Delta{Seq: 1, Ops: []Op{
		{Value: 2, Weight: 5},
		{Value: 1, Weight: 9, Remove: true}, // underflows
	}}
	if err := c.Apply("p", bad); err == nil {
		t.Fatal("expected underflow error")
	}
	// The failed delta must be fully rolled back and the stream must be
	// able to continue at the same sequence number.
	good := Delta{Seq: 1, Ops: []Op{{Value: 2, Weight: 5}}, Barrier: 2}
	if err := c.Apply("p", good); err != nil {
		t.Fatal(err)
	}
	got, err := c.Query(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("got %d, want 2", got)
	}
}
