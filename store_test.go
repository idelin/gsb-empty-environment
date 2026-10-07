package factstore

import (
	"fmt"
	"math/rand"
	"testing"
)

func mustGet(t *testing.T, s *Store, key string, v int64, asOf uint64) Record {
	t.Helper()
	rec, ok := s.Get(key, v, asOf)
	if !ok {
		t.Fatalf("Get(%q, %d, %d): expected a record, got none", key, v, asOf)
	}
	return rec
}

func mustMiss(t *testing.T, s *Store, key string, v int64, asOf uint64) {
	t.Helper()
	if rec, ok := s.Get(key, v, asOf); ok {
		t.Fatalf("Get(%q, %d, %d): expected no record, got %+v", key, v, asOf, rec)
	}
}

// A correction written later must not leak into an older knowledge
// snapshot, while a fresh snapshot sees it.
func TestCorrectionDoesNotLeakIntoOldSnapshot(t *testing.T) {
	s := New()
	a, _ := s.Put("acct", "open", 0, 10)  // seq 1
	b, _ := s.Put("acct", "closed", 3, 5) // seq 2, retroactive correction

	if got := mustGet(t, s, "acct", 4, a.Seq); got.Value != "open" {
		t.Fatalf("old snapshot at v=4: got %v, want open", got.Value)
	}
	if got := mustGet(t, s, "acct", 4, b.Seq); got.Value != "closed" {
		t.Fatalf("new snapshot at v=4: got %v, want closed", got.Value)
	}
	if got := mustGet(t, s, "acct", 0, b.Seq); got.Value != "open" {
		t.Fatalf("outside correction interval: got %v, want open", got.Value)
	}
	mustMiss(t, s, "acct", 4, 0) // before any write
}

// Deletions carry their own valid interval and only retract that slice.
func TestDeleteHasValidInterval(t *testing.T) {
	s := New()
	a, _ := s.Put("acct", "open", 0, 10)
	d, _ := s.Delete("acct", 2, 4)

	mustMiss(t, s, "acct", 3, s.Now()) // deleted slice
	if got := mustGet(t, s, "acct", 3, a.Seq); got.Value != "open" {
		t.Fatalf("snapshot before delete: got %v, want open", got.Value)
	}
	if got := mustGet(t, s, "acct", 5, d.Seq); got.Value != "open" {
		t.Fatalf("outside delete interval: got %v, want open", got.Value)
	}
}

// Overlapping facts resolve to the one with the greatest Seq.
func TestOverlapTakesMaxSeq(t *testing.T) {
	s := New()
	s.Put("k", "v1", 0, 10)
	s.Put("k", "v2", 2, 8)
	s.Put("k", "v3", 4, 6)

	if got := mustGet(t, s, "k", 5, s.Now()); got.Value != "v3" {
		t.Fatalf("got %v, want v3", got.Value)
	}
	if got := mustGet(t, s, "k", 3, s.Now()); got.Value != "v2" {
		t.Fatalf("got %v, want v2", got.Value)
	}
	if got := mustGet(t, s, "k", 1, s.Now()); got.Value != "v1" {
		t.Fatalf("got %v, want v1", got.Value)
	}
}

// Overlapping writes split the valid-time timeline at record boundaries.
func TestIntervalSplitting(t *testing.T) {
	s := New()
	s.Put("k", "v1", 0, 10)
	s.Put("k", "v2", 3, 5)

	segs := s.Segments("k")
	want := [][2]int64{{0, 3}, {3, 5}, {5, 10}}
	if len(segs) != len(want) {
		t.Fatalf("segments = %+v, want boundaries %v", segs, want)
	}
	for i, w := range want {
		if segs[i].From != w[0] || segs[i].To != w[1] {
			t.Fatalf("segment %d = [%d,%d), want [%d,%d)", i, segs[i].From, segs[i].To, w[0], w[1])
		}
	}
	if n := len(segs[1].Versions); n != 2 {
		t.Fatalf("middle segment has %d versions, want 2", n)
	}
	if n := len(segs[0].Versions); n != 1 {
		t.Fatalf("left segment has %d versions, want 1", n)
	}
}

