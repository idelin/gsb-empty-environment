package replay

import (
	"math/bits"
	"math/rand"
	"testing"
	"time"
)

// naiveWindow is the reference anti-replay model: an explicit set of
// every sequence number still inside the window, with no bitmap tricks.
type naiveWindow struct {
	width uint64
	max   uint64
	init  bool
	seen  map[uint64]struct{}
}

func newNaiveWindow(width uint64) *naiveWindow {
	return &naiveWindow{width: width, seen: map[uint64]struct{}{}}
}

func (n *naiveWindow) check(seq uint64) bool {
	if !n.init {
		n.init = true
		n.max = seq
		n.seen[seq] = struct{}{}
		return true
	}

	if seq > n.max {
		for c := range n.seen {
			if seq-c >= n.width {
				delete(n.seen, c)
			}
		}
		n.max = seq
		n.seen[seq] = struct{}{}
		return true
	}

	if n.max-seq >= n.width {
		return false
	}
	if _, ok := n.seen[seq]; ok {
		return false
	}
	n.seen[seq] = struct{}{}
	return true
}

func countSetBits(words []uint64) int {
	total := 0
	for _, word := range words {
		total += bits.OnesCount64(word)
	}
	return total
}

// boundedRand returns a pseudo-random uint64 in [0, n). It avoids relying
// on rand.Rand methods newer than the module's go 1.22 baseline.
func boundedRand(rng *rand.Rand, n uint64) uint64 {
	if n == 0 {
		return 0
	}
	return rng.Uint64() % n
}

// TestWindowDifferentialFuzz drives the ring bitmap and the naive set
// model through identical large-span, out-of-order and replay streams and
// requires bit-identical verdicts and window contents after every event.
func TestWindowDifferentialFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(20261127))
	widths := []uint64{64, 128, 256, 2048}

	for iter := 0; iter < 200; iter++ {
		width := widths[rng.Intn(len(widths))]
		ring, err := NewWindow(width)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		naive := newNaiveWindow(width)

		seq := boundedRand(rng, 1_000_000) + 1_000
		for step := 0; step < 400; step++ {
			mode := rng.Intn(10)
			var candidate uint64
			switch {
			case mode < 3:
				candidate = seq + 1 + boundedRand(rng, width+300)
			case mode < 5:
				candidate = seq + width - 1
			case mode < 7:
				candidate = seq + width
			case mode < 9:
				if seq == 0 {
					candidate = boundedRand(rng, width)
				} else {
					back := boundedRand(rng, width)
					if back > seq {
						back = seq
					}
					candidate = seq - back
				}
			default:
				candidate = seq
			}

			got := ring.Check(candidate)
			want := naive.check(candidate)
			if got != want {
				t.Fatalf("iter %d step %d width %d seq %d candidate %d: ring=%v naive=%v",
					iter, step, width, seq, candidate, got, want)
			}
			if got {
				seq = candidate
			}

			if ring.Max() != naive.max {
				t.Fatalf("iter %d step %d: max mismatch ring=%d naive=%d", iter, step, ring.Max(), naive.max)
			}
			if countSetBits(ring.Words()) != len(naive.seen) {
				t.Fatalf("iter %d step %d: set bit count %d != naive set size %d",
					iter, step, countSetBits(ring.Words()), len(naive.seen))
			}
			for c := range naive.seen {
				if !ring.Seen(c) {
					t.Fatalf("iter %d step %d: ring missing in-window counter %d (max=%d)", iter, step, c, ring.Max())
				}
			}
			if naive.init {
				floor := naive.max - width + 1
				for probe := 0; probe < 5; probe++ {
					old := floor - 1 - boundedRand(rng, width)
					if old > floor { // unsigned underflow guard
						continue
					}
					if ring.Seen(old) {
						t.Fatalf("iter %d step %d: counter %d below floor %d reported seen", iter, step, old, floor)
					}
				}
			}
		}
	}
}

type manualClock struct {
	t time.Time
}

func (m *manualClock) now() time.Time { return m.t }

func (m *manualClock) advance(units int) {
	m.t = m.t.Add(time.Duration(units) * time.Millisecond)
}

// naiveSession mirrors Session gates using the set-based window.
type naiveSession struct {
	index               uint32
	rejectAfterMessages uint64
	rejectAfterTime     time.Duration
	createdAt           time.Time
	accepted            uint64
	window              *naiveWindow
	clock               *manualClock
}

func (n *naiveSession) process(seq uint64) RejectReason {
	if n.rejectAfterTime != 0 && n.clock.t.Sub(n.createdAt) >= n.rejectAfterTime {
		return RejectExpired
	}
	if n.rejectAfterMessages != 0 {
		if n.accepted >= n.rejectAfterMessages || seq >= ^uint64(0)-n.rejectAfterMessages+1 {
			return RejectMessageCap
		}
	}
	if !n.window.check(seq) {
		return RejectReplay
	}
	n.accepted++
	return RejectNone
}

type naiveKeyRing struct {
	previous *naiveSession
	current  *naiveSession
	next     *naiveSession
	clock    *manualClock
}

