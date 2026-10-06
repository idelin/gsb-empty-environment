package eventbus

import (
	"fmt"
	"sync"
	"testing"
)

// collect reads n events from sub, failing the test if the stream ends
// early.
func collect(t *testing.T, sub *Subscription, n int) []Event {
	t.Helper()
	out := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		ev, ok := <-sub.C()
		if !ok {
			t.Fatalf("stream closed after %d events, wanted %d", len(out), n)
		}
		out = append(out, ev)
	}
	return out
}

// drain reads until the channel is closed and returns all events.
func drain(sub *Subscription) []Event {
	var out []Event
	for ev := range sub.C() {
		out = append(out, ev)
	}
	return out
}

// checkAgainstHistory asserts got equals history[start:] exactly:
// same length, same sequence numbers, same order, no gaps, no duplicates.
func checkAgainstHistory(t *testing.T, got, history []Event, start int) {
	t.Helper()
	want := history[start:]
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d (history[%d:])", len(got), len(want), start)
	}
	for i := range want {
		if got[i].Seq != want[i].Seq {
			t.Fatalf("position %d: got seq %d, want seq %d", i, got[i].Seq, want[i].Seq)
		}
		if got[i].Data != want[i].Data {
			t.Fatalf("position %d: got data %v, want %v", i, got[i].Data, want[i].Data)
		}
	}
}

func publishN(b *Bus, from, to int) {
	for i := from; i <= to; i++ {
		b.Publish(fmt.Sprintf("event-%d", i))
	}
}

// TestReplayThenLiveBoundary subscribes while events are being published
// concurrently and verifies the replay/live junction against the full
// history slice: no lost and no duplicated events.
func TestReplayThenLiveBoundary(t *testing.T) {
	const (
		pre     = 100 // events published before subscribe
		during  = 400 // events published concurrently with subscribe/replay
		quota   = 1024
		fromSeq = 37 // replay starts at seq 38
	)
	for trial := 0; trial < 50; trial++ {
		b := New(quota)
		publishN(b, 1, pre)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			publishN(b, pre+1, pre+during)
		}()

		sub := b.Subscribe(fromSeq)
		got := collect(t, sub, pre+during-fromSeq)
		wg.Wait()

		checkAgainstHistory(t, got, b.Snapshot(), fromSeq)
		if sub.Dropped() {
			t.Fatalf("trial %d: subscription unexpectedly dropped", trial)
		}
		sub.Cancel()
	}
}

// TestConcurrentSubscribersSeesFullHistory runs many subscribers joining
// at different points while publishers keep writing; every subscriber
// must observe an exact suffix of the history.
func TestConcurrentSubscribersSeesFullHistory(t *testing.T) {
	const (
		total   = 500
		quota   = 1024
		readers = 8
	)
	b := New(quota)

	subs := make([]*Subscription, readers)
	froms := make([]int64, readers)
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			froms[r] = int64(r * 50)
			subs[r] = b.Subscribe(froms[r])
		}(r)
	}
	publishN(b, 1, total)
	wg.Wait()

	history := b.Snapshot()
	for r := 0; r < readers; r++ {
		got := collect(t, subs[r], total-int(froms[r]))
		checkAgainstHistory(t, got, history, int(froms[r]))
		subs[r].Cancel()
	}
}

