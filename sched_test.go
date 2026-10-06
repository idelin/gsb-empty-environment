package sched

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

type taskDef struct {
	id     string
	deps   []string
	needs  Resources
	script Script
}

func build(t *testing.T, capacity Resources, ex *Executor, defs []taskDef) *Scheduler {
	t.Helper()
	s := New(capacity)
	for _, d := range defs {
		ex.Handle(d.id, d.needs, d.script)
		if err := s.Add(Task{ID: d.id, Deps: d.deps, Needs: d.needs, Run: ex.RunFunc(d.id)}); err != nil {
			t.Fatalf("Add(%q): %v", d.id, err)
		}
	}
	return s
}

func eventStrings(ex *Executor) []string {
	var out []string
	for _, ev := range ex.Events() {
		out = append(out, ev.Kind+" "+ev.Task)
	}
	return out
}

func TestDeterministicOutcomesAndOrder(t *testing.T) {
	// Diamond DAG, capacity 1 forces a fully serial, deterministic order.
	defs := []taskDef{
		{id: "a", needs: Resources{"cpu": 1}},
		{id: "b", deps: []string{"a"}, needs: Resources{"cpu": 1}},
		{id: "c", deps: []string{"a"}, needs: Resources{"cpu": 1}},
		{id: "d", deps: []string{"b", "c"}, needs: Resources{"cpu": 1}},
	}
	want := []string{
		"start a", "end a",
		"start b", "end b",
		"start c", "end c",
		"start d", "end d",
	}
	for run := 0; run < 2; run++ {
		ex := NewExecutor(Resources{"cpu": 1})
		s := build(t, Resources{"cpu": 1}, ex, defs)
		outcomes, err := s.Run(context.Background())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		for _, d := range defs {
			if o := outcomes[d.id]; o.State != StateSucceeded || o.Err != nil {
				t.Fatalf("run %d: task %q = %v, %v", run, d.id, o.State, o.Err)
			}
		}
		if got := eventStrings(ex); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d: events = %v, want %v", run, got, want)
		}
	}
}

func TestFailureSkipsOnlyDependents(t *testing.T) {
	// a fails; b depends on a; c is independent; d depends on b and c.
	ex := NewExecutor(Resources{"cpu": 8})
	s := build(t, Resources{"cpu": 8}, ex, []taskDef{
		{id: "a", needs: Resources{"cpu": 1}, script: Script{Err: errBoom}},
		{id: "b", deps: []string{"a"}, needs: Resources{"cpu": 1}},
		{id: "c", needs: Resources{"cpu": 1}, script: Script{Work: 5 * time.Millisecond}},
		{id: "d", deps: []string{"b", "c"}, needs: Resources{"cpu": 1}},
	})
	outcomes, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if o := outcomes["a"]; o.State != StateFailed || o.Err != errBoom {
		t.Fatalf("a = %v, %v", o.State, o.Err)
	}
	if o := outcomes["c"]; o.State != StateSucceeded {
		t.Fatalf("c = %v (independent branch must keep running)", o.State)
	}
	for _, id := range []string{"b", "d"} {
		if o := outcomes[id]; o.State != StateSkipped {
			t.Fatalf("%s = %v, want skipped", id, o.State)
		}
	}
	// b was skipped directly because of a.
	dep, ok := outcomes["b"].Err.(*DepError)
	if !ok || dep.Task != "b" || dep.Dep != "a" {
		t.Fatalf("b.Err = %#v", outcomes["b"].Err)
	}
	if RootCause(outcomes["b"].Err) != errBoom {
		t.Fatalf("b root cause = %v", RootCause(outcomes["b"].Err))
	}
	// d's chain leads through b back to boom.
	chain := Chain(outcomes["d"].Err)
	if len(chain) != 3 {
		t.Fatalf("d chain = %v", chain)
	}
	if RootCause(outcomes["d"].Err) != errBoom {
		t.Fatalf("d root cause = %v", RootCause(outcomes["d"].Err))
	}
	// Skipped tasks never started.
	for _, id := range []string{"b", "d"} {
		if ex.Started(id) {
			t.Fatalf("skipped task %q must not start", id)
		}
	}
}

