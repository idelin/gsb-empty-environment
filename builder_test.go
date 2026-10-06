package viewbuilder

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func reverse(evs []Event) []Event {
	out := make([]Event, len(evs))
	for i, ev := range evs {
		out[len(evs)-1-i] = ev
	}
	return out
}

func waitForSeq(t *testing.T, b *Builder, want uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b.Seq() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("view did not reach seq %d in time, stuck at %d", want, b.Seq())
}

// The builder scans a fixed snapshot, then replays the changes that
// arrived during the scan. Out-of-order delivery must be reordered into
// the contiguous prefix, and delete events must survive the reordering.
func TestSnapshotScanThenOutOfOrderReplay(t *testing.T) {
	l := NewLog()
	l.Append("a", []byte("1")) // seq 1, in snapshot
	l.Append("b", []byte("2")) // seq 2, in snapshot
	l.SetSnapshotHook(func() {
		l.Append("c", []byte("3")) // seq 3, replay window
		l.Delete("a")              // seq 4, replay window
		l.Append("d", []byte("4")) // seq 5, replay window
	})
	l.SetJumble(reverse) // deliver the replay batch out of order

	b := NewBuilder(l, Faults{})
	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := b.Seq(); got != 5 {
		t.Fatalf("view seq = %d, want 5", got)
	}
	c := NewChecked(b, l, t.Fatalf)
	if _, ok := c.Get("a"); ok {
		t.Fatal("key a must be deleted")
	}
	if v, _ := c.Get("b"); string(v) != "2" {
		t.Fatalf("b = %q, want 2", v)
	}
	if v, _ := c.Get("c"); string(v) != "3" {
		t.Fatalf("c = %q, want 3", v)
	}
	if v, _ := c.Get("d"); string(v) != "4" {
		t.Fatalf("d = %q, want 4", v)
	}
	if n := c.Len(); n != 3 {
		t.Fatalf("Len = %d, want 3", n)
	}
	c.CheckAll()
}

// Injected build failures must not corrupt or swap the view; a later
// successful build still produces the correct state.
func TestBuildFailureInjection(t *testing.T) {
	l := NewLog()
	l.Append("k", []byte("v"))
	b := NewBuilder(l, Faults{FailBuilds: 2})
	for i := 0; i < 2; i++ {
		if err := b.Build(context.Background()); err == nil {
			t.Fatalf("attempt %d: expected injected failure", i)
		}
		if b.Seq() != 0 {
			t.Fatalf("failed build swapped the view, seq = %d", b.Seq())
		}
	}
	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("third attempt: %v", err)
	}
	c := NewChecked(b, l, t.Fatalf)
	if v, _ := c.Get("k"); string(v) != "v" {
		t.Fatalf("k = %q, want v", v)
	}
	c.CheckAll()
}

// A lost switch acknowledgement makes the log replay the unacked events
// on the next subscription. Sequence dedupe must keep the newer value
// instead of resurrecting the replayed stale one.
func TestSwitchAckLossReplayDeduped(t *testing.T) {
	l := NewLog()
	l.Append("k", []byte("v1")) // seq 1
	l.DropAcks(1)               // the first switch ack is lost

	b := NewBuilder(l, Faults{})
	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	l.Append("k", []byte("v2")) // seq 2
	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	c := NewChecked(b, l, t.Fatalf)
	if v, _ := c.Get("k"); string(v) != "v2" {
		t.Fatalf("k = %q after ack-loss replay, want v2", v)
	}
	if got := b.Seq(); got != 2 {
		t.Fatalf("view seq = %d, want 2", got)
	}
	c.CheckAll()
}

// Run follows the live stream, survives disconnects, and keeps every
// concurrent public query consistent with a full recompute.
func TestRunReconnectAndConcurrentQueries(t *testing.T) {
	l := NewLog()
	for i := 0; i < 10; i++ {
		l.Append(fmt.Sprintf("key-%d", i), []byte("base"))
	}
	l.DropAcks(1) // lose the first switch ack

	b := NewBuilder(l, Faults{FailBuilds: 1}) // first build attempt fails
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	waitForSeq(t, b, 10)

	c := NewChecked(b, l, t.Fatalf)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(r)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				c.Get(fmt.Sprintf("key-%d", rng.Intn(20)))
				c.Len()
			}
		}(r)
	}

	// Live writes, a disconnect (forcing reconnect + unacked replay),
	// then more writes including deletes.
	for i := 10; i < 20; i++ {
		l.Append(fmt.Sprintf("key-%d", i), []byte("live"))
	}
	l.closeSubs()
	for i := 0; i < 10; i++ {
		l.Append(fmt.Sprintf("key-%d", i), []byte("v2"))
	}
	for i := 15; i < 20; i++ {
		l.Delete(fmt.Sprintf("key-%d", i))
	}
	waitForSeq(t, b, l.Seq())
	close(stop)
	wg.Wait()
	cancel()

	c.CheckAll()
	for i := 15; i < 20; i++ {
		if _, ok := c.Get(fmt.Sprintf("key-%d", i)); ok {
			t.Fatalf("key-%d must be deleted", i)
		}
	}
}

// A delete that lands inside the scan window must not be dropped by the
// reorder buffer even when it arrives before the puts it follows.
func TestDeletePreservedUnderReordering(t *testing.T) {
	l := NewLog()
	l.Append("x", []byte("1")) // seq 1, snapshot
	l.SetSnapshotHook(func() {
		l.Append("y", []byte("2")) // seq 2
		l.Delete("x")              // seq 3
		l.Delete("y")              // seq 4
		l.Append("z", []byte("3")) // seq 5
	})
	l.SetJumble(reverse)

	b := NewBuilder(l, Faults{})
	if err := b.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	c := NewChecked(b, l, t.Fatalf)
	if _, ok := c.Get("x"); ok {
		t.Fatal("x must be deleted")
	}
	if _, ok := c.Get("y"); ok {
		t.Fatal("y must be deleted")
	}
	if v, _ := c.Get("z"); string(v) != "3" {
		t.Fatalf("z = %q, want 3", v)
	}
	if n := c.Len(); n != 1 {
		t.Fatalf("Len = %d, want 1", n)
	}
	c.CheckAll()
}
