package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func parseEvents(t *testing.T, src string) []rawEvent {
	t.Helper()
	var events []rawEvent
	if err := json.Unmarshal([]byte(src), &events); err != nil {
		t.Fatalf("parse events: %v", err)
	}
	return events
}

func TestBasicAckAndFreeze(t *testing.T) {
	events := parseEvents(t, `[
		["lease", 0, 10, 1],
		["ack", 0, 5, 1],
		["ack", 5, 10, 1],
		["lease", 0, 10, 2],
		["ack", 0, 3, 2]
	]`)
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{0, 5, 10, 10, 10}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestStaleAckAndPartialReLease(t *testing.T) {
	events := parseEvents(t, `[
		["lease", 0, 10, 1],
		["ack", 0, 5, 1],
		["lease", 5, 10, 2],
		["ack", 5, 10, 1],
		["ack", 5, 8, 2]
	]`)
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{0, 5, 5, 5, 8}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCrossHoleAdvance(t *testing.T) {
	events := parseEvents(t, `[
		["lease", 0, 20, 1],
		["ack", 0, 5, 1],
		["ack", 10, 15, 1],
		["ack", 5, 10, 1],
		["ack", 15, 18, 1]
	]`)
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{0, 5, 5, 15, 18}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestOverlappingLeaseKeepsHigherGeneration(t *testing.T) {
	events := parseEvents(t, `[
		["lease", 0, 100, 5],
		["lease", 50, 100, 3],
		["lease", 75, 100, 8],
		["ack", 0, 100, 5],
		["ack", 75, 100, 8],
		["ack", 0, 75, 5]
	]`)
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{0, 0, 0, 75, 100, 100}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestLargeCoordinates(t *testing.T) {
	const big = int64(1_000_000_000_000)
	events := parseEvents(t, `[
		["lease", 0, 1000000000000, 7],
		["ack", 0, 500000000000, 7],
		["ack", 500000000000, 1000000000000, 7]
	]`)
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{0, big / 2, big}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if countNodes(newShard().root) != 1 {
		t.Fatal("fresh shard must hold a single segment")
	}
}

func TestStaleLeaseDoesNotResetAcks(t *testing.T) {
	events := parseEvents(t, `[
		["lease", 0, 10, 3],
		["ack", 0, 10, 3],
		["lease", 0, 10, 2],
		["lease", 0, 5, 3]
	]`)
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{0, 10, 10, 10}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCLIEndToEnd(t *testing.T) {
	in := bytes.NewBufferString(`[["lease",0,4,1],["ack",0,4,1]]`)
	var out bytes.Buffer
	if err := run(in, &out); err != nil {
		t.Fatal(err)
	}
	var got []int64
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want := []int64{0, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	var empty bytes.Buffer
	if err := run(bytes.NewBufferString(`[]`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.String() != "[]\n" {
		t.Fatalf("empty input got %q", empty.String())
	}
}

// bruteReplay models every offset explicitly over a small domain and is the
// reference oracle for the segment-based implementation.
func bruteReplay(events []rawEvent, domain int64) []int64 {
	gen := make([]int64, domain)
	acked := make([]bool, domain)
	committed := int64(0)
	breakpoints := make([]int64, 0, len(events))
	for _, ev := range events {
		var kind string
		var l, r, g int64
		_ = json.Unmarshal(ev[0], &kind)
		_ = json.Unmarshal(ev[1], &l)
		_ = json.Unmarshal(ev[2], &r)
		_ = json.Unmarshal(ev[3], &g)
		for x := l; x < r; x++ {
			if x < committed {
				continue
			}
			switch kind {
			case "lease":
				if gen[x] < g {
					gen[x] = g
					acked[x] = false
				}
			case "ack":
				if gen[x] == g {
					acked[x] = true
				}
			}
		}
		for committed < domain && acked[committed] {
			committed++
		}
		breakpoints = append(breakpoints, committed)
	}
	return breakpoints
}

type lcg struct{ state uint64 }

func (r *lcg) next() uint64 {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return r.state
}

func (r *lcg) intn(n int64) int64 {
	return int64(r.next() % uint64(n))
}

func countNodes(t *node) int {
	if t == nil {
		return 0
	}
	return 1 + countNodes(t.left) + countNodes(t.right)
}

func TestDifferentialAgainstBruteForce(t *testing.T) {
	const domain = int64(37)
	const eventsPerTrial = 400
	for trial := uint64(1); trial <= 300; trial++ {
		rng := lcg{state: trial * 1_000_003}
		events := make([]rawEvent, 0, eventsPerTrial)
		maxGen := int64(1)
		for i := 0; i < eventsPerTrial; i++ {
			kind := "lease"
			if rng.next()%2 == 0 {
				kind = "ack"
			}
			l := rng.intn(domain)
			r := l + 1 + rng.intn(domain-l)
			g := maxGen
			if kind == "lease" && rng.next()%4 == 0 {
				g = 1 + rng.intn(maxGen+2)
				if g > maxGen {
					maxGen = g
				}
			}
			raw, _ := json.Marshal([]any{kind, l, r, g})
			var ev rawEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatal(err)
			}
			events = append(events, ev)
		}

		got, err := replay(events)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		want := bruteReplay(events, domain)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: got %v want %v", trial, got, want)
		}

		// Replay once more on a live shard to inspect structural invariants.
		s := newShard()
		for _, ev := range events {
			var kind string
			var l, r, g int64
			_ = json.Unmarshal(ev[0], &kind)
			_ = json.Unmarshal(ev[1], &l)
			_ = json.Unmarshal(ev[2], &r)
			_ = json.Unmarshal(ev[3], &g)
			if kind == "lease" {
				s.lease(l, r, g)
			} else {
				s.ack(l, r, g)
			}
			s.advance()
		}
		var segs []*node
		collect(s.root, &segs)
		if len(segs) == 0 || segs[0].start != 0 {
			t.Fatalf("trial %d: coverage must start at 0", trial)
		}
		prevEnd := int64(0)
		for _, seg := range segs {
			if seg.start != prevEnd || seg.end <= seg.start {
				t.Fatalf("trial %d: non-contiguous segment", trial)
			}
			prevEnd = seg.end
		}
		if prevEnd != infinity {
			t.Fatalf("trial %d: coverage must reach infinity", trial)
		}
		if countNodes(s.root) > 2*eventsPerTrial+2 {
			t.Fatalf("trial %d: segment count grew linearly in offsets", trial)
		}
	}
}

func TestLargeScaleDoesNotAllocatePerOffset(t *testing.T) {
	const n = 20_000
	const maxCoord = int64(1_000_000_000_000)
	rng := lcg{state: 42}
	events := make([]rawEvent, 0, n)
	maxGen := int64(1)
	for i := 0; i < n; i++ {
		kind := "lease"
		if rng.next()%2 == 0 {
			kind = "ack"
		}
		l := rng.intn(maxCoord)
		r := l + 1 + rng.intn(maxCoord-l)
		g := maxGen
		if kind == "lease" {
			g = 1 + rng.intn(maxGen+3)
			if g > maxGen {
				maxGen = g
			}
		}
		raw, _ := json.Marshal([]any{kind, l, r, g})
		var ev rawEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		events = append(events, ev)
	}

	start := time.Now()
	breakpoints, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(breakpoints) != n {
		t.Fatalf("got %d breakpoints want %d", len(breakpoints), n)
	}

	s := newShard()
	for _, ev := range events {
		var kind string
		var l, r, g int64
		_ = json.Unmarshal(ev[0], &kind)
		_ = json.Unmarshal(ev[1], &l)
		_ = json.Unmarshal(ev[2], &r)
		_ = json.Unmarshal(ev[3], &g)
		if kind == "lease" {
			s.lease(l, r, g)
		} else {
			s.ack(l, r, g)
		}
		s.advance()
	}
	nodes := countNodes(s.root)
	if nodes > 4*n+2 {
		t.Fatalf("node count %d exceeds linear bound", nodes)
	}
	t.Logf("replayed %d events at coords up to 1e12 in %v, live nodes=%d", n, time.Since(start), nodes)
}
