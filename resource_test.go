package suspense

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// recorder collects every downstream snapshot in order.
type recorder[R any] struct {
	mu    sync.Mutex
	snaps []Snapshot[R]
}

func (rc *recorder[R]) fn() func(Snapshot[R]) {
	return func(s Snapshot[R]) {
		rc.mu.Lock()
		rc.snaps = append(rc.snaps, s)
		rc.mu.Unlock()
	}
}

func (rc *recorder[R]) all() []Snapshot[R] {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	out := make([]Snapshot[R], len(rc.snaps))
	copy(out, rc.snaps)
	return out
}

// TestRaceDiscardsStalePromise is the acceptance scenario:
// query params change A -> B in rapid succession; request A is still in
// flight (and deliberately ignores cancellation, simulating an old promise
// that resolves late). The system must discard A's late result and settle
// on B's result downstream.
func TestRaceDiscardsStalePromise(t *testing.T) {
	src := NewSignal("")

	started := make(chan string, 4)
	releaseA := make(chan struct{})

	fetch := func(ctx context.Context, q string) (string, error) {
		started <- q
		if q == "A" {
			// Old promise: resolves late, ignoring cancellation.
			<-releaseA
			return "result-A", nil
		}
		return "result-" + q, nil
	}

	res := NewResource(context.Background(), src, fetch)
	defer res.Close()
	rec := &recorder[string]{}
	succeeded := make(chan Snapshot[string], 8)
	res.Subscribe(func(s Snapshot[string]) {
		rec.fn()(s)
		if s.State == Success {
			succeeded <- s
		}
	})

	// Initial fetch for "" fires; let it finish.
	<-started
	res.Wait()
	// Drain the initial request's success so the race below is observed cleanly.
drain:
	for {
		select {
		case <-succeeded:
		default:
			break drain
		}
	}

	// Rapid consecutive edits: A then B.
	src.Set("A")
	if got := <-started; got != "A" {
		t.Fatalf("expected request A to start, got %q", got)
	}
	// A is in flight; user changes the query to B before A resolves.
	src.Set("B")
	if got := <-started; got != "B" {
		t.Fatalf("expected request B to start, got %q", got)
	}

	// B resolves first; downstream must settle on B's result.
	select {
	case s := <-succeeded:
		if s.Value != "result-B" {
			t.Fatalf("first success after race = %q, want result-B", s.Value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for B's result")
	}

	// Only now does the stale promise A resolve; it must be discarded.
	close(releaseA)
	res.Wait()

	final := res.Snapshot()
	if final.State != Success || final.Value != "result-B" {
		t.Fatalf("final state = %v (%q), want Success(result-B)", final.State, final.Value)
	}

	// Downstream must never have observed A's stale success.
	var pendings int
	for _, s := range rec.all() {
		if s.State == Success && s.Value == "result-A" {
			t.Fatalf("stale result-A leaked downstream: %+v", rec.all())
		}
		if s.State == Pending {
			pendings++
		}
	}
	if pendings < 2 { // A + B (the initial Pending predates subscription)
		t.Fatalf("expected >=2 Pending beacons, got %d: %+v", pendings, rec.all())
	}
}

// TestPendingBeaconPrecedesData verifies the Pending beacon is propagated
// downstream before data is ready.
func TestPendingBeaconPrecedesData(t *testing.T) {
	src := NewSignal("init")
	fetch := func(ctx context.Context, q string) (string, error) {
		time.Sleep(10 * time.Millisecond)
		return "data:" + q, nil
	}
	res := NewResource(context.Background(), src, fetch)
	defer res.Close()
	rec := &recorder[string]{}
	res.Subscribe(rec.fn())

	src.Set("next")
	// Synchronously after Set, the resource must already be Pending.
	if s := res.Snapshot(); s.State != Pending {
		t.Fatalf("immediately after Set, state = %v, want Pending", s.State)
	}
	res.Wait()

	snaps := rec.all()
	if len(snaps) < 2 {
		t.Fatalf("want >=2 snapshots, got %+v", snaps)
	}
	last := snaps[len(snaps)-1]
	if last.State != Success || last.Value != "data:next" {
		t.Fatalf("last snapshot = %+v, want Success(data:next)", last)
	}
	// Every Success must be preceded by a Pending for the same epoch.
	seenPending := map[uint64]bool{}
	for _, s := range snaps {
		if s.State == Pending {
			seenPending[s.Epoch] = true
		}
		if s.State == Success && !seenPending[s.Epoch] {
			t.Fatalf("Success without preceding Pending beacon: %+v", snaps)
		}
	}
}

// TestErrorTransition verifies Pending -> Error propagation.
func TestErrorTransition(t *testing.T) {
	boom := errors.New("boom")
	src := NewSignal("q")
	fetch := func(ctx context.Context, q string) (string, error) {
		return "", boom
	}
	res := NewResource(context.Background(), src, fetch)
	defer res.Close()
	res.Wait()

	s := res.Snapshot()
	if s.State != Error || !errors.Is(s.Err, boom) {
		t.Fatalf("state = %v err = %v, want Error(boom)", s.State, s.Err)
	}
}

// TestStaleRequestContextCancelled verifies the previous request's context
// is cancelled as soon as a newer request is issued.
func TestStaleRequestContextCancelled(t *testing.T) {
	src := NewSignal("")
	firstCtx := make(chan context.Context, 1)
	release := make(chan struct{})
	fetch := func(ctx context.Context, q string) (string, error) {
		if q == "A" {
			firstCtx <- ctx
			<-release
			return "", ctx.Err()
		}
		return q, nil
	}
	res := NewResource(context.Background(), src, fetch)
	defer res.Close()
	res.Wait()

	src.Set("A")
	ctxA := <-firstCtx
	src.Set("B")

	select {
	case <-ctxA.Done():
		// cancelled as expected
	case <-time.After(time.Second):
		t.Fatal("stale request context was not cancelled")
	}
	close(release)
	res.Wait()
	if s := res.Snapshot(); s.State != Success || s.Value != "B" {
		t.Fatalf("final = %+v, want Success(B)", s)
	}
}

// TestRapidFireSets hammers the source with many rapid updates and checks
// the resource converges to the last query's result.
func TestRapidFireSets(t *testing.T) {
	src := NewSignal(0)
	fetch := func(ctx context.Context, n int) (int, error) {
		time.Sleep(time.Millisecond) // simulated latency
		return n * 10, nil
	}
	res := NewResource(context.Background(), src, fetch)
	defer res.Close()

	const N = 50
	for i := 1; i <= N; i++ {
		src.Set(i)
	}
	res.Wait()

	if s := res.Snapshot(); s.State != Success || s.Value != N*10 {
		t.Fatalf("final = %+v, want Success(%d)", s, N*10)
	}
}

// TestSubscriberUnsubscribe verifies unsubscribed observers stop receiving
// transitions.
func TestSubscriberUnsubscribe(t *testing.T) {
	src := NewSignal("x")
	fetch := func(ctx context.Context, q string) (string, error) { return q, nil }
	res := NewResource(context.Background(), src, fetch)
	defer res.Close()

	rec := &recorder[string]{}
	unsub := res.Subscribe(rec.fn())
	unsub()
	src.Set("y")
	res.Wait()
	if got := len(rec.all()); got != 0 {
		t.Fatalf("unsubscribed recorder got %d snapshots", got)
	}
}

// TestStateString checks the state names used in logs and UIs.
func TestStateString(t *testing.T) {
	want := map[State]string{Pending: "Pending", Success: "Success", Error: "Error"}
	for s, w := range want {
		if fmt.Sprint(s) != w {
			t.Fatalf("State(%d).String() = %q, want %q", int(s), s, w)
		}
	}
}
