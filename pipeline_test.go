package freqsketch

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// fakeClock is a manually advanced injectable clock.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestPipeline(clk Clock, decayEvery time.Duration, budget int) *Pipeline {
	sk := NewSketch(SketchConfig{})
	return NewPipeline(PipelineConfig{
		Sketch:     sk,
		Table:      NewTable(sk, budget),
		Clock:      clk,
		DecayEvery: decayEvery,
	})
}

// TestDuplicateEventsIdempotent: redelivering an event never changes
// state after the first delivery.
func TestDuplicateEventsIdempotent(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := newTestPipeline(clk.now, time.Second, 1<<20)
	ev := Event{ID: "e1", Key: "a", Bytes: 10}
	if err := p.Process(ev); err != nil {
		t.Fatal(err)
	}
	before := p.Checkpoint()
	for i := 0; i < 5; i++ {
		if err := p.Process(ev); err != nil {
			t.Fatal(err)
		}
	}
	after := p.Checkpoint()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("duplicate deliveries mutated pipeline state")
	}
	if got := p.sketch.Estimate("a"); got != 1 {
		t.Fatalf("Estimate(a) = %d, want 1 (dedup failed)", got)
	}
}

// TestStateExplorationDuplicateInterleavings: explore the state space of
// delivery schedules — unique events in fixed logical order, duplicates
// injected at arbitrary points, decay epochs advancing between unique
// events. Every explored path must reach the identical final state.
func TestStateExplorationDuplicateInterleavings(t *testing.T) {
	uniq := []Event{
		{ID: "e1", Key: "alpha", Bytes: 20},
		{ID: "e2", Key: "beta", Bytes: 20},
		{ID: "e3", Key: "alpha", Bytes: 20},
		{ID: "e4", Key: "gamma", Bytes: 20},
		{ID: "e5", Key: "beta", Bytes: 20},
		{ID: "e6", Key: "alpha", Bytes: 20},
	}
	finalStates := map[uint64]State{}
	var explore func(delivered []Event, next int, dupsLeft int, path uint64)
	explore = func(delivered []Event, next, dupsLeft int, path uint64) {
		if next == len(uniq) {
			clk := &fakeClock{t: time.Unix(0, 0)}
			p := newTestPipeline(clk.now, 2*time.Second, 1<<20)
			// Logical time advances only on first delivery of a unique
			// event; duplicate redeliveries must not disturb decay.
			deliveredIDs := map[string]bool{}
			for _, ev := range delivered {
				if !deliveredIDs[ev.ID] {
					clk.advance(time.Second)
					deliveredIDs[ev.ID] = true
				}
				if err := p.Process(ev); err != nil {
					t.Fatal(err)
				}
			}
			finalStates[path] = p.Checkpoint()
			return
		}
		// Choice 1: deliver the next unique event.
		explore(append(delivered, uniq[next]), next+1, dupsLeft, path*2)
		// Choice 2: re-deliver any already-delivered event (bounded).
		if dupsLeft > 0 {
			for i := 0; i < next; i++ {
				dup := delivered[i]
				cp := append(append([]Event{}, delivered...), dup)
				explore(cp, next, dupsLeft-1, path*2+1)
			}
		}
	}
	explore(nil, 0, 3, 1)

	var ref State
	first := true
	for path, st := range finalStates {
		if first {
			ref, first = st, false
			continue
		}
		if !reflect.DeepEqual(ref, st) {
			t.Fatalf("path %d diverged: duplicate interleavings changed final state", path)
		}
	}
	t.Logf("explored %d delivery schedules, all converged", len(finalStates))
}

