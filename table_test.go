package checksumsync

import (
	"fmt"
	"math/rand"
	"testing"
)

// The rolling digest must equal a from-scratch recomputation at any time.
func recompute(t *Table) [NumBuckets]Digest {
	var out [NumBuckets]Digest
	for i := range t.buckets {
		b := &t.buckets[i]
		b.mu.RLock()
		for pk, v := range b.rows {
			out[i].Xor ^= hashRow(pk, v)
			out[i].Count++
		}
		b.mu.RUnlock()
	}
	return out
}

func TestRollingChecksumMatchesRecompute(t *testing.T) {
	tbl := NewTable()
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < 20000; i++ {
		pk := uint64(rng.Intn(5000))
		switch rng.Intn(3) {
		case 0:
			tbl.Delete(pk)
		default:
			tbl.Upsert(pk, fmt.Sprintf("v-%d", rng.Int()))
		}
		if i%1000 == 0 {
			if got, want := tbl.SnapshotDigests(), recompute(tbl); got != want {
				t.Fatalf("op %d: rolling digest diverged from recompute", i)
			}
		}
	}
	if got, want := tbl.SnapshotDigests(), recompute(tbl); got != want {
		t.Fatal("final rolling digest diverged from recompute")
	}
}

func TestIdenticalChangeStreamsProduceIdenticalDigests(t *testing.T) {
	src, tgt := NewTable(), NewTable()
	rng := rand.New(rand.NewSource(7))

	for i := 0; i < 50000; i++ {
		pk := uint64(rng.Intn(10000))
		val := fmt.Sprintf("payload-%d", rng.Int())
		src.Upsert(pk, val)
		tgt.Upsert(pk, val)
	}
	if src.SnapshotDigests() != tgt.SnapshotDigests() {
		t.Fatal("identical change streams must yield identical digests")
	}
	if rep := Compare(src, tgt); !rep.Consistent() {
		t.Fatalf("expected consistent, got %v", rep.Mismatches)
	}
}

func TestUpdateAndDeleteRollChecksumCorrectly(t *testing.T) {
	a, b := NewTable(), NewTable()
	a.Upsert(1, "x")
	a.Upsert(1, "y") // update must roll "x" out and "y" in
	b.Upsert(1, "y")
	a.Upsert(2, "z")
	a.Delete(2) // delete must restore the digest
	if a.SnapshotDigests() != b.SnapshotDigests() {
		t.Fatal("update/delete did not roll the checksum correctly")
	}
}
