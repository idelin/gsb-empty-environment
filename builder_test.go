package viewbuilder

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// checkViewAgainstOracle compares every public query of the view
// against a full recomputation of the source at the view's watermark.
func checkViewAgainstOracle(t *testing.T, src *Source, v *View) {
	t.Helper()
	want := src.RecomputeAt(v.Seq())

	if got := v.Len(); got != len(want) {
		t.Fatalf("Len()=%d, oracle=%d at seq=%d", got, len(want), v.Seq())
	}
	for k, wv := range want {
		gv, ok := v.Get(k)
		if !ok || gv != wv {
			t.Fatalf("Get(%q)=(%q,%v), oracle=(%q,true) at seq=%d", k, gv, ok, wv, v.Seq())
		}
	}
	// Missing keys must be absent.
	for i := 0; i < 5; i++ {
		k := fmt.Sprintf("absent-%d", i)
		if _, ok := v.Get(k); ok {
			if _, inOracle := want[k]; !inOracle {
				t.Fatalf("Get(%q) present in view but not in oracle at seq=%d", k, v.Seq())
			}
		}
	}
	keys := v.Keys()
	sorted := make([]string, 0, len(want))
	for k := range want {
		sorted = append(sorted, k)
	}
	if len(keys) != len(sorted) {
		t.Fatalf("Keys() len=%d, oracle=%d at seq=%d", len(keys), len(sorted), v.Seq())
	}
	ranged := make(map[string]string)
	v.Range(func(k, val string) bool { ranged[k] = val; return true })
	if !reflect.DeepEqual(ranged, want) {
		t.Fatalf("Range() mismatch with oracle at seq=%d", v.Seq())
	}
}

func TestSnapshotScanThenReplay(t *testing.T) {
	src := NewSource()
	for i := 0; i < 10; i++ {
		src.Put(fmt.Sprintf("k%d", i), "v0")
	}
	// Changes committed during the scan window: after the snapshot
	// point the builder must replay them before switching.
	b := NewBuilder(src, Faults{AfterScan: func() {
		src.Put("k0", "v1")
		src.Delete("k1")
		src.Put("new", "n1")
	}})
	switchSeq := uint64(13) // 10 seed puts + 3 changes above

	got, err := b.Rebuild()
	if err != nil {
		t.Fatal(err)
	}
	if got != switchSeq {
		t.Fatalf("switch seq=%d, want %d", got, switchSeq)
	}
	if b.View().Seq() != switchSeq {
		t.Fatalf("view watermark=%d, want %d", b.View().Seq(), switchSeq)
	}
	checkViewAgainstOracle(t, src, b.View())
	if b.View().Generation() != 1 {
		t.Fatalf("generation=%d, want 1", b.View().Generation())
	}
}

func TestReorderBufferContiguousPrefix(t *testing.T) {
	mk := func(seq uint64) Event { return Event{Seq: seq, Key: fmt.Sprintf("k%d", seq), Op: OpPut} }
	buf := newReorderBuffer(1)

	// Out of order with a gap at 2: only event 1 may be released.
	buf.add(mk(3))
	buf.add(mk(1))
	buf.add(mk(4))
	got := buf.drain()
	if len(got) != 1 || got[0].Seq != 1 {
		t.Fatalf("drain released %v, want only seq 1", got)
	}
	// Gap still open: nothing more.
	if got := buf.drain(); len(got) != 0 {
		t.Fatalf("drain released %v across a gap", got)
	}
	// Filling the gap releases the whole contiguous prefix.
	buf.add(mk(2))
	got = buf.drain()
	if len(got) != 3 || got[0].Seq != 2 || got[2].Seq != 4 {
		t.Fatalf("drain after fill released %v, want seqs 2,3,4", got)
	}
	// Duplicates and stale events are ignored.
	buf.add(mk(3))
	buf.add(mk(5))
	buf.add(mk(5))
	if got := buf.drain(); len(got) != 1 || got[0].Seq != 5 {
		t.Fatalf("drain released %v, want only seq 5", got)
	}
}

