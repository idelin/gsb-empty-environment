package main

import (
	"sync"
	"testing"
	"time"
)

func enqueueAll(q *Queue, events []Event) {
	for _, ev := range events {
		q.Enqueue(ev)
	}
}

func makeEvents(tenant string, priority Priority, from, to uint64) []Event {
	events := make([]Event, 0, to-from)
	for i := from; i < to; i++ {
		events = append(events, Event{ID: i, Tenant: tenant, Priority: priority})
	}
	return events
}

// TestConservationUnderContention floods a small queue with a slow handler
// and verifies received == processed + dropped after a drain shutdown.
func TestConservationUnderContention(t *testing.T) {
	q := NewQueue(8, DefaultHighBurst, PolicyRejectNew)
	pool := NewPool(q, func(Event) { time.Sleep(time.Millisecond) })
	pool.Start(2)

	var events []Event
	for _, tenant := range []string{"a", "b", "c"} {
		events = append(events, makeEvents(tenant, PriorityNormal, 0, 100)...)
		events = append(events, makeEvents(tenant, PriorityHigh, 100, 200)...)
	}
	enqueueAll(q, events)
	q.Close(Drain)
	pool.Wait()

	snap := q.Snapshot()
	if !snap.Conserved() {
		t.Fatalf("conservation violated: %+v", snap)
	}
	if snap.Dropped == 0 {
		t.Fatal("expected drops under contention")
	}
	if snap.DropsByReason[ReasonQueueFull] != snap.Dropped {
		t.Fatalf("unexpected drop reasons: %+v", snap.DropsByReason)
	}
}

// TestHighPriorityDoesNotStarveNormal enqueues a long high-priority stream
// ahead of normal events and verifies normal events interleave with bounded
// gaps instead of all waiting until the high stream is exhausted.
func TestHighPriorityDoesNotStarveNormal(t *testing.T) {
	q := NewQueue(256, 3, PolicyRejectNew)
	enqueueAll(q, makeEvents("t", PriorityHigh, 0, 100))
	enqueueAll(q, makeEvents("t", PriorityNormal, 1000, 1025))

	var mu sync.Mutex
	var order []Event
	pool := NewPool(q, func(ev Event) {
		mu.Lock()
		order = append(order, ev)
		mu.Unlock()
	})
	pool.Start(1)
	q.Close(Drain)
	pool.Wait()

	snap := q.Snapshot()
	if snap.Processed != 125 || snap.Dropped != 0 {
		t.Fatalf("expected 125 processed, 0 dropped; got %+v", snap)
	}
	var normalPositions []int
	for i, ev := range order {
		if ev.Priority == PriorityNormal {
			normalPositions = append(normalPositions, i)
		}
	}
	if len(normalPositions) != 25 {
		t.Fatalf("expected 25 normal events processed, got %d", len(normalPositions))
	}
	prev := -1
	for _, pos := range normalPositions {
		if gap := pos - prev; gap > 4 { // highBurst + 1
			t.Fatalf("normal stream starved: gap %d before position %d", gap, pos)
		}
		prev = pos
	}
}

// TestRejectedEventsAreNotProcessed fills the queue with no workers running
// and verifies rejected events count as dropped, never processed.
func TestRejectedEventsAreNotProcessed(t *testing.T) {
	q := NewQueue(2, DefaultHighBurst, PolicyRejectNew)
	enqueueAll(q, makeEvents("t", PriorityNormal, 0, 10))

	snap := q.Snapshot()
	if snap.Received != 10 || snap.Processed != 0 || snap.Dropped != 8 {
		t.Fatalf("got %+v", snap)
	}

	pool := NewPool(q, func(Event) {})
	pool.Start(1)
	q.Close(Drain)
	pool.Wait()

	snap = q.Snapshot()
	if snap.Processed != 2 || snap.Dropped != 8 || !snap.Conserved() {
		t.Fatalf("got %+v", snap)
	}
}

// TestDrainShutdownProcessesBacklog verifies drain mode empties the queue.
func TestDrainShutdownProcessesBacklog(t *testing.T) {
	q := NewQueue(64, DefaultHighBurst, PolicyRejectNew)
	enqueueAll(q, makeEvents("t", PriorityNormal, 0, 50))
	q.Close(Drain)

	pool := NewPool(q, func(Event) {})
	pool.Start(3)
	pool.Wait()

	snap := q.Snapshot()
	if snap.Processed != 50 || snap.Dropped != 0 || !snap.Conserved() {
		t.Fatalf("got %+v", snap)
	}
}

