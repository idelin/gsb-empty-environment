package reordersim

import "testing"

func TestEventsRunInTickOrder(t *testing.T) {
	s := NewSimulator()
	var got []int64
	for _, tick := range []int64{3, 1, 2} {
		tick := tick
		s.Schedule(tick, func() { got = append(got, tick) })
	}
	s.Run(3)
	want := []int64{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSameTickRunsFIFO(t *testing.T) {
	s := NewSimulator()
	var got []int
	for i := 0; i < 3; i++ {
		i := i
		s.Schedule(5, func() { got = append(got, i) })
	}
	s.Run(5)
	for i := 0; i < 3; i++ {
		if got[i] != i {
			t.Fatalf("got %v, want [0 1 2]", got)
		}
	}
}

func TestZeroDelayRunsInSameTick(t *testing.T) {
	s := NewSimulator()
	var got []string
	s.Schedule(1, func() {
		got = append(got, "first")
		s.After(0, func() { got = append(got, "chained") })
	})
	s.Schedule(1, func() { got = append(got, "second") })
	s.Run(1)
	want := []string{"first", "second", "chained"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTickableSeesEveryTick(t *testing.T) {
	s := NewSimulator()
	r := NewReleaser(ReleaserConfig{WaitLimit: 2, CacheLimit: 8})
	s.AddTickable(r)
	var ticks []int64
	s.Schedule(4, func() { ticks = append(ticks, s.Now()) })
	s.Run(5)
	if r.now != 5 {
		t.Fatalf("releaser clock = %d, want 5", r.now)
	}
	if len(ticks) != 1 || ticks[0] != 4 {
		t.Fatalf("event ran at %v, want [4]", ticks)
	}
}

// TestEndToEndReordersAcrossLink drives packets through a delayed link into
// the releaser: arrivals are out of order, one packet is lost past the wait
// limit, and the stream still completes without blocking.
func TestEndToEndReordersAcrossLink(t *testing.T) {
	s := NewSimulator()
	r := NewReleaser(ReleaserConfig{WaitLimit: 4, CacheLimit: 3})
	s.AddTickable(r)
	link := &Link{
		Sim: s,
		// Deterministic per-packet delay; seq 2 is delayed so long that the
		// wait limit skips it before it arrives.
		Delay: func(p Packet) int64 {
			switch p.Seq {
			case 2:
				return 30
			default:
				return (int64(p.Seq) * 3) % 5
			}
		},
		To: r.Receive,
	}
	src := &Source{Sim: s, Out: link}
	for seq := uint64(0); seq < 6; seq++ {
		src.SendAt(int64(seq), Packet{Epoch: 0, Seq: seq, EventTime: int64(seq)})
	}
	s.Run(40)

	released := drain(r.Released())
	late := drain(r.Late())
	if len(released) != 5 {
		t.Fatalf("released %v, want 5 packets (seq 2 skipped)", seqs(released))
	}
	for _, p := range released {
		if p.Seq == 2 {
			t.Fatalf("skipped seq 2 was released")
		}
	}
	if got := seqs(late); !equalSeqs(got, 2) {
		t.Fatalf("late %v, want [2]", got)
	}
	// Released keys must be non-decreasing in (EventTime, Seq).
	for i := 1; i < len(released); i++ {
		if keyOf(released[i]).less(keyOf(released[i-1])) {
			t.Fatalf("release order violated at %v", seqs(released))
		}
	}
	if s.Pending() != 0 {
		t.Fatalf("%d events left pending", s.Pending())
	}
}
