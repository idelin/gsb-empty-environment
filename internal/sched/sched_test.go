package sched

import (
	"strings"
	"testing"
)

func TestExploreEnumeratesAllInterleavings(t *testing.T) {
	// Two threads with two steps each: the four appended markers have
	// C(4,2) = 6 distinct orders. Each thread is also resumed once more
	// to return, so the scheduler explores C(6,3) = 20 runs.
	seen := map[string]bool{}
	runs, err := Explore(func(s *Sched) func() {
		var order []int
		for id := 0; id < 2; id++ {
			id := id
			s.Go(func() {
				for step := 0; step < 2; step++ {
					s.Step()
					order = append(order, id)
				}
			})
		}
		return func() {
			var b strings.Builder
			for _, id := range order {
				b.WriteByte(byte('0' + id))
			}
			seen[b.String()] = true
		}
	}, Config{MaxPreemptions: -1})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 20 || len(seen) != 6 {
		t.Fatalf("got %d runs, %d distinct orders; want 20/6", runs, len(seen))
	}
	for _, want := range []string{"0011", "0101", "0110", "1001", "1010", "1100"} {
		if !seen[want] {
			t.Errorf("missing interleaving %s", want)
		}
	}
}

func TestExplorePreemptionBound(t *testing.T) {
	// Three threads with two steps each (three resumes per thread
	// including the final return): unbounded exploration has
	// 9!/(3!3!3!) = 1680 schedules; forbidding preemptions leaves only
	// the 3! = 6 schedules that run each thread to completion.
	count := func(cfg Config) int {
		runs, err := Explore(func(s *Sched) func() {
			for id := 0; id < 3; id++ {
				s.Go(func() {
					s.Step()
					s.Step()
				})
			}
			return func() {}
		}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return runs
	}
	if got := count(Config{MaxPreemptions: -1}); got != 1680 {
		t.Errorf("unbounded: got %d runs, want 1680", got)
	}
	if got := count(Config{MaxPreemptions: 0}); got != 6 {
		t.Errorf("no preemptions: got %d runs, want 6", got)
	}
}

func TestExploreBlockUntil(t *testing.T) {
	ready := false
	var order []string
	runs, err := Explore(func(s *Sched) func() {
		ready = false
		order = nil
		s.Go(func() {
			s.Step()
			ready = true
			order = append(order, "set")
		})
		s.Go(func() {
			s.BlockUntil(func() bool { return ready })
			order = append(order, "wait")
		})
		return func() {}
	}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if runs != 2 {
		// The waiter may or may not park before the setter's first
		// step; both schedules converge to the same order.
		t.Fatalf("got %d runs, want 2", runs)
	}
	if len(order) != 2 || order[0] != "set" || order[1] != "wait" {
		t.Fatalf("got order %v, want [set wait]", order)
	}
}

func TestExploreDeadlock(t *testing.T) {
	_, err := Explore(func(s *Sched) func() {
		s.Go(func() {
			s.BlockUntil(func() bool { return false })
		})
		return func() {}
	}, Config{})
	if err == nil || !strings.Contains(err.Error(), "deadlock") {
		t.Fatalf("got %v, want deadlock error", err)
	}
}
