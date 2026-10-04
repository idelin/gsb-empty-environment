package executor

import (
	"context"
	"testing"
	"time"
)

func contains(list []TaskID, id TaskID) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

func assertNoOverlap(t *testing.T, r Report) {
	t.Helper()
	done := make(map[TaskID]bool, len(r.Completed))
	for _, id := range r.Completed {
		done[id] = true
	}
	for _, id := range r.Unfinished {
		if done[id] {
			t.Fatalf("task %q listed as both completed and unfinished", id)
		}
	}
}

func TestShutdownDrainsBeforeDeadline(t *testing.T) {
	e := New(2)
	for _, id := range []TaskID{"a", "b", "c"} {
		if err := e.Submit(id, func(ctx context.Context) (interface{}, error) {
			time.Sleep(10 * time.Millisecond)
			return "ok", nil
		}); err != nil {
			t.Fatalf("submit %q: %v", id, err)
		}
	}
	r := e.Shutdown(2 * time.Second)
	if !r.Drained {
		t.Fatal("expected drained shutdown")
	}
	if len(r.Unfinished) != 0 {
		t.Fatalf("expected no unfinished tasks, got %v", r.Unfinished)
	}
	if len(r.Completed) != 3 {
		t.Fatalf("expected 3 completed, got %v", r.Completed)
	}
	assertNoOverlap(t, r)
	e.Wait()
}

func TestShutdownDeadlineCancelsAndReportsUnfinished(t *testing.T) {
	e := New(2)
	release := make(chan struct{})

	mustSubmit := func(id TaskID, fn TaskFunc) {
		if err := e.Submit(id, fn); err != nil {
			t.Fatalf("submit %q: %v", id, err)
		}
	}
	mustSubmit("fast", func(ctx context.Context) (interface{}, error) {
		return "fast-done", nil
	})
	// Ignores cancellation: finishes only when release closes.
	mustSubmit("stubborn", func(ctx context.Context) (interface{}, error) {
		<-release
		return "stubborn-done", nil
	})
	// Honors cancellation.
	mustSubmit("cooperative", func(ctx context.Context) (interface{}, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	// Never started: workers are busy and the deadline hits first.
	mustSubmit("queued", func(ctx context.Context) (interface{}, error) {
		return "queued-done", nil
	})

	r := e.Shutdown(100 * time.Millisecond)
	if r.Drained {
		t.Fatal("expected deadline-expired shutdown")
	}
	if !contains(r.Completed, "fast") {
		t.Fatalf("fast should be completed: %+v", r)
	}
	for _, id := range []TaskID{"stubborn", "cooperative", "queued"} {
		if !contains(r.Unfinished, id) {
			t.Fatalf("%q should be unfinished: %+v", id, r)
		}
	}
	assertNoOverlap(t, r)

	// The report is repeatable and stable while nothing changes.
	r2 := e.Report()
	if len(r2.Unfinished) != len(r.Unfinished) {
		t.Fatalf("repeat query mismatch: %+v vs %+v", r, r2)
	}

	// Let the stubborn task finish late, after Shutdown returned.
	close(release)
	e.Wait()

	r3 := e.Report()
	if contains(r3.Unfinished, "stubborn") {
		t.Fatalf("late-completed task still listed unfinished: %+v", r3)
	}
	if !contains(r3.Completed, "stubborn") {
		t.Fatalf("late-completed task missing from completed: %+v", r3)
	}
	// The queued task never ran and stays unfinished; it must not be
	// duplicated into completed.
	if !contains(r3.Unfinished, "queued") || contains(r3.Completed, "queued") {
		t.Fatalf("queued task misreported: %+v", r3)
	}
	if r3.LateCompletions < 2 {
		t.Fatalf("expected >=2 late completions (stubborn, cooperative), got %d", r3.LateCompletions)
	}
	assertNoOverlap(t, r3)

	// Late results remain safely retrievable.
	res, ok := e.Result("stubborn")
	if !ok || res.Value != "stubborn-done" || !res.Late {
		t.Fatalf("late result not recorded safely: %+v ok=%v", res, ok)
	}
	res, ok = e.Result("cooperative")
	if !ok || res.Err == nil {
		t.Fatalf("cooperative task should record ctx error: %+v ok=%v", res, ok)
	}
	if _, ok = e.Result("queued"); ok {
		t.Fatal("queued task must have no result")
	}
}

func TestSubmitRejectedDuringShutdown(t *testing.T) {
	e := New(1)
	e.Shutdown(10 * time.Millisecond)
	if err := e.Submit("late", func(ctx context.Context) (interface{}, error) { return nil, nil }); err != ErrShuttingDown {
		t.Fatalf("expected ErrShuttingDown, got %v", err)
	}
	if err := e.Submit("late", func(ctx context.Context) (interface{}, error) { return nil, nil }); err != ErrShuttingDown {
		t.Fatalf("expected ErrShuttingDown on repeat, got %v", err)
	}
	e.Wait()
}

func TestDuplicateSubmitRejected(t *testing.T) {
	e := New(1)
	fn := func(ctx context.Context) (interface{}, error) { return nil, nil }
	if err := e.Submit("x", fn); err != nil {
		t.Fatal(err)
	}
	if err := e.Submit("x", fn); err != ErrDuplicateTask {
		t.Fatalf("expected ErrDuplicateTask, got %v", err)
	}
	e.Shutdown(time.Second)
	e.Wait()
}

func TestShutdownIsIdempotentAndReportRepeatable(t *testing.T) {
	e := New(1)
	e.Submit("a", func(ctx context.Context) (interface{}, error) { return 1, nil })
	r1 := e.Shutdown(time.Second)
	r2 := e.Shutdown(time.Second) // must not deadlock or reset state
	r3 := e.Report()
	if !r1.Drained || !r2.Drained || !r3.Drained {
		t.Fatalf("inconsistent reports: %+v %+v %+v", r1, r2, r3)
	}
	if len(r2.Completed) != 1 || len(r3.Completed) != 1 {
		t.Fatalf("repeat queries diverge: %+v %+v", r2, r3)
	}
	assertNoOverlap(t, r2)
	assertNoOverlap(t, r3)
	e.Wait()
}
