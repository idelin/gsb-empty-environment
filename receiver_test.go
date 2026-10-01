package recvq

import (
	"sync"
	"testing"
	"time"
)

// runToQuiescence enqueues all records, shuts down with the given mode,
// and waits for workers to finish.
func runToQuiescence(r *Receiver, recs []Record, workers int, mode Mode, work time.Duration) {
	done := make(chan struct{})
	go func() {
		r.Run(workers, func(Record) { time.Sleep(work) })
		close(done)
	}()
	for _, rec := range recs {
		r.Enqueue(rec)
	}
	r.Shutdown(mode)
	<-done
}

func TestConservationUnderLoad(t *testing.T) {
	r := NewReceiver(16, DropIncoming, nil)
	recs := Synthesize(1, 2000, []string{"a", "b", "c", "d"})
	runToQuiescence(r, recs, 4, Drain, 50*time.Microsecond)

	s := r.Stats()
	if !s.Conserved(r.InQueue()) {
		t.Fatalf("conservation violated: %s in_queue=%d", s, r.InQueue())
	}
	if r.InQueue() != 0 {
		t.Fatalf("drain left %d records queued", r.InQueue())
	}
	if s.DropsByReason[ReasonQueueFull] == 0 {
		t.Fatalf("expected queue_full drops under load, got %s", s)
	}
	if s.Arrived != len(recs) {
		t.Fatalf("arrived=%d want %d", s.Arrived, len(recs))
	}
}

func TestRejectedRecordsAreDroppedNotProcessed(t *testing.T) {
	r := NewReceiver(4, DropIncoming, nil)
	recs := Synthesize(2, 500, []string{"a", "b"})
	runToQuiescence(r, recs, 1, Drain, 2*time.Millisecond)

	s := r.Stats()
	rejected := s.DropsByReason[ReasonQueueFull]
	if rejected == 0 {
		t.Fatal("expected rejections on a full queue")
	}
	if s.Processed != s.Enqueued {
		t.Fatalf("processed=%d must equal enqueued=%d after drain (rejected records must not count as processed)",
			s.Processed, s.Enqueued)
	}
	if s.Arrived != s.Processed+s.Dropped {
		t.Fatalf("arrived=%d processed=%d dropped=%d: not conserved", s.Arrived, s.Processed, s.Dropped)
	}
}

func TestHighPriorityStreamCannotStarveNormal(t *testing.T) {
	// Fill the queue before workers start so both lanes stay saturated.
	r := NewReceiver(1000, DropIncoming, nil)
	var recs []Record
	for i := 0; i < 400; i++ {
		recs = append(recs, Record{ID: "h" + string(rune(i)), Tenant: "noisy", Priority: High})
	}
	for i := 0; i < 400; i++ {
		recs = append(recs, Record{ID: "n" + string(rune(i)), Tenant: "quiet", Priority: Normal})
	}
	for _, rec := range recs {
		if !r.Enqueue(rec) {
			t.Fatalf("record %s unexpectedly rejected", rec.ID)
		}
	}

	var mu sync.Mutex
	var order []Record
	done := make(chan struct{})
	go func() {
		r.Run(1, func(rec Record) {
			mu.Lock()
			order = append(order, rec)
			mu.Unlock()
		})
		close(done)
	}()
	r.Shutdown(Drain)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 800 {
		t.Fatalf("processed %d records, want 800", len(order))
	}
	normalFirst400 := 0
	for _, rec := range order[:400] {
		if rec.Priority == Normal {
			normalFirst400++
		}
	}
	// Weighted 3:1 scheduling guarantees ~25% for the normal lane even
	// against a saturated high-priority lane.
	if normalFirst400 < 80 {
		t.Fatalf("normal stream starved: only %d/400 processed in the first half", normalFirst400)
	}
}

func TestTenantFairnessRoundRobin(t *testing.T) {
	r := NewReceiver(200, DropIncoming, nil)
	tenants := []string{"t1", "t2", "t3", "t4"}
	for i := 0; i < 200; i++ {
		r.Enqueue(Record{ID: string(rune(i)), Tenant: tenants[i%len(tenants)], Priority: Normal})
	}

	var mu sync.Mutex
	var order []Record
	done := make(chan struct{})
	go func() {
		r.Run(1, func(rec Record) {
			mu.Lock()
			order = append(order, rec)
			mu.Unlock()
		})
		close(done)
	}()
	r.Shutdown(Drain)
	<-done

	mu.Lock()
	defer mu.Unlock()
	// In the first 40 picks every tenant must appear at least 5 times:
	// no tenant can monopolize the lane.
	counts := map[string]int{}
	for _, rec := range order[:40] {
		counts[rec.Tenant]++
	}
	for _, tenant := range tenants {
		if counts[tenant] < 5 {
			t.Fatalf("tenant %s under-served: %v", tenant, counts)
		}
	}
}

func TestDropOldestEvictsQueuedRecords(t *testing.T) {
	r := NewReceiver(4, DropOldest, nil)
	for i := 0; i < 6; i++ {
		r.Enqueue(Record{ID: string(rune('a' + i)), Tenant: "t", Priority: Normal})
	}
	s := r.Stats()
	if s.Enqueued != 6 || s.Dropped != 2 || s.DropsByReason[ReasonEvicted] != 2 {
		t.Fatalf("want enqueued=6 dropped=2(evicted), got %s", s)
	}
	if r.InQueue() != 4 {
		t.Fatalf("queue size=%d, want bounded at 4", r.InQueue())
	}
	runToQuiescence(r, nil, 1, Drain, 0)
	s = r.Stats()
	if s.Processed != 4 || !s.Conserved(0) {
		t.Fatalf("after drain: %s", s)
	}
}

func TestDrainProcessesEverything(t *testing.T) {
	r := NewReceiver(512, DropIncoming, nil)
	recs := Synthesize(3, 300, []string{"a", "b"})
	runToQuiescence(r, recs, 2, Drain, 0)
	s := r.Stats()
	if s.Dropped != 0 || s.Processed != len(recs) {
		t.Fatalf("drain must process everything: %s", s)
	}
}

func TestStopNowDiscardsQueuedRecords(t *testing.T) {
	r := NewReceiver(256, DropIncoming, nil)
	recs := Synthesize(4, 256, []string{"a", "b"})
	// Slow handler + fast enqueue: most records are still queued when
	// StopNow hits and must be discarded, not processed.
	runToQuiescence(r, recs, 1, StopNow, 20*time.Millisecond)

	s := r.Stats()
	if s.DropsByReason[ReasonShutdown] == 0 {
		t.Fatalf("expected shutdown discards, got %s", s)
	}
	if r.InQueue() != 0 {
		t.Fatalf("stop-now left %d records queued", r.InQueue())
	}
	if !s.Conserved(0) {
		t.Fatalf("conservation violated after stop-now: %s", s)
	}
	if s.Processed >= s.Enqueued {
		t.Fatalf("stop-now should discard most of the queue: %s", s)
	}
}

func TestEnqueueAfterCloseIsRejected(t *testing.T) {
	r := NewReceiver(8, DropIncoming, nil)
	r.Shutdown(Drain)
	if r.Enqueue(Record{ID: "x", Tenant: "t"}) {
		t.Fatal("enqueue after close must be rejected")
	}
	s := r.Stats()
	if s.DropsByReason[ReasonClosed] != 1 || s.Processed != 0 {
		t.Fatalf("closed-queue rejection must count as dropped: %s", s)
	}
	if !s.Conserved(0) {
		t.Fatalf("conservation violated: %s", s)
	}
}
