package sim

import "testing"

func TestEventOrdering(t *testing.T) {
	s := New(1000)
	var order []int
	// Same-instant scheduling must be FIFO; earlier instants go first.
	s.After(5, func(*Sim) { order = append(order, 3) })
	s.After(1, func(*Sim) { order = append(order, 0) })
	s.After(1, func(*Sim) { order = append(order, 1) })
	s.After(0, func(*Sim) { order = append(order, 2) }) // t=0 deferred, before t=1
	s.Run()
	want := []int{2, 0, 1, 3}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
	if s.Now() != 5 {
		t.Fatalf("final clock = %d, want 5", s.Now())
	}
}

func TestUniqueIDs(t *testing.T) {
	s := New(10)
	seen := map[uint64]bool{}
	for i := 0; i < 50; i++ {
		id := s.NextID()
		if seen[id] {
			t.Fatalf("duplicate id %d", id)
		}
		seen[id] = true
	}
}

func TestCancelTree(t *testing.T) {
	s := New(100)
	root := RootCtl(s)
	hooks := map[string]int{}
	c1 := root.Child(func(string) { hooks["c1"]++ })
	c2 := root.Child(func(string) { hooks["c2"]++ })
	g1 := c1.Child(func(string) { hooks["g1"]++ })
	c1.Child(nil) // hookless child must not panic

	root.Cancel("deadline exceeded")

	if !root.Canceled() || !c1.Canceled() || !c2.Canceled() || !g1.Canceled() {
		t.Fatal("cancel did not reach the whole subtree")
	}
	if hooks["c1"] != 1 || hooks["c2"] != 1 || hooks["g1"] != 1 {
		t.Fatalf("hook counts = %v, want exactly one each", hooks)
	}
	if !c1.DeadlineHit() || !g1.DeadlineHit() {
		t.Fatal("deadline reason must propagate")
	}

	// One-shot: late cancel/reasons do not re-run hooks.
	root.Cancel("something else")
	g1.Cancel("x")
	if hooks["g1"] != 1 {
		t.Fatalf("hook re-ran: %v", hooks)
	}
	if !g1.DeadlineHit() {
		t.Fatal("late cancel must not overwrite the first reason")
	}
}

func TestChildAfterCancelIsBornCanceled(t *testing.T) {
	s := New(10)
	root := RootCtl(s)
	root.Cancel("parent canceled")
	ran := false
	child := root.Child(func(string) { ran = true })
	if !child.Canceled() || !ran {
		t.Fatal("a child born after cancel must be canceled and its hook fired")
	}
}

func TestRunBudgetPanicsOnStorm(t *testing.T) {
	s := New(3)
	defer func() {
		if recover() == nil {
			t.Fatal("want panic on event-budget exhaustion")
		}
	}()
	// Self-rescheduling loop that never settles.
	var loop func(*Sim)
	loop = func(*Sim) { s.After(1, loop) }
	s.After(0, loop)
	s.Run()
}