func TestTransitiveReasonChain(t *testing.T) {
	ex := NewExecutor(Resources{"cpu": 4})
	s := build(t, Resources{"cpu": 4}, ex, []taskDef{
		{id: "a", needs: Resources{"cpu": 1}, script: Script{Err: errBoom}},
		{id: "b", deps: []string{"a"}, needs: Resources{"cpu": 1}},
		{id: "c", deps: []string{"b"}, needs: Resources{"cpu": 1}},
	})
	outcomes, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		`task "c" skipped: dependency "b" did not succeed`,
		`task "b" skipped: dependency "a" did not succeed`,
		"boom",
	}
	if got := Chain(outcomes["c"].Err); !reflect.DeepEqual(got, want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
}

func TestResourceConservation(t *testing.T) {
	capacity := Resources{"cpu": 4, "mem": 6}
	defs := []taskDef{
		{id: "t00", needs: Resources{"cpu": 2, "mem": 1}},
		{id: "t01", needs: Resources{"cpu": 1, "mem": 2}},
		{id: "t02", deps: []string{"t00"}, needs: Resources{"cpu": 3}},
		{id: "t03", deps: []string{"t00", "t01"}, needs: Resources{"cpu": 1, "mem": 3}},
		{id: "t04", deps: []string{"t01"}, needs: Resources{"cpu": 2, "mem": 2}},
		{id: "t05", deps: []string{"t02", "t03"}, needs: Resources{"cpu": 1}},
		{id: "t06", deps: []string{"t03", "t04"}, needs: Resources{"mem": 4}},
		{id: "t07", deps: []string{"t05", "t06"}, needs: Resources{"cpu": 4, "mem": 1}},
		{id: "t08", needs: Resources{"mem": 2}},
		{id: "t09", deps: []string{"t08"}, needs: Resources{"cpu": 1, "mem": 1}},
	}
	ex := NewExecutor(capacity)
	for i := range defs {
		defs[i].script = Script{Work: time.Millisecond}
	}
	s := build(t, capacity, ex, defs)
	outcomes, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, d := range defs {
		if o := outcomes[d.id]; o.State != StateSucceeded {
			t.Fatalf("%s = %v, %v", d.id, o.State, o.Err)
		}
	}
	if v := ex.Violations(); len(v) > 0 {
		t.Fatalf("capacity violations: %v", v)
	}
	for name, q := range ex.MaxHeld() {
		if q > capacity[name] {
			t.Fatalf("peak %s=%d exceeds capacity %d", name, q, capacity[name])
		}
	}
	if held := ex.Held(); len(held) != 0 {
		for name, q := range held {
			if q != 0 {
				t.Fatalf("resource leak: %s=%d still held", name, q)
			}
		}
	}
}

func TestAllOrNothingAdmission(t *testing.T) {
	// Capacity 4: x needs 3, y and z need 2 each. x starts alone;
	// only after x releases do y and z start together.
	capacity := Resources{"cpu": 4}
	ex := NewExecutor(capacity)
	s := build(t, capacity, ex, []taskDef{
		{id: "x", needs: Resources{"cpu": 3}, script: Script{Work: 5 * time.Millisecond}},
		{id: "y", needs: Resources{"cpu": 2}, script: Script{Work: 5 * time.Millisecond}},
		{id: "z", needs: Resources{"cpu": 2}, script: Script{Work: 5 * time.Millisecond}},
	})
	outcomes, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"x", "y", "z"} {
		if outcomes[id].State != StateSucceeded {
			t.Fatalf("%s = %v", id, outcomes[id].State)
		}
	}
	got := eventStrings(ex)
	if len(got) < 4 || got[0] != "start x" || got[1] != "end x" {
		t.Fatalf("x must run alone first, events = %v", got)
	}
	pair := map[string]bool{got[2]: true, got[3]: true}
	if !pair["start y"] || !pair["start z"] {
		t.Fatalf("y and z must start together after x, events = %v", got)
	}
	if v := ex.Violations(); len(v) > 0 {
		t.Fatalf("capacity violations: %v", v)
	}
	if peak := ex.MaxHeld()["cpu"]; peak > 4 {
		t.Fatalf("peak cpu = %d", peak)
	}
}