// TestSlowSubscriberDisconnect verifies that a subscriber exceeding the
// backlog quota is explicitly disconnected, learns the last delivered
// sequence number, and can resubscribe from it to recover the remaining
// history with no gap and no duplication.
func TestSlowSubscriberDisconnect(t *testing.T) {
	const quota = 8
	b := New(quota)

	slow := b.Subscribe(0) // never read from it
	publishN(b, 1, quota)
	if slow.Dropped() {
		t.Fatal("dropped before exceeding quota")
	}
	b.Publish("one-too-many") // exceeds the quota
	if !slow.Dropped() {
		t.Fatal("slow subscriber not disconnected after exceeding quota")
	}
	if got := slow.LastDelivered(); got != quota {
		t.Fatalf("LastDelivered = %d, want %d", got, quota)
	}
	if n := b.Subscribers(); n != 0 {
		t.Fatalf("bus still tracks %d subscribers after drop", n)
	}

	// Buffered events stay readable after the disconnect.
	buffered := drain(slow)
	checkAgainstHistory(t, buffered, b.Snapshot()[:quota], 0)

	// More events accumulate while the subscriber is gone.
	publishN(b, quota+2, 2*quota-2)

	// Reconnect from the last delivered sequence number.
	rejoined := b.Subscribe(slow.LastDelivered())
	// Live events after the reconnect must follow the replayed ones.
	publishN(b, 2*quota-1, 2*quota)
	rest := collect(t, rejoined, b.Len()-quota)
	rejoined.Cancel()

	// Concatenated streams must equal the full history exactly.
	full := append(buffered, rest...)
	checkAgainstHistory(t, full, b.Snapshot(), 0)
}

// TestReplayExceedsQuota covers a replay that alone overflows the quota:
// the subscription is dropped immediately with LastDelivered pointing at
// the last event that still fit.
func TestReplayExceedsQuota(t *testing.T) {
	const quota = 4
	b := New(quota)
	publishN(b, 1, 10)

	sub := b.Subscribe(0)
	if !sub.Dropped() {
		t.Fatal("subscription not dropped when replay exceeded quota")
	}
	if got := sub.LastDelivered(); got != quota {
		t.Fatalf("LastDelivered = %d, want %d", got, quota)
	}
	got := drain(sub)
	if len(got) != quota || got[quota-1].Seq != quota {
		t.Fatalf("buffered events = %v, want seqs 1..%d", got, quota)
	}
}

// TestCancelReleasesBacklog verifies that cancelling a subscription
// detaches it and frees its backlog, and that Cancel is idempotent.
func TestCancelReleasesBacklog(t *testing.T) {
	const quota = 16
	b := New(quota)

	sub := b.Subscribe(0)
	publishN(b, 1, 6)
	if got := b.Backlog(); got != 6 {
		t.Fatalf("Backlog = %d, want 6", got)
	}

	sub.Cancel()
	if got := b.Backlog(); got != 0 {
		t.Fatalf("Backlog after Cancel = %d, want 0", got)
	}
	if n := b.Subscribers(); n != 0 {
		t.Fatalf("Subscribers after Cancel = %d, want 0", n)
	}
	if sub.Dropped() {
		t.Fatal("cancelled subscription reported as dropped")
	}

	// New publishes must not reach the cancelled subscription.
	publishN(b, 7, 9)
	if got := b.Backlog(); got != 0 {
		t.Fatalf("Backlog after publish to cancelled sub = %d, want 0", got)
	}

	// Cancel is idempotent and does not double-release the backlog.
	sub.Cancel()
	if got := b.Backlog(); got != 0 {
		t.Fatalf("Backlog after second Cancel = %d, want 0", got)
	}
}

// TestCancelConcurrentWithPublish hammers Cancel and Publish together to
// surface data races and double-close bugs.
func TestCancelConcurrentWithPublish(t *testing.T) {
	b := New(4)
	for trial := 0; trial < 100; trial++ {
		sub := b.Subscribe(0)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			b.Publish(trial)
		}()
		go func() {
			defer wg.Done()
			sub.Cancel()
		}()
		wg.Wait()
		drain(sub)
	}
	if got := b.Backlog(); got != 0 {
		t.Fatalf("Backlog = %d, want 0", got)
	}
	if n := b.Subscribers(); n != 0 {
		t.Fatalf("Subscribers = %d, want 0", n)
	}
}

// TestDropReleasesBacklog verifies that a quota drop also frees the
// subscriber's backlog from the bus's accounting.
func TestDropReleasesBacklog(t *testing.T) {
	b := New(4)
	sub := b.Subscribe(0)
	publishN(b, 1, 5) // 5th publish exceeds the quota
	if !sub.Dropped() {
		t.Fatal("subscriber not dropped")
	}
	if got := b.Backlog(); got != 0 {
		t.Fatalf("Backlog after drop = %d, want 0", got)
	}
	drain(sub)
}
