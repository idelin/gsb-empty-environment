package batchq

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func ids(tasks []Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

func resIDs(res []*Reservation) []string {
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.Task().ID
	}
	return out
}

func equalStrings(a, b []string) bool {
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

func TestAdmitAllOrNothing(t *testing.T) {
	q := New(10)
	batch := []Task{{"a", 4}, {"b", 4}, {"c", 4}}
	admitted, rejected, err := q.Admit(batch, AdmitAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(admitted) != 0 || len(rejected) != 3 {
		t.Fatalf("batch of 12 into capacity 10: admitted=%d rejected=%d, want 0/3", len(admitted), len(rejected))
	}
	if got := q.Used(); got != 0 {
		t.Fatalf("used=%d, want 0", got)
	}

	admitted, rejected, err = q.Admit([]Task{{"a", 4}, {"b", 4}}, AdmitAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(admitted) != 2 || len(rejected) != 0 {
		t.Fatalf("admitted=%d rejected=%d, want 2/0", len(admitted), len(rejected))
	}
	if got := q.Used(); got != 8 {
		t.Fatalf("used=%d, want 8", got)
	}
	if got := ids(q.Snapshot()); !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("snapshot=%v, want [a b]", got)
	}
}

func TestAdmitLongestPrefix(t *testing.T) {
	q := New(10)
	batch := []Task{{"a", 3}, {"b", 4}, {"c", 5}, {"d", 1}}
	admitted, rejected, err := q.Admit(batch, AdmitPrefix)
	if err != nil {
		t.Fatal(err)
	}
	// 3+4 fits, 3+4+5 does not: longest prefix is [a b].
	if got := resIDs(admitted); !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("admitted=%v, want [a b]", got)
	}
	if got := ids(rejected); !equalStrings(got, []string{"c", "d"}) {
		t.Fatalf("rejected=%v, want [c d]", got)
	}
	if got := q.Used(); got != 7 {
		t.Fatalf("used=%d, want 7", got)
	}
}

func TestCancelRefundsCapacity(t *testing.T) {
	q := New(5)
	admitted, _, err := q.Admit([]Task{{"a", 5}}, AdmitAll)
	if err != nil {
		t.Fatal(err)
	}
	if _, rejected, _ := q.Admit([]Task{{"b", 5}}, AdmitAll); len(rejected) != 1 {
		t.Fatal("b should be rejected while a holds the capacity")
	}
	admitted[0].Cancel()
	if got := q.Used(); got != 0 {
		t.Fatalf("used=%d after cancel, want 0", got)
	}
	admitted2, rejected, _ := q.Admit([]Task{{"b", 5}}, AdmitAll)
	if len(admitted2) != 1 || len(rejected) != 0 {
		t.Fatal("b should be admitted after a was cancelled")
	}
	if got := ids(q.Snapshot()); !equalStrings(got, []string{"b"}) {
		t.Fatalf("snapshot=%v, want [b]", got)
	}
}

func TestCancelIsIdempotent(t *testing.T) {
	q := New(5)
	admitted, _, _ := q.Admit([]Task{{"a", 5}}, AdmitAll)
	admitted[0].Cancel()
	admitted[0].Cancel()
	admitted[0].Cancel()
	if got := q.Used(); got != 0 {
		t.Fatalf("used=%d after repeated cancels, want 0", got)
	}
}

func TestInvalidTask(t *testing.T) {
	q := New(5)
	if _, _, err := q.Admit([]Task{{"", 1}}, AdmitAll); err != ErrInvalidTask {
		t.Fatalf("err=%v, want ErrInvalidTask", err)
	}
	if _, _, err := q.Admit([]Task{{"a", 0}}, AdmitPrefix); err != ErrInvalidTask {
		t.Fatalf("err=%v, want ErrInvalidTask", err)
	}
}

// TestConcurrentHistories hammers the queue with concurrent batches and
// cancellations, then replays the recorded history to verify that
// capacity was never exceeded, every admitted list is a valid prefix
// decision, and no later batch was served before an earlier one.
func TestConcurrentHistories(t *testing.T) {
	const (
		capacity   = 64
		goroutines = 8
		batches    = 40
		maxBatch   = 6
		maxSize    = 20
	)

	q := New(capacity)
	sizes := make(map[string]int64)
	var sizesMu sync.Mutex

	type result struct {
		admitted []string
	}
	results := make([][]result, goroutines)
	live := make([][]*Reservation, goroutines)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)*7919 + 1))
			var pending []*Reservation
			for b := 0; b < batches; b++ {
				n := 1 + rng.Intn(maxBatch)
				batch := make([]Task, n)
				sizesMu.Lock()
				for i := range batch {
					id := fmt.Sprintf("g%02d-b%02d-t%d", g, b, i)
					size := int64(1 + rng.Intn(maxSize))
					batch[i] = Task{ID: id, Size: size}
					sizes[id] = size
				}
				sizesMu.Unlock()

				mode := AdmitAll
				if rng.Intn(2) == 0 {
					mode = AdmitPrefix
				}
				admitted, _, err := q.Admit(batch, mode)
				if err != nil {
					t.Errorf("Admit: %v", err)
					return
				}
				results[g] = append(results[g], result{admitted: resIDs(admitted)})
				pending = append(pending, admitted...)

				// Cancel a random subset of the reservations this
				// goroutine still holds.
				var keep []*Reservation
				for _, r := range pending {
					if rng.Intn(3) == 0 {
						r.Cancel()
					} else {
						keep = append(keep, r)
					}
				}
				pending = keep
			}
			live[g] = pending
		}(g)
	}
	wg.Wait()

	// 1. Replay the serialized history.
	events := q.Events()
	cancelled := make(map[string]bool)
	admittedOnce := make(map[string]bool)
	var used int64
	var lastSeq uint64
	firstAdmit := true
	for _, ev := range events {
		switch ev.Kind {
		case EventAdmit:
			if !firstAdmit && ev.Seq <= lastSeq {
				t.Fatalf("admit events out of order: seq %d after %d", ev.Seq, lastSeq)
			}
			firstAdmit = false
			lastSeq = ev.Seq

			// Admitted tasks must be a prefix of the requested batch.
			if len(ev.Admitted) > len(ev.Requested) {
				t.Fatalf("seq %d: admitted %v longer than requested %v", ev.Seq, ev.Admitted, ev.Requested)
			}
			for i, id := range ev.Admitted {
				if ev.Requested[i] != id {
					t.Fatalf("seq %d: admitted %v is not a prefix of requested %v", ev.Seq, ev.Admitted, ev.Requested)
				}
			}
			if ev.Mode == AdmitAll && len(ev.Admitted) != 0 && len(ev.Admitted) != len(ev.Requested) {
				t.Fatalf("seq %d: AdmitAll admitted %d of %d", ev.Seq, len(ev.Admitted), len(ev.Requested))
			}
			var sum int64
			for _, id := range ev.Admitted {
				sum += sizes[id]
				admittedOnce[id] = true
			}
			if used+sum > capacity {
				t.Fatalf("seq %d: capacity exceeded: used %d + %d > %d", ev.Seq, used, sum, capacity)
			}
			if ev.Mode == AdmitPrefix && len(ev.Admitted) < len(ev.Requested) {
				next := sizes[ev.Requested[len(ev.Admitted)]]
				if used+sum+next <= capacity {
					t.Fatalf("seq %d: prefix not longest, %s still fits", ev.Seq, ev.Requested[len(ev.Admitted)])
				}
			}
			used += sum
		case EventCancel:
			if !admittedOnce[ev.TaskID] {
				t.Fatalf("cancel of never-admitted task %s", ev.TaskID)
			}
			if cancelled[ev.TaskID] {
				t.Fatalf("task %s cancelled twice", ev.TaskID)
			}
			cancelled[ev.TaskID] = true
			used -= sizes[ev.TaskID]
			if used < 0 {
				t.Fatalf("used went negative after cancel of %s", ev.TaskID)
			}
		}
		if ev.Used != used {
			t.Fatalf("event %+v: recorded used %d, replayed %d", ev, ev.Used, used)
		}
	}

	// 2. Every caller observed exactly what the history records.
	observed := make(map[string][]string) // first requested id -> admitted ids
	for g := 0; g < goroutines; g++ {
		for b, res := range results[g] {
			first := fmt.Sprintf("g%02d-b%02d-t0", g, b)
			observed[first] = res.admitted
		}
	}
	for _, ev := range events {
		if ev.Kind != EventAdmit {
			continue
		}
		got, ok := observed[ev.Requested[0]]
		if !ok {
			t.Fatalf("seq %d: no caller result for batch %v", ev.Seq, ev.Requested)
		}
		if !equalStrings(got, ev.Admitted) {
			t.Fatalf("seq %d: caller saw %v, history says %v", ev.Seq, got, ev.Admitted)
		}
	}

	// 3. Final capacity equals the sum of the still-live reservations.
	var wantUsed int64
	for g := 0; g < goroutines; g++ {
		for _, r := range live[g] {
			wantUsed += r.Task().Size
		}
	}
	if got := q.Used(); got != wantUsed {
		t.Fatalf("used=%d, want %d from live reservations", got, wantUsed)
	}
	if used != wantUsed {
		t.Fatalf("replayed used=%d, want %d", used, wantUsed)
	}

	// 4. The queue contents are exactly the admitted, non-cancelled
	// tasks in admission order.
	var wantQueued []string
	for _, ev := range events {
		if ev.Kind != EventAdmit {
			continue
		}
		for _, id := range ev.Admitted {
			if !cancelled[id] {
				wantQueued = append(wantQueued, id)
			}
		}
	}
	if got := ids(q.Snapshot()); !equalStrings(got, wantQueued) {
		t.Fatalf("snapshot=%v, want %v", got, wantQueued)
	}
}