// Every write stays reachable through History, so corrections are
// traceable.
func TestHistoryTracesCorrections(t *testing.T) {
	s := New()
	s.Put("k", "v1", 0, 10)
	s.Put("k", "v2", 2, 8)
	s.Delete("k", 4, 6)

	h := s.History("k")
	if len(h) != 3 {
		t.Fatalf("history len = %d, want 3", len(h))
	}
	for i, rec := range h {
		if rec.Seq != uint64(i+1) {
			t.Fatalf("history[%d].Seq = %d, want %d", i, rec.Seq, i+1)
		}
	}
	if !h[2].Deleted {
		t.Fatal("third history entry should be the tombstone")
	}
}

func TestInvalidRange(t *testing.T) {
	s := New()
	if _, err := s.Put("k", "v", 5, 5); err != ErrInvalidRange {
		t.Fatalf("empty range: err = %v", err)
	}
	if _, err := s.Delete("k", 9, 2); err != ErrInvalidRange {
		t.Fatalf("inverted range: err = %v", err)
	}
}

// model is the brute-force reference: a point query scans the raw log.
type model struct {
	recs []Record
}

func (m *model) apply(rec Record) { m.recs = append(m.recs, rec) }

func (m *model) get(key string, v int64, asOf uint64) (Record, bool) {
	var best Record
	found := false
	for _, r := range m.recs {
		if r.Key == key && r.Seq <= asOf && r.ValidFrom <= v && v < r.ValidTo && (!found || r.Seq > best.Seq) {
			best, found = r, true
		}
	}
	if !found || best.Deleted {
		return Record{}, false
	}
	return best, true
}

// checkInvariants validates the split segment timeline of one key.
func checkInvariants(t *testing.T, s *Store, key string) {
	t.Helper()
	segs := s.Segments(key)
	for i, seg := range segs {
		if seg.From >= seg.To {
			t.Fatalf("key %q segment %d: empty or inverted [%d,%d)", key, i, seg.From, seg.To)
		}
		if i > 0 && segs[i-1].To > seg.From {
			t.Fatalf("key %q segments %d/%d overlap", key, i-1, i)
		}
		if len(seg.Versions) == 0 {
			t.Fatalf("key %q segment %d: no versions", key, i)
		}
		for j, ver := range seg.Versions {
			if j > 0 && seg.Versions[j-1].TxFrom >= ver.TxFrom {
				t.Fatalf("key %q segment %d: versions not strictly increasing", key, i)
			}
			if ver.Rec.ValidFrom > seg.From || ver.Rec.ValidTo < seg.To {
				t.Fatalf("key %q segment %d: record [%d,%d) does not cover [%d,%d)",
					key, i, ver.Rec.ValidFrom, ver.Rec.ValidTo, seg.From, seg.To)
			}
		}
	}
}

// TestRandomAgainstModel cross-checks the store against the 2D point
// query model: after random puts/deletes, random (validTime, asOf)
// points must resolve identically in both.
func TestRandomAgainstModel(t *testing.T) {
	for seed := int64(0); seed < 5; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomAgainstModel(t, seed)
		})
	}
}

func runRandomAgainstModel(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	s := New()
	m := &model{}
	keys := []string{"a", "b", "c"}

	for op := 0; op < 300; op++ {
		key := keys[rng.Intn(len(keys))]
		from := int64(rng.Intn(50))
		to := from + 1 + int64(rng.Intn(10))
		var rec Record
		var err error
		if rng.Intn(4) == 0 {
			rec, err = s.Delete(key, from, to)
		} else {
			rec, err = s.Put(key, op, from, to)
		}
		if err != nil {
			t.Fatalf("op %d: %v", op, err)
		}
		m.apply(rec)

		now := s.Now()
		for q := 0; q < 40; q++ {
			v := int64(rng.Intn(62)) - 1
			asOf := uint64(rng.Intn(int(now) + 2)) // may exceed now
			wantRec, wantOK := m.get(key, v, asOf)
			gotRec, gotOK := s.Get(key, v, asOf)
			if gotOK != wantOK {
				t.Fatalf("op %d Get(%q,%d,%d): ok=%v, model ok=%v", op, key, v, asOf, gotOK, wantOK)
			}
			if gotOK && gotRec != wantRec {
				t.Fatalf("op %d Get(%q,%d,%d): got %+v, model %+v", op, key, v, asOf, gotRec, wantRec)
			}
		}
	}
	for _, key := range keys {
		checkInvariants(t, s, key)
	}
}