func TestCancelWaitsForCleanup(t *testing.T) {
	capacity := Resources{"cpu": 1}
	ex := NewExecutor(capacity)
	s := build(t, capacity, ex, []taskDef{
		{id: "long", needs: Resources{"cpu": 1}, script: Script{WaitForCancel: true, Cleanup: 50 * time.Millisecond}},
		{id: "queued", deps: []string{"long"}, needs: Resources{"cpu": 1}},
		{id: "ready", needs: Resources{"cpu": 1}}, // admitted only after long releases cpu
	})
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		outcomes map[string]Outcome
		err      error
	}
	resCh := make(chan result, 1)
	go func() {
		o, err := s.Run(ctx)
		resCh <- result{o, err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !ex.Started("long") {
		if time.Now().After(deadline) {
			t.Fatal("long task never started")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	res := <-resCh
	if res.err != nil {
		t.Fatalf("Run: %v", res.err)
	}
	// Run returned only after the started task finished its cleanup.
	select {
	case <-ex.CleanedUp("long"):
	default:
		t.Fatal("Run returned before cleanup of the started task finished")
	}
	if o := res.outcomes["long"]; o.State != StateSucceeded {
		t.Fatalf("long = %v, %v", o.State, o.Err)
	}
	for _, id := range []string{"queued", "ready"} {
		if o := res.outcomes[id]; o.State != StateCanceled {
			t.Fatalf("%s = %v, want canceled", id, o.State)
		}
		if ex.Started(id) {
			t.Fatalf("canceled task %q must not start", id)
		}
	}
	if held := ex.Held()["cpu"]; held != 0 {
		t.Fatalf("resource leak after cancel: cpu=%d", held)
	}
}

func TestCancelBeforeStart(t *testing.T) {
	ex := NewExecutor(Resources{"cpu": 2})
	s := build(t, Resources{"cpu": 2}, ex, []taskDef{
		{id: "a", needs: Resources{"cpu": 1}},
		{id: "b", deps: []string{"a"}, needs: Resources{"cpu": 1}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcomes, err := s.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		if outcomes[id].State != StateCanceled {
			t.Fatalf("%s = %v, want canceled", id, outcomes[id].State)
		}
	}
	if len(ex.Events()) != 0 {
		t.Fatalf("no task may start after cancel, events = %v", eventStrings(ex))
	}
}

func TestValidation(t *testing.T) {
	cap := Resources{"cpu": 2}
	run := func() func(context.Context) error { return func(context.Context) error { return nil } }

	s := New(cap)
	if err := s.Add(Task{ID: "a", Run: run()}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Task{ID: "a", Run: run()}); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	if err := s.Add(Task{ID: "big", Needs: Resources{"cpu": 3}, Run: run()}); err == nil {
		t.Fatal("oversize request accepted")
	}
	if err := s.Add(Task{ID: "nil"}); err == nil {
		t.Fatal("nil Run accepted")
	}

	s = New(cap)
	_ = s.Add(Task{ID: "a", Deps: []string{"ghost"}, Run: run()})
	if _, err := s.Run(context.Background()); err == nil {
		t.Fatal("unknown dependency accepted")
	}

	s = New(cap)
	_ = s.Add(Task{ID: "a", Deps: []string{"c"}, Run: run()})
	_ = s.Add(Task{ID: "b", Deps: []string{"a"}, Run: run()})
	_ = s.Add(Task{ID: "c", Deps: []string{"b"}, Run: run()})
	if _, err := s.Run(context.Background()); err == nil {
		t.Fatal("cycle accepted")
	} else {
		fmt.Println("cycle rejected:", err)
	}
}