// TestStopNowDropsBacklog verifies immediate shutdown discards queued events
// as dropped (reason "shutdown") while an in-flight handler still completes.
func TestStopNowDropsBacklog(t *testing.T) {
	q := NewQueue(64, DefaultHighBurst, PolicyRejectNew)
	enqueueAll(q, makeEvents("t", PriorityNormal, 0, 100))

	started := make(chan struct{})
	release := make(chan struct{})
	pool := NewPool(q, func(Event) {
		close(started)
		<-release
	})
	pool.Start(1)
	<-started // one event is in flight, 63 queued, 36 already rejected
	q.Close(StopNow)
	close(release)
	pool.Wait()

	snap := q.Snapshot()
	if !snap.Conserved() {
		t.Fatalf("conservation violated: %+v", snap)
	}
	if snap.Processed != 1 {
		t.Fatalf("in-flight event should complete, processed=%d", snap.Processed)
	}
	if snap.DropsByReason[ReasonShutdown] != 63 {
		t.Fatalf("expected 63 shutdown drops, got %+v", snap.DropsByReason)
	}
	if snap.DropsByReason[ReasonQueueFull] != 36 {
		t.Fatalf("expected 36 queue_full drops, got %+v", snap.DropsByReason)
	}
}

// TestEnqueueAfterCloseIsDropped verifies arrivals after shutdown are
// received-then-dropped, never processed.
func TestEnqueueAfterCloseIsDropped(t *testing.T) {
	q := NewQueue(4, DefaultHighBurst, PolicyRejectNew)
	q.Close(StopNow)
	q.Enqueue(Event{ID: 1, Tenant: "t", Priority: PriorityHigh})

	snap := q.Snapshot()
	if snap.Received != 1 || snap.Processed != 0 || snap.Dropped != 1 {
		t.Fatalf("got %+v", snap)
	}
	if snap.DropsByReason[ReasonShutdown] != 1 {
		t.Fatalf("got %+v", snap.DropsByReason)
	}
}

// TestTenantFairness verifies a flooding tenant cannot delay another
// tenant's events behind its entire backlog.
func TestTenantFairness(t *testing.T) {
	q := NewQueue(512, DefaultHighBurst, PolicyRejectNew)
	var mu sync.Mutex
	var order []Event
	pool := NewPool(q, func(ev Event) {
		mu.Lock()
		order = append(order, ev)
		mu.Unlock()
	})
	pool.Start(1)

	enqueueAll(q, makeEvents("flood", PriorityNormal, 0, 200))
	enqueueAll(q, makeEvents("small", PriorityNormal, 1000, 1010))
	q.Close(Drain)
	pool.Wait()

	lastFlood, lastSmall := -1, -1
	for i, ev := range order {
		if ev.Tenant == "flood" {
			lastFlood = i
		} else {
			lastSmall = i
		}
	}
	if lastSmall >= lastFlood {
		t.Fatalf("small tenant starved: last small at %d, last flood at %d", lastSmall, lastFlood)
	}
	snap := q.Snapshot()
	if snap.Tenants["small"].Processed != 10 || snap.Tenants["flood"].Processed != 200 {
		t.Fatalf("got %+v", snap.Tenants)
	}
}

// TestDropOldestPolicy verifies the oldest queued event is evicted when the
// queue is full under the drop-oldest policy.
func TestDropOldestPolicy(t *testing.T) {
	q := NewQueue(2, DefaultHighBurst, PolicyDropOldest)
	q.Enqueue(Event{ID: 1, Tenant: "a", Priority: PriorityNormal})
	q.Enqueue(Event{ID: 2, Tenant: "b", Priority: PriorityNormal})
	q.Enqueue(Event{ID: 3, Tenant: "c", Priority: PriorityHigh})   // evicts 1
	q.Enqueue(Event{ID: 4, Tenant: "c", Priority: PriorityNormal}) // evicts 2

	snap := q.Snapshot()
	if snap.Dropped != 2 || snap.DropsByReason[ReasonQueueFull] != 2 {
		t.Fatalf("got %+v", snap)
	}

	var mu sync.Mutex
	var processed []uint64
	pool := NewPool(q, func(ev Event) {
		mu.Lock()
		processed = append(processed, ev.ID)
		mu.Unlock()
	})
	pool.Start(1)
	q.Close(Drain)
	pool.Wait()

	if len(processed) != 2 {
		t.Fatalf("processed %v", processed)
	}
	seen := map[uint64]bool{processed[0]: true, processed[1]: true}
	if !seen[3] || !seen[4] {
		t.Fatalf("expected events 3 and 4 to survive, processed %v", processed)
	}
	if s := q.Snapshot(); !s.Conserved() {
		t.Fatalf("conservation violated: %+v", s)
	}
}
