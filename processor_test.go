package decayfreq

import (
	"bytes"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// fakeSource returns injectable Next/Close functions over a fixed event list.
func fakeSource(events []Event) (next func() (Event, bool), closed *int, closeFn func() error) {
	i := 0
	n := 0
	closed = &n
	return func() (Event, bool) {
		if i >= len(events) {
			return Event{}, false
		}
		e := events[i]
		i++
		return e, true
	}, closed, func() error { n++; return nil }
}

func testConfig() Config {
	return Config{Rows: 4, Width: 256, ByteQuota: 64, DecayInterval: time.Minute}
}

// TestDuplicateEventsIdempotent: reprocessing duplicate event IDs must not
// change estimates, cache state, or decisions.
func TestDuplicateEventsIdempotent(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	var events []Event
	for i := 0; i < 500; i++ {
		events = append(events, Event{ID: itoa(i), Key: itoa(rng.Intn(30)), Size: 1 + rng.Intn(16)})
	}
	// Interleave duplicates.
	var withDups []Event
	for _, e := range events {
		withDups = append(withDups, e)
		if rng.Intn(3) == 0 {
			withDups = append(withDups, e) // exact duplicate
		}
	}
	run := func(ev []Event) (*Processor, []bool) {
		next, _, closeFn := fakeSource(ev)
		p := NewProcessor(testConfig(), Deps{Next: next, Close: closeFn})
		if err := p.Run(); err != nil {
			t.Fatal(err)
		}
		return p, nil
	}
	p1, _ := run(events)
	p2, _ := run(withDups)
	for k := 0; k < 30; k++ {
		key := itoa(k)
		if a, b := p1.Sketch().Estimate(key), p2.Sketch().Estimate(key); a != b {
			t.Fatalf("key %q: estimate %d without dups != %d with dups", key, a, b)
		}
	}
	if !reflect.DeepEqual(p1.Cache().Snapshot(), p2.Cache().Snapshot()) {
		t.Fatal("cache state differs after duplicate events")
	}
}

// TestPeriodicDecayWithInjectedClock: decay fires once per elapsed interval
// of the fake clock, and only then.
func TestPeriodicDecayWithInjectedClock(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	events := []Event{{ID: "1", Key: "k", Size: 1}}
	next, _, closeFn := fakeSource(events)
	cfg := testConfig()
	p := NewProcessor(cfg, Deps{Next: next, Now: clock, Close: closeFn})
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if got := p.Sketch().Estimate("k"); got != 1 {
		t.Fatalf("estimate %d, want 1 (no interval elapsed)", got)
	}
	// Advance 2.5 intervals: exactly 2 decays.
	now = now.Add(150 * time.Second)
	p.maybeDecay()
	if got := p.Sketch().Estimate("k"); got != 0 {
		t.Fatalf("estimate %d, want 0 after 2 decays of count 1", got)
	}
	if epoch := p.Sketch().Epoch(); epoch != 2 {
		t.Fatalf("epoch %d, want 2", epoch)
	}
}

// TestResourceRelease: Close releases the injected resource exactly once.
func TestResourceRelease(t *testing.T) {
	next, closed, closeFn := fakeSource(nil)
	p := NewProcessor(testConfig(), Deps{Next: next, Close: closeFn})
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if *closed != 1 {
		t.Fatalf("resource closed %d times, want exactly 1", *closed)
	}
}

// TestCheckpointFailureInjection: an injected failure aborts the checkpoint
// without writing anything or mutating processor state.
func TestCheckpointFailureInjection(t *testing.T) {
	next, _, closeFn := fakeSource([]Event{{ID: "1", Key: "a", Size: 4}})
	p := NewProcessor(testConfig(), Deps{
		Next:  next,
		Close: closeFn,
		Fail:  func(op string) bool { return op == "checkpoint" },
	})
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := p.Checkpoint(&buf); err == nil {
		t.Fatal("expected injected checkpoint failure")
	}
	if buf.Len() != 0 {
		t.Fatal("failed checkpoint must not write bytes")
	}
	// State is intact and a later checkpoint (failure disabled) succeeds.
	p.deps.Fail = nil
	if err := p.Checkpoint(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("checkpoint after recovery wrote nothing")
	}
	if got := p.Sketch().Estimate("a"); got != 1 {
		t.Fatalf("estimate %d after failed checkpoint, want 1", got)
	}
}

// TestCheckpointRestoreDecisionsIdentical: replaying the same events from a
// restored checkpoint must produce identical admission decisions and state.
func TestCheckpointRestoreDecisionsIdentical(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	var pre, post []Event
	for i := 0; i < 300; i++ {
		e := Event{ID: itoa(i), Key: itoa(rng.Intn(20)), Size: 1 + rng.Intn(12)}
		if i < 150 {
			pre = append(pre, e)
		} else {
			post = append(post, e)
		}
	}
	// Include duplicates of pre-checkpoint events in post: dedup state must
	// survive the checkpoint round trip.
	post = append(post, pre[0], pre[1])

	cfg := testConfig()
	mkDeps := func() Deps {
		return Deps{Next: func() (Event, bool) { return Event{}, false }}
	}

	// Original run: pre -> checkpoint -> post.
	p1 := NewProcessor(cfg, mkDeps())
	for _, e := range pre {
		p1.Process(e)
	}
	var buf bytes.Buffer
	if err := p1.Checkpoint(&buf); err != nil {
		t.Fatal(err)
	}
	var dec1 []bool
	for _, e := range post {
		admitted, dup := p1.Process(e)
		if !dup {
			dec1 = append(dec1, admitted)
		}
	}

	// Restored run: checkpoint -> same post events.
	p2, err := RestoreProcessor(cfg, mkDeps(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	var dec2 []bool
	for _, e := range post {
		admitted, dup := p2.Process(e)
		if !dup {
			dec2 = append(dec2, admitted)
		}
	}
	if !reflect.DeepEqual(dec1, dec2) {
		t.Fatalf("decisions differ after restore:\n%v\n%v", dec1, dec2)
	}
	for k := 0; k < 20; k++ {
		key := itoa(k)
		if a, b := p1.Sketch().Estimate(key), p2.Sketch().Estimate(key); a != b {
			t.Fatalf("key %q: estimate %d != restored %d", key, a, b)
		}
	}
	if !reflect.DeepEqual(p1.Cache().Snapshot(), p2.Cache().Snapshot()) {
		t.Fatal("cache state differs after restore")
	}
}

// TestCheckpointCorruption: a truncated checkpoint must fail to restore.
func TestCheckpointCorruption(t *testing.T) {
	p := NewProcessor(testConfig(), Deps{})
	p.Process(Event{ID: "1", Key: "a", Size: 1})
	var buf bytes.Buffer
	if err := p.Checkpoint(&buf); err != nil {
		t.Fatal(err)
	}
	truncated := buf.Bytes()[:buf.Len()/2]
	if _, err := RestoreProcessor(testConfig(), Deps{}, bytes.NewReader(truncated)); err == nil {
		t.Fatal("expected error restoring truncated checkpoint")
	}
}
