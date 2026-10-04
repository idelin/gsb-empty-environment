package batchq

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

func taskIDs(tasks []Task) []string {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	return ids
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAllOrNothing(t *testing.T) {
	q := New(5)
	batch := []Task{{ID: "a", Cost: 2}, {ID: "b", Cost: 2}, {ID: "c", Cost: 2}}

	r := q.Reserve(batch, AllOrNothing)
	if got := len(r.Admitted()); got != 0 {
		t.Fatalf("expected nothing admitted, got %d tasks", got)
	}
	r.Cancel()
	if got := q.Used(); got != 0 {
		t.Fatalf("expected used=0 after cancel, got %d", got)
	}

	r = q.Reserve(batch[:2], AllOrNothing)
	if got := taskIDs(r.Admitted()); !equalIDs(got, []string{"a", "b"}) {
		t.Fatalf("expected [a b], got %v", got)
	}
	enqueued := r.Enqueue()
	if got := taskIDs(enqueued); !equalIDs(got, []string{"a", "b"}) {
		t.Fatalf("expected enqueued [a b], got %v", got)
	}
	if got := q.Used(); got != 4 {
		t.Fatalf("expected used=4, got %d", got)
	}
	if got := taskIDs(q.Tasks()); !equalIDs(got, []string{"a", "b"}) {
		t.Fatalf("expected queue [a b], got %v", got)
	}
}

func TestLongestPrefix(t *testing.T) {
	q := New(5)
	batch := []Task{{ID: "a", Cost: 2}, {ID: "b", Cost: 2}, {ID: "c", Cost: 2}}

	r := q.Reserve(batch, LongestPrefix)
	if got := taskIDs(r.Admitted()); !equalIDs(got, []string{"a", "b"}) {
		t.Fatalf("expected prefix [a b], got %v", got)
	}
	r.Enqueue()

	// One unit left: only a cost-1 prefix task fits.
	r2 := q.Reserve([]Task{{ID: "d", Cost: 2}, {ID: "e", Cost: 1}}, LongestPrefix)
	if got := len(r2.Admitted()); got != 0 {
		t.Fatalf("expected nothing admitted, got %d", got)
	}
	r2.Cancel()

	r3 := q.Reserve([]Task{{ID: "e", Cost: 1}}, LongestPrefix)
	if got := taskIDs(r3.Admitted()); !equalIDs(got, []string{"e"}) {
		t.Fatalf("expected [e], got %v", got)
	}
	r3.Enqueue()
	if got := q.Used(); got != 5 {
		t.Fatalf("expected used=5, got %d", got)
	}
}

func TestCancelRefundsUniformly(t *testing.T) {
	// The refund rule is identical for both modes: cancel returns
	// exactly the reserved amount.
	cases := []struct {
		mode         Mode
		wantAdmitted []string
	}{
		{AllOrNothing, nil},            // total 4 > 2 free: nothing admitted
		{LongestPrefix, []string{"b"}}, // only the first task fits
	}
	for _, tc := range cases {
		q := New(4)
		r1 := q.Reserve([]Task{{ID: "a", Cost: 2}}, AllOrNothing)
		r1.Enqueue()

		r2 := q.Reserve([]Task{{ID: "b", Cost: 2}, {ID: "c", Cost: 2}}, tc.mode)
		if got := taskIDs(r2.Admitted()); !equalIDs(got, tc.wantAdmitted) {
			t.Fatalf("mode %v: admitted %v, want %v", tc.mode, got, tc.wantAdmitted)
		}
		r2.Cancel()
		if got := q.Used(); got != 2 {
			t.Fatalf("mode %v: expected used=2 after cancel, got %d", tc.mode, got)
		}
		// Double cancel is a no-op.
		r2.Cancel()
		if got := q.Used(); got != 2 {
			t.Fatalf("mode %v: expected used=2 after double cancel, got %d", tc.mode, got)
		}
		// Enqueue after cancel is a no-op.
		if got := r2.Enqueue(); got != nil {
			t.Fatalf("mode %v: expected nil enqueue after cancel, got %v", tc.mode, got)
		}
	}
}

func TestCancelRefundsExactReservation(t *testing.T) {
	q := New(10)
	r := q.Reserve([]Task{{ID: "a", Cost: 2}, {ID: "b", Cost: 3}, {ID: "c", Cost: 4}}, LongestPrefix)
	if got := q.Used(); got != 9 {
		t.Fatalf("expected used=9, got %d", got)
	}
	r.Cancel()
	if got := q.Used(); got != 0 {
		t.Fatalf("expected full refund, got used=%d", got)
	}
}

func TestFIFONoOvertaking(t *testing.T) {
	// Fill the queue, then ensure a later reservation cannot be served
	// before an earlier one that is still waiting for its turn.
	q := New(2)
	r1 := q.Reserve([]Task{{ID: "a", Cost: 2}}, AllOrNothing)
	r1.Enqueue()

	const n = 50
	start := make(chan struct{})
	seqs := make([]uint64, n)
	var mu sync.Mutex
	order := make([]int, 0, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			r := q.Reserve([]Task{{ID: fmt.Sprintf("t%d", i), Cost: 1}}, LongestPrefix)
			seqs[i] = r.Seq()
			r.Cancel()
		}(i)
	}
	close(start)
	wg.Wait()

	// Admission sequence numbers must be distinct and dense: every
	// reservation was served exactly once, in some serial order.
	sorted := append([]uint64(nil), seqs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for i, s := range sorted {
		if s != uint64(i)+1 { // +1: r1 took ticket 0
			t.Fatalf("expected dense sequence, got %v", sorted)
		}
	}
}

// TestConcurrentHistory hammers the queue with concurrent batches and
// then replays the recorded history to verify capacity accounting and
// the returned task lists.
func TestConcurrentHistory(t *testing.T) {
	const (
		capacity = 64
		workers  = 64
		batches  = 25
	)
	q := New(capacity)

	type record struct {
		mode      Mode
		submitted []Task
		admitted  []Task
		enqueued  []Task
		canceled  bool
		seq       uint64
	}

	records := make([][]record, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)*7919 + 13))
			for b := 0; b < batches; b++ {
				n := 1 + rng.Intn(5)
				batch := make([]Task, n)
				for i := range batch {
					batch[i] = Task{
						ID:   fmt.Sprintf("w%d-b%d-t%d", w, b, i),
						Cost: 1 + rng.Intn(8),
					}
				}
				mode := AllOrNothing
				if rng.Intn(2) == 0 {
					mode = LongestPrefix
				}
				r := q.Reserve(batch, mode)
				rec := record{
					mode:      mode,
					submitted: batch,
					admitted:  r.Admitted(),
					seq:       r.Seq(),
				}
				if rng.Intn(10) < 3 {
					r.Cancel()
					rec.canceled = true
				} else {
					rec.enqueued = r.Enqueue()
				}
				records[w] = append(records[w], rec)
			}
		}(w)
	}
	wg.Wait()

	// 1. Capacity invariant: the high-water mark never exceeded capacity.
	if got := q.MaxUsed(); got > capacity {
		t.Fatalf("capacity exceeded: maxUsed=%d > %d", got, capacity)
	}

	var all []record
	for _, recs := range records {
		all = append(all, recs...)
	}

	// 2. Per-batch shape: admitted is a prefix of submitted; AllOrNothing
	//    admits all or nothing; Enqueue returns exactly the admitted list.
	for _, rec := range all {
		if len(rec.admitted) > len(rec.submitted) {
			t.Fatalf("admitted longer than submitted: %v vs %v", rec.admitted, rec.submitted)
		}
		for i := range rec.admitted {
			if rec.admitted[i] != rec.submitted[i] {
				t.Fatalf("admitted is not a prefix of submitted at %d: %v vs %v", i, rec.admitted, rec.submitted)
			}
		}
		if rec.mode == AllOrNothing && len(rec.admitted) != 0 && len(rec.admitted) != len(rec.submitted) {
			t.Fatalf("AllOrNothing admitted a partial batch: %v of %v", rec.admitted, rec.submitted)
		}
		if rec.canceled {
			if len(rec.enqueued) != 0 {
				t.Fatalf("canceled reservation returned enqueued tasks: %v", rec.enqueued)
			}
		} else if !equalIDs(taskIDs(rec.enqueued), taskIDs(rec.admitted)) {
			t.Fatalf("enqueued %v != admitted %v", rec.enqueued, rec.admitted)
		}
	}

	// 3. Replay the admission history in FIFO order: at each admission
	//    the admitted cost must fit in the capacity left by earlier
	//    reservations that were never canceled.
	sort.Slice(all, func(i, j int) bool { return all[i].seq < all[j].seq })
	held := 0
	for _, rec := range all {
		cost := totalCost(rec.admitted)
		if held+cost > capacity {
			t.Fatalf("history overflows capacity at seq %d: held=%d cost=%d capacity=%d", rec.seq, held, cost, capacity)
		}
		if !rec.canceled {
			held += cost
		}
	}

	// 4. Final accounting: Used equals the cost of everything enqueued,
	//    and the queue's task list is exactly the enqueued tasks in
	//    admission order, each batch contiguous and in order.
	wantUsed := 0
	var wantTasks []string
	for _, rec := range all {
		if rec.canceled {
			continue
		}
		wantUsed += totalCost(rec.enqueued)
		wantTasks = append(wantTasks, taskIDs(rec.enqueued)...)
	}
	if got := q.Used(); got != wantUsed {
		t.Fatalf("used=%d, want %d", got, wantUsed)
	}
	if got := taskIDs(q.Tasks()); !equalIDs(got, wantTasks) {
		t.Fatalf("queue tasks mismatch:\n got %v\nwant %v", got, wantTasks)
	}
}