// TestCheckpointRecoveryIdenticalDecisions: restoring a checkpoint and
// replaying the remaining stream must reproduce the exact same state and
// admission decisions as an uninterrupted run.
func TestCheckpointRecoveryIdenticalDecisions(t *testing.T) {
	const n = 40
	events := make([]Event, n)
	r := rand.New(rand.NewSource(99))
	for i := range events {
		events[i] = Event{
			ID:    "ev-" + strconv.Itoa(i),
			Key:   "k" + strconv.Itoa(r.Intn(12)),
			Bytes: 10 + r.Intn(30),
		}
	}
	const split = 15

	// Uninterrupted reference run.
	clk1 := &fakeClock{t: time.Unix(0, 0)}
	p1 := newTestPipeline(clk1.now, 3*time.Second, 100)
	for i, ev := range events {
		clk1.advance(time.Second)
		if err := p1.Process(ev); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	want := p1.Checkpoint()

	// Run to split, checkpoint, crash (discard), restore into a fresh
	// pipeline, replay the rest.
	clk2 := &fakeClock{t: time.Unix(0, 0)}
	p2 := newTestPipeline(clk2.now, 3*time.Second, 100)
	for _, ev := range events[:split] {
		clk2.advance(time.Second)
		if err := p2.Process(ev); err != nil {
			t.Fatal(err)
		}
	}
	cp := p2.Checkpoint()

	clk3 := &fakeClock{t: clk2.t} // restored logical clock
	p3 := newTestPipeline(clk3.now, 3*time.Second, 100)
	p3.Restore(cp)
	for _, ev := range events[split:] {
		clk3.advance(time.Second)
		if err := p3.Process(ev); err != nil {
			t.Fatal(err)
		}
	}
	got := p3.Checkpoint()

	if !reflect.DeepEqual(want.Sketch, got.Sketch) {
		t.Fatal("sketch state diverged after checkpoint recovery")
	}
	if !reflect.DeepEqual(want.Table, got.Table) {
		t.Fatal("admission table diverged after checkpoint recovery")
	}
	if !reflect.DeepEqual(want.Seen, got.Seen) {
		t.Fatal("dedup set diverged after checkpoint recovery")
	}
	// Same admission decisions: identical table contents.
	if !reflect.DeepEqual(p1.table.Entries(), p3.table.Entries()) {
		t.Fatal("admission decisions differ after recovery")
	}
}

// TestFailureInjectionAtomic: an injected failure aborts the event
// before any state changes; the event can be retried cleanly.
func TestFailureInjectionAtomic(t *testing.T) {
	boom := errors.New("injected boom")
	var calls int
	clk := &fakeClock{t: time.Unix(0, 0)}
	sk := NewSketch(SketchConfig{})
	p := NewPipeline(PipelineConfig{
		Sketch: sk,
		Table:  NewTable(sk, 1<<20),
		Clock:  clk.now,
		Fail: func(op string) error {
			calls++
			if calls == 3 {
				return boom
			}
			return nil
		},
	})
	e1 := Event{ID: "e1", Key: "a", Bytes: 5}
	e2 := Event{ID: "e2", Key: "b", Bytes: 5}
	e3 := Event{ID: "e3", Key: "c", Bytes: 5}
	if err := p.Process(e1); err != nil {
		t.Fatal(err)
	}
	if err := p.Process(e2); err != nil {
		t.Fatal(err)
	}
	before := p.Checkpoint()
	if err := p.Process(e3); !errors.Is(err, boom) {
		t.Fatalf("want injected error, got %v", err)
	}
	mid := p.Checkpoint()
	if !reflect.DeepEqual(before, mid) {
		t.Fatal("failed event left partial state behind")
	}
	// Retry succeeds and applies exactly once.
	if err := p.Process(e3); err != nil {
		t.Fatal(err)
	}
	if got := sk.Estimate("c"); got != 1 {
		t.Fatalf("Estimate(c) = %d, want 1", got)
	}
}

// TestSourceFailure: a failing source propagates its error and leaves
// processed events intact.
func TestSourceFailure(t *testing.T) {
	srcErr := errors.New("source drained badly")
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := newTestPipeline(clk.now, 0, 1<<20)
	i := 0
	src := Source(func() (Event, bool, error) {
		i++
		if i == 3 {
			return Event{}, false, srcErr
		}
		return Event{ID: fmt.Sprintf("e%d", i), Key: "k", Bytes: 1}, true, nil
	})
	if err := p.Run(src); !errors.Is(err, srcErr) {
		t.Fatalf("want source error, got %v", err)
	}
	if got := p.sketch.Estimate("k"); got != 2 {
		t.Fatalf("Estimate(k) = %d, want 2 (events before failure kept)", got)
	}
}

// TestResourceRelease: Close releases registered resources exactly once
// and is idempotent; operations after Close fail.
func TestResourceRelease(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := newTestPipeline(clk.now, 0, 1<<20)
	released := 0
	p.OnClose(func() error { released++; return nil })
	p.OnClose(func() error { released++; return nil })
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if released != 2 {
		t.Fatalf("released = %d, want 2 (each closer exactly once)", released)
	}
	if err := p.Process(Event{ID: "x", Key: "k"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Process after Close: got %v, want ErrClosed", err)
	}
	// Closer registered after Close runs immediately.
	late := 0
	p.OnClose(func() error { late++; return nil })
	if late != 1 {
		t.Fatalf("late closer ran %d times, want 1", late)
	}
}

// TestFrequentItemsRetained: under a tight byte budget, the admission
// table keeps the genuinely frequent keys, and reported frequencies are
// upper bounds (never pretending to be exact counts).
func TestFrequentItemsRetained(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	sk := NewSketch(SketchConfig{})
	tbl := NewTable(sk, 400) // room for ~20 entries of 20 bytes
	p := NewPipeline(PipelineConfig{Sketch: sk, Table: tbl, Clock: clk.now})

	r := rand.New(rand.NewSource(2024))
	z := rand.NewZipf(r, 1.3, 1, 199)
	exact := map[string]uint64{}
	const n = 60000
	for i := 0; i < n; i++ {
		k := "item-" + strconv.FormatUint(z.Uint64(), 10)
		ev := Event{ID: "ev-" + strconv.Itoa(i), Key: k, Bytes: 20}
		if err := p.Process(ev); err != nil {
			t.Fatal(err)
		}
		exact[k]++
	}
	// Top-10 exact keys.
	type kv struct {
		k string
		v uint64
	}
	top := make([]kv, 0, len(exact))
	for k, v := range exact {
		top = append(top, kv{k, v})
	}
	for i := 0; i < len(top); i++ {
		for j := i + 1; j < len(top); j++ {
			if top[j].v > top[i].v {
				top[i], top[j] = top[j], top[i]
			}
		}
	}
	hits := 0
	for _, e := range top[:10] {
		if _, ok := tbl.Get(e.k); ok {
			hits++
		}
	}
	if hits < 8 {
		t.Fatalf("only %d/10 hottest keys retained, want >= 8", hits)
	}
	// Reported frequencies never understate the exact count.
	for _, e := range tbl.Entries() {
		if e.Freq < exact[e.Key] {
			t.Fatalf("entry %s reports %d < exact %d", e.Key, e.Freq, exact[e.Key])
		}
	}
	t.Logf("retained %d/10 hottest keys under tight budget", hits)
}