func TestDeleteEventsRetained(t *testing.T) {
	// The reorder buffer must retain delete events, not compact them away.
	buf := newReorderBuffer(1)
	buf.add(Event{Seq: 1, Key: "a", Op: OpPut, Value: "1"})
	buf.add(Event{Seq: 2, Key: "a", Op: OpDelete})
	got := buf.drain()
	if len(got) != 2 || got[1].Op != OpDelete {
		t.Fatalf("delete event not retained: %v", got)
	}

	src := NewSource()
	src.Put("stale", "x") // present in the snapshot
	src.Put("gone", "y")  // put then deleted during the scan window
	src.Delete("gone")
	src.Delete("stale")
	src.Put("fresh", "z")

	b := NewBuilder(src, Faults{})
	if _, err := b.Rebuild(); err != nil {
		t.Fatal(err)
	}
	v := b.View()
	if _, ok := v.Get("gone"); ok {
		t.Fatal("deleted key 'gone' survived replay")
	}
	if _, ok := v.Get("stale"); ok {
		t.Fatal("deleted snapshot key 'stale' survived replay")
	}
	checkViewAgainstOracle(t, src, v)
}

func TestOutOfOrderDelivery(t *testing.T) {
	src := NewSource()
	src.Put("base", "0")
	// Reverse every delivery batch to simulate out-of-order arrival.
	src.SetShuffle(func(batch []Event) {
		for i, j := 0, len(batch)-1; i < j; i, j = i+1, j-1 {
			batch[i], batch[j] = batch[j], batch[i]
		}
	})
	b := NewBuilder(src, Faults{})
	for i := 0; i < 50; i++ {
		src.Put(fmt.Sprintf("k%02d", i), "v")
		if i%3 == 0 {
			src.Delete(fmt.Sprintf("k%02d", i))
		}
	}
	if _, err := b.Rebuild(); err != nil {
		t.Fatal(err)
	}
	checkViewAgainstOracle(t, src, b.View())

	// Live follow under shuffled delivery too.
	for i := 50; i < 80; i++ {
		src.Put(fmt.Sprintf("k%02d", i), "v")
	}
	if _, err := b.CatchUp(); err != nil {
		t.Fatal(err)
	}
	checkViewAgainstOracle(t, src, b.View())
}

func TestAtomicSwitch(t *testing.T) {
	src := NewSource()
	src.Put("a", "1")
	b := NewBuilder(src, Faults{})
	if _, err := b.Rebuild(); err != nil {
		t.Fatal(err)
	}
	old := b.View()

	for i := 0; i < 20; i++ {
		src.Put(fmt.Sprintf("b%d", i), "x")
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	// Readers must always see a self-consistent view: its contents must
	// equal a full recomputation at its own watermark, never a torn mix.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				v := b.View()
				want := src.RecomputeAt(v.Seq())
				if v.Len() != len(want) {
					t.Errorf("torn view: len=%d oracle=%d seq=%d", v.Len(), len(want), v.Seq())
					return
				}
			}
		}()
	}
	if _, err := b.Rebuild(); err != nil {
		t.Fatal(err)
	}
	close(done)
	wg.Wait()

	if b.View() == old {
		t.Fatal("serving view was not replaced")
	}
	if old.Seq() != 1 {
		t.Fatalf("old view mutated after switch: seq=%d", old.Seq())
	}
	checkViewAgainstOracle(t, src, b.View())
}

