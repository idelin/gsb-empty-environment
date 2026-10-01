package stabilizer

import (
	"math/rand"
	"sort"
	"testing"
)

func collect(s *Stabilizer, events []Event) []Event {
	var out []Event
	for _, e := range events {
		out = append(out, s.Push(e)...)
	}
	out = append(out, s.Flush()...)
	return out
}

func assertStrictlyMonotonic(t *testing.T, out []Event) {
	t.Helper()
	for i := 1; i < len(out); i++ {
		if out[i].Seq < out[i-1].Seq {
			t.Fatalf("output not monotonic at %d: seq %d after %d", i, out[i].Seq, out[i-1].Seq)
		}
	}
}

func TestReordersOutOfOrderStream(t *testing.T) {
	s := New(3)
	in := []Event{
		{Seq: 1, Source: "a"}, {Seq: 2, Source: "a"}, {Seq: 5, Source: "b"},
		{Seq: 3, Source: "a"}, {Seq: 4, Source: "b"}, {Seq: 8, Source: "c"},
		{Seq: 6, Source: "a"}, {Seq: 7, Source: "b"}, {Seq: 9, Source: "c"},
	}
	out := collect(s, in)
	assertStrictlyMonotonic(t, out)
	if len(out) != len(in) {
		t.Fatalf("got %d events, want %d", len(out), len(in))
	}
	for i, e := range out {
		if e.Seq != int64(i+1) {
			t.Fatalf("out[%d].Seq = %d, want %d", i, e.Seq, i+1)
		}
	}
	if s.Dropped() != 0 {
		t.Fatalf("dropped = %d, want 0", s.Dropped())
	}
}

func TestNothingReleasedBeforeWatermark(t *testing.T) {
	s := New(5)
	for seq := int64(1); seq <= 5; seq++ {
		if got := s.Push(Event{Seq: seq}); len(got) != 0 {
			t.Fatalf("released %d events before watermark advanced", len(got))
		}
	}
	got := s.Push(Event{Seq: 6})
	if len(got) != 1 || got[0].Seq != 1 {
		t.Fatalf("got %v, want [seq=1]", got)
	}
	if wm, ok := s.Watermark(); !ok || wm != 1 {
		t.Fatalf("watermark = %d,%v, want 1,true", wm, ok)
	}
}

func TestLateEventsDropped(t *testing.T) {
	s := New(2)
	in := []Event{{Seq: 1}, {Seq: 2}, {Seq: 10}, {Seq: 3}, {Seq: 1}}
	var out []Event
	for _, e := range in {
		out = append(out, s.Push(e)...)
	}
	assertStrictlyMonotonic(t, out)
	if s.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", s.Dropped())
	}
	want := []int64{1, 2, 3}
	if len(out) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(out), len(want), out)
	}
	for i, seq := range want {
		if out[i].Seq != seq {
			t.Fatalf("out[%d].Seq = %d, want %d (late duplicate leaked?)", i, out[i].Seq, seq)
		}
	}
}

func TestEqualSeqTieBrokenByArrival(t *testing.T) {
	s := New(1)
	in := []Event{
		{Seq: 7, Source: "first"},
		{Seq: 7, Source: "second"},
		{Seq: 9},
	}
	var out []Event
	for _, e := range in {
		out = append(out, s.Push(e)...)
	}
	out = append(out, s.Flush()...)
	if len(out) != 3 {
		t.Fatalf("got %d events, want 3", len(out))
	}
	if out[0].Source != "first" || out[1].Source != "second" {
		t.Fatalf("arrival tie-break violated: %+v", out[:2])
	}
}

func TestFlushDrainsRemainder(t *testing.T) {
	s := New(10)
	in := []Event{{Seq: 3}, {Seq: 1}, {Seq: 2}}
	var out []Event
	for _, e := range in {
		out = append(out, s.Push(e)...)
	}
	if len(out) != 0 {
		t.Fatalf("released %d events with huge window", len(out))
	}
	out = append(out, s.Flush()...)
	if len(out) != 3 || out[0].Seq != 1 || out[1].Seq != 2 || out[2].Seq != 3 {
		t.Fatalf("flush output = %+v", out)
	}
	if s.Pending() != 0 {
		t.Fatalf("pending = %d after flush", s.Pending())
	}
}

func TestNegativeWindowPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for negative window")
		}
	}()
	New(-1)
}

// TestRandomizedBoundedDisorder is a property-style check: for any input
// whose disorder stays within the window K, the concatenated output must
// equal the globally sorted input.
func TestRandomizedBoundedDisorder(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const k = 4
	for trial := 0; trial < 200; trial++ {
		n := 1 + rng.Intn(200)
		sorted := make([]Event, n)
		for i := range sorted {
			sorted[i] = Event{Seq: int64(i + 1)}
		}
		// Assign each element an arrival slot at most K past its sorted
		// position, then order by (slot, position). Every event is thus
		// delayed by at most K and can never outlive the window.
		type slotted struct {
			ev   Event
			slot int
			pos  int
		}
		slots := make([]slotted, n)
		for i := range sorted {
			slots[i] = slotted{ev: sorted[i], slot: i + rng.Intn(min(k+1, n-i)), pos: i}
		}
		sort.SliceStable(slots, func(a, b int) bool { return slots[a].slot < slots[b].slot })
		shuffled := make([]Event, n)
		for i := range slots {
			shuffled[i] = slots[i].ev
		}
		s := New(k)
		out := collect(s, shuffled)
		if s.Dropped() != 0 {
			t.Fatalf("trial %d: dropped %d events within window", trial, s.Dropped())
		}
		if len(out) != n {
			t.Fatalf("trial %d: got %d events, want %d", trial, len(out), n)
		}
		if !sort.SliceIsSorted(out, func(i, j int) bool { return out[i].Seq < out[j].Seq }) {
			t.Fatalf("trial %d: output not sorted", trial)
		}
		for i := range out {
			if out[i].Seq != sorted[i].Seq {
				t.Fatalf("trial %d: out[%d].Seq = %d, want %d", trial, i, out[i].Seq, sorted[i].Seq)
			}
		}
	}
}