func (r *naiveKeyRing) receive(p Packet) ReceiveResult {
	if !p.Authentic {
		return ReceiveResult{Reason: RejectForge}
	}

	var sess *naiveSession
	switch {
	case r.next != nil && r.next.index == p.Index:
		sess = r.next
	case r.current != nil && r.current.index == p.Index:
		sess = r.current
	case r.previous != nil && r.previous.index == p.Index:
		sess = r.previous
	default:
		return ReceiveResult{Reason: RejectNoSession, SessionIndex: p.Index}
	}

	reason := sess.process(p.Counter)
	if reason != RejectNone {
		return ReceiveResult{Reason: reason, SessionIndex: sess.index}
	}

	result := ReceiveResult{Accepted: true, Reason: RejectNone, SessionIndex: sess.index}
	if r.next != nil && r.next.index == p.Index {
		r.previous = r.current
		r.current = r.next
		r.next = nil
		result.Promoted = true
	}
	return result
}

func (r *naiveKeyRing) slotIDs() (uint32, uint32, uint32, bool, bool, bool) {
	id := func(s *naiveSession) (uint32, bool) {
		if s == nil {
			return 0, false
		}
		return s.index, true
	}
	pi, pok := id(r.previous)
	ci, cok := id(r.current)
	ni, nok := id(r.next)
	return pi, ci, ni, pok, cok, nok
}

// TestKeyRingDifferentialFuzz replays a full rekey event stream (rekeys,
// clock advances, forgeries, replays, out-of-order counters) against both
// the real KeyRing and the naive reference and requires identical
// verdicts, promotions and generation-slot rotations.
func TestKeyRingDifferentialFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(97531))
	widths := []uint64{64, 128, 256}

	for iter := 0; iter < 300; iter++ {
		clock := &manualClock{}
		ring := NewKeyRing(clock.now)
		naive := &naiveKeyRing{clock: clock}

		width := widths[rng.Intn(len(widths))]
		var capMessages uint64
		if rng.Intn(2) == 0 {
			capMessages = 2 + boundedRand(rng, 8)
		}
		ttlUnits := []int{0, 5, 25}[rng.Intn(3)]

		cfg := func(index uint32) SessionConfig {
			return SessionConfig{
				Index:               index,
				RejectAfterMessages: capMessages,
				RejectAfterTime:     time.Duration(ttlUnits) * time.Millisecond,
				WindowWidth:         width,
				Now:                 clock.now,
			}
		}

		var indexCounter uint32 = 1
		firstIndex := indexCounter
		indexCounter++
		if _, err := ring.Bootstrap(cfg(firstIndex)); err != nil {
			t.Fatalf("iter %d bootstrap: %v", iter, err)
		}
		naive.current = &naiveSession{
			index:               firstIndex,
			rejectAfterMessages: capMessages,
			rejectAfterTime:     time.Duration(ttlUnits) * time.Millisecond,
			createdAt:           clock.t,
			window:              newNaiveWindow(width),
			clock:               clock,
		}

		counters := map[uint32]uint64{}
		history := map[uint32][]uint64{}

		compareSlots := func(stage int) {
			t.Helper()
			pi, ci, ni, pok, cok, nok := naive.slotIDs()
			if (ring.previous != nil) != pok || (ring.current != nil) != cok || (ring.next != nil) != nok {
				t.Fatalf("iter %d stage %d: slot presence mismatch", iter, stage)
			}
			if pok && ring.previous.index != pi {
				t.Fatalf("iter %d stage %d: previous index %d != naive %d", iter, stage, ring.previous.index, pi)
			}
			if cok && ring.current.index != ci {
				t.Fatalf("iter %d stage %d: current index %d != naive %d", iter, stage, ring.current.index, ci)
			}
			if nok && ring.next.index != ni {
				t.Fatalf("iter %d stage %d: next index %d != naive %d", iter, stage, ring.next.index, ni)
			}
		}

		for event := 0; event < 100; event++ {
			roll := rng.Intn(100)
			switch {
			case roll < 12 && ring.Next() == nil:
				idx := indexCounter
				indexCounter++
				if _, err := ring.CreateNext(cfg(idx)); err != nil {
					t.Fatalf("iter %d event %d create next: %v", iter, event, err)
				}
				naive.next = &naiveSession{
					index:               idx,
					rejectAfterMessages: capMessages,
					rejectAfterTime:     time.Duration(ttlUnits) * time.Millisecond,
					createdAt:           clock.t,
					window:              newNaiveWindow(width),
					clock:               clock,
				}
				counters[idx] = 0
			case roll < 24:
				clock.advance(1 + rng.Intn(8))
			default:
				live := make([]uint32, 0, 3)
				if ring.previous != nil {
					live = append(live, ring.previous.index)
				}
				if ring.current != nil {
					live = append(live, ring.current.index)
				}
				if ring.next != nil {
					live = append(live, ring.next.index)
				}
				if len(live) == 0 {
					continue
				}
				target := live[rng.Intn(len(live))]

				var counter uint64
				if rng.Intn(5) == 0 && len(history[target]) > 0 {
					counter = history[target][rng.Intn(len(history[target]))]
				} else {
					counter = counters[target]
					counters[target]++
				}

				pkt := Packet{Index: target, Counter: counter, Authentic: rng.Intn(100) >= 15}
				got := ring.Receive(pkt)
				want := naive.receive(pkt)
				if got != want {
					t.Fatalf("iter %d event %d pkt=%+v: got=%+v want=%+v", iter, event, pkt, got, want)
				}
				if got.Accepted {
					history[target] = append(history[target], counter)
				}
			}
			compareSlots(event)
		}
	}
}