func TestBuildFailureInjection(t *testing.T) {
	src := NewSource()
	src.Put("a", "1")
	b := NewBuilder(src, Faults{})
	if _, err := b.Rebuild(); err != nil {
		t.Fatal(err)
	}
	base := b.View()

	src.Put("a", "2")
	src.Delete("missing")
	src.Put("c", "3")

	fail := errors.New("injected")
	// Fail every phase a few times; the old view must keep serving.
	for _, phase := range []string{"scan", "replay", "swap"} {
		attempts := 0
		f := Faults{}
		switch phase {
		case "scan":
			f.FailScan = func() error {
				attempts++
				if attempts <= 2 {
					return fail
				}
				return nil
			}
		case "replay":
			f.FailReplay = func(seq uint64) error {
				attempts++
				if attempts <= 2 {
					return fail
				}
				return nil
			}
			// Commit changes inside the scan window on every attempt
			// so the replay phase actually has events to apply.
			n := 0
			f.AfterScan = func() {
				n++
				src.Put(fmt.Sprintf("during-scan-%d", n), "x")
				src.Delete("a")
			}
		case "swap":
			f.FailSwap = func() error {
				attempts++
				if attempts <= 2 {
					return fail
				}
				return nil
			}
		}
		fb := NewBuilder(src, f)
		fb.live.Store(base)
		for i := 0; i < 2; i++ {
			if _, err := fb.Rebuild(); err != fail {
				t.Fatalf("phase %s attempt %d: err=%v, want injected", phase, i, err)
			}
			if fb.View() != base {
				t.Fatalf("phase %s: view switched despite failed build", phase)
			}
		}
		if _, err := fb.Rebuild(); err != nil {
			t.Fatalf("phase %s: retry failed: %v", phase, err)
		}
		checkViewAgainstOracle(t, src, fb.View())
	}
}

func TestLostSwitchAck(t *testing.T) {
	src := NewSource()
	src.Put("a", "1")

	lost := 0
	src.SetDropAck(func(Ack) bool {
		lost++
		return lost <= 3 // first three acks lost in transit
	})
	b := NewBuilder(src, Faults{})
	switchSeq, err := b.Rebuild()
	if err != nil {
		t.Fatal(err)
	}
	if b.AcksLost != 3 || b.AcksSent != 4 {
		t.Fatalf("acks sent=%d lost=%d, want 4/3", b.AcksSent, b.AcksLost)
	}
	seq, ok := src.AckedSeq(1)
	if !ok || seq != switchSeq {
		t.Fatalf("acked seq=(%d,%v), want (%d,true)", seq, ok, switchSeq)
	}
	if src.AckCount() != 1 {
		t.Fatalf("source recorded %d acks, want exactly 1", src.AckCount())
	}
}

func TestAckIdempotent(t *testing.T) {
	src := NewSource()
	ack := Ack{Generation: 7, Seq: 42}
	if !src.SendAck(ack) || !src.SendAck(ack) {
		t.Fatal("ack delivery failed")
	}
	if src.AckCount() != 2 {
		t.Fatalf("physical acks=%d, want 2", src.AckCount())
	}
	seq, ok := src.AckedSeq(7)
	if !ok || seq != 42 {
		t.Fatalf("acked seq=(%d,%v), want (42,true)", seq, ok)
	}
}

func TestRandomizedWithOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	src := NewSource()
	src.SetShuffle(func(batch []Event) {
		rng.Shuffle(len(batch), func(i, j int) { batch[i], batch[j] = batch[j], batch[i] })
	})

	fail := errors.New("injected")
	var faults Faults
	b := NewBuilder(src, faults)

	keys := make([]string, 12)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
	}

	for round := 0; round < 60; round++ {
		// Random mutations.
		for i := 0; i < rng.Intn(8); i++ {
			k := keys[rng.Intn(len(keys))]
			if rng.Intn(3) == 0 {
				src.Delete(k)
			} else {
				src.Put(k, fmt.Sprintf("r%d-%d", round, i))
			}
		}
		// Randomly inject a one-shot build failure for this round.
		faults = Faults{}
		switch rng.Intn(4) {
		case 0:
			used := false
			faults.FailScan = func() error {
				if !used {
					used = true
					return fail
				}
				return nil
			}
		case 1:
			used := false
			faults.FailSwap = func() error {
				if !used {
					used = true
					return fail
				}
				return nil
			}
		}
		b.faults = faults

		if rng.Intn(2) == 0 {
			if _, err := b.Rebuild(); err != nil && err != fail {
				t.Fatalf("round %d: rebuild: %v", round, err)
			}
		}
		if _, err := b.CatchUp(); err != nil {
			t.Fatalf("round %d: catchup: %v", round, err)
		}
		// Every public query is checked against a full recomputation.
		checkViewAgainstOracle(t, src, b.View())
	}

	if b.View().Seq() != src.LatestSeq() {
		t.Fatalf("view watermark=%d, source latest=%d", b.View().Seq(), src.LatestSeq())
	}
	checkViewAgainstOracle(t, src, b.View())
}
