package tsxform

import (
	"math/rand"
	"testing"
)

// Equal ideal ticks across streams must merge in declared input order, and
// the link must serialize transmissions.
func TestDeclaredOrderMergeAndSerialization(t *testing.T) {
	cfg := Config{MTU: 1500, BytesPerTick: 100, TimeRate: MustRatio(1, 1), ByteRate: MustRatio(1, 1)}
	pkts := []Packet{
		{Stream: 0, Time: 5, Size: 100},
		{Stream: 1, Time: 5, Size: 250},
		{Stream: 2, Time: 5, Size: 50},
		{Stream: 0, Time: 5, Size: 10},
	}
	out, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("got %d records, want 4", len(out))
	}
	for i, rec := range out {
		if rec.Seq != int64(i) {
			t.Errorf("departure %d has seq %d, want declared order %d", i, rec.Seq, i)
		}
		if i > 0 && rec.Depart < out[i-1].Done {
			t.Errorf("record %d departs at %d during previous transmission (done %d)", i, rec.Depart, out[i-1].Done)
		}
		wantDur := (rec.Size + 99) / 100
		if rec.Done-rec.Depart != wantDur {
			t.Errorf("record %d occupies %d ticks, want %d", i, rec.Done-rec.Depart, wantDur)
		}
	}
}

// Per-stream causality holds for a randomized trace: packets of a stream
// depart in input order with non-decreasing ticks.
func TestCausalityRandomTrace(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var pkts []Packet
	last := map[int]int64{}
	for i := 0; i < 400; i++ {
		s := rng.Intn(4)
		last[s] += int64(rng.Intn(5))
		pkts = append(pkts, Packet{Stream: s, Time: last[s], Size: int64(1 + rng.Intn(900))})
	}
	cfg := Config{MTU: 400, BytesPerTick: 64, TimeRate: MustRatio(3, 2), ByteRate: MustRatio(2, 3)}
	out, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}
	lastPkt := map[int]int64{}
	lastDepart := map[int]int64{}
	seen := map[int]bool{}
	for _, rec := range out {
		if seen[rec.Stream] {
			if rec.PacketID < lastPkt[rec.Stream] {
				t.Fatalf("stream %d: packet %d after %d", rec.Stream, rec.PacketID, lastPkt[rec.Stream])
			}
			if rec.Depart < lastDepart[rec.Stream] {
				t.Fatalf("stream %d: depart %d goes backwards", rec.Stream, rec.Depart)
			}
		}
		seen[rec.Stream] = true
		lastPkt[rec.Stream] = rec.PacketID
		lastDepart[rec.Stream] = rec.Depart
	}
}
