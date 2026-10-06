package eventbus

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// collect reads n events from s or fails the test.
func collect(t *testing.T, s *Subscription, n int) []Event {
	t.Helper()
	out := make([]Event, 0, n)
	deadline := time.After(5 * time.Second)
	for len(out) < n {
		select {
		case ev, ok := <-s.C():
			if !ok {
				t.Fatalf("channel closed after %d/%d events, err=%v", len(out), n, s.Err())
			}
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("timed out after %d/%d events", len(out), n)
		}
	}
	return out
}

// waitBuffered waits until n events sit in the subscription buffer, i.e.
// the pump has delivered n events that the test consumer has not read.
func waitBuffered(t *testing.T, s *Subscription, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.C()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d buffered events, have %d", n, len(s.C()))
		}
		time.Sleep(time.Millisecond)
	}
}

// expectSlice asserts got matches the want history slice seq-for-seq.
func expectSlice(t *testing.T, got []Event, want []Event) {
	t.Helper()
	if uint64(len(got)) != uint64(len(want)) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Seq != want[i].Seq || got[i].Data != want[i].Data {
			t.Fatalf("position %d: got (%d,%v), want (%d,%v)",
				i, got[i].Seq, got[i].Data, want[i].Seq, want[i].Data)
		}
	}
}

// TestReplayThenLiveJunction subscribes into the middle of the history and
// keeps publishing while the replay drains; the delivered stream must equal
// the full history slice after the subscription point, with no gap or dup.
func TestReplayThenLiveJunction(t *testing.T) {
	bus := New(1000)
	for i := 1; i <= 10; i++ {
		bus.Publish(fmt.Sprintf("h%d", i))
	}

	sub := bus.Subscribe(4)
	// Publish while the replay of 5..10 is still in flight.
	for i := 11; i <= 25; i++ {
		bus.Publish(fmt.Sprintf("l%d", i))
	}

	got := collect(t, sub, 21)
	expectSlice(t, got, bus.History(4, 0))
	sub.Cancel()
	if err := sub.Err(); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

// TestConcurrentJunction hammers the replay/live junction: many subscribers
// join at random history offsets while a publisher keeps appending. Every
// subscriber must observe exactly the history slice from its offset on.
func TestConcurrentJunction(t *testing.T) {
	bus := New(100000)
	const total = 300

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(after uint64) {
			defer wg.Done()
			sub := bus.Subscribe(after)
			got := collect(t, sub, total-int(after))
			expectSlice(t, got, bus.History(after, uint64(total)))
			sub.Cancel()
		}(uint64(g * 7)) // offsets 0,7,14,...,49
	}
	for i := 1; i <= total; i++ {
		bus.Publish(i)
	}
	wg.Wait()
}

// TestSlowSubscriberDisconnect never reads from the channel; once the
// backlog passes the quota the bus must disconnect the subscriber and
// report the last delivered sequence.
func TestSlowSubscriberDisconnect(t *testing.T) {
	const quota = 5
	bus := New(quota)
	sub := bus.Subscribe(0)

	// Let the pump fill the buffered channel to exactly quota events,
	// then overflow the backlog quota with further publishes.
	for i := 1; i <= quota; i++ {
		bus.Publish(i)
	}
	waitBuffered(t, sub, quota)
	for i := quota + 1; i <= 2*quota+1; i++ {
		bus.Publish(i)
	}

	if err := sub.Err(); err != ErrBacklogExceeded {
		t.Fatalf("err = %v, want ErrBacklogExceeded", err)
	}
	if last := sub.Last(); last != quota {
		t.Fatalf("last delivered = %d, want %d", last, quota)
	}

	// The buffered backlog is still drainable and contiguous.
	var drained []Event
	for ev := range sub.C() {
		drained = append(drained, ev)
	}
	expectSlice(t, drained, bus.History(0, quota))

	if n := bus.SubscriberCount(); n != 0 {
		t.Fatalf("subscriber still registered after disconnect: %d", n)
	}
}

// TestDisconnectReconnect resubscribes from the reported Last() sequence
// and verifies no event is lost or duplicated across the reconnect.
func TestDisconnectReconnect(t *testing.T) {
	const quota = 4
	bus := New(quota)

	sub1 := bus.Subscribe(0)
	for i := 1; i <= quota; i++ {
		bus.Publish(i)
	}
	waitBuffered(t, sub1, quota)
	for i := quota + 1; i <= 3*quota; i++ {
		bus.Publish(i)
	}
	if err := sub1.Err(); err != ErrBacklogExceeded {
		t.Fatalf("err = %v, want ErrBacklogExceeded", err)
	}
	resume := sub1.Last()

	// Drain what sub1 buffered; that is the prefix up to resume.
	var first []Event
	for ev := range sub1.C() {
		first = append(first, ev)
	}
	expectSlice(t, first, bus.History(0, resume))

	// Reconnect from the reported sequence and drain the replayed
	// backlog; with no concurrent publishes the pump cannot trip the
	// quota while the consumer keeps up.
	sub2 := bus.Subscribe(resume)
	second := collect(t, sub2, int(bus.Len()-resume))
	// Live tail: publish while collecting.
	for i := 0; i < 3; i++ {
		bus.Publish(fmt.Sprintf("live-%d", i))
	}
	second = append(second, collect(t, sub2, 3)...)
	expectSlice(t, second, bus.History(resume, 0))
	sub2.Cancel()

	// Concatenated, both subscriptions cover the full history exactly once.
	expectSlice(t, append(first, second...), bus.History(0, 0))
}

// TestCancelReleasesBacklog cancels a lagging subscriber and verifies the
// bus drops it, the goroutine exits and later publishes behave normally.
func TestCancelReleasesBacklog(t *testing.T) {
	bus := New(3)
	sub := bus.Subscribe(0)
	for i := 1; i <= 10; i++ {
		bus.Publish(i) // would trip the quota; cancel must win cleanly
	}

	sub.Cancel()
	if err := sub.Err(); err != nil && err != ErrBacklogExceeded {
		t.Fatalf("unexpected err: %v", err)
	}
	if n := bus.SubscriberCount(); n != 0 {
		t.Fatalf("subscriber not released: %d", n)
	}

	// The bus keeps working for a fresh subscriber at the live tail.
	live := bus.Subscribe(bus.Len())
	bus.Publish("after-cancel")
	got := collect(t, live, 1)
	if got[0].Data != "after-cancel" {
		t.Fatalf("got %v, want after-cancel", got[0].Data)
	}
	live.Cancel()
	if n := bus.SubscriberCount(); n != 0 {
		t.Fatalf("subscriber not released: %d", n)
	}
}

// TestCancelDuringReplay cancels while a large replay is in flight; the
// pump must stop promptly instead of draining the whole backlog.
func TestCancelDuringReplay(t *testing.T) {
	bus := New(1 << 20)
	for i := 0; i < 100000; i++ {
		bus.Publish(i)
	}
	sub := bus.Subscribe(0)
	<-sub.C() // confirm delivery started
	done := make(chan struct{})
	go func() { sub.Cancel(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not release the subscription promptly")
	}
	if n := bus.SubscriberCount(); n != 0 {
		t.Fatalf("subscriber not released: %d", n)
	}
}
