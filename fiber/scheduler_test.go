package fiber

import (
	"fmt"
	"testing"
)

// buildThreeFiberLog spawns 3 fibers that each Yield `yields` times and
// returns the scheduler's event log.
func buildThreeFiberLog(yields int) []Event {
	s := New()
	for _, name := range []string{"A", "B", "C"} {
		s.Spawn(name, func(f *Fiber) {
			for i := 0; i < yields; i++ {
				f.Yield()
			}
		})
	}
	s.Run()
	return s.Log()
}

// TestThreeFibersAlternate verifies the acceptance scenario: 3 fibers
// strictly alternate A,B,C,A,B,C... in the RESUME sequence.
func TestThreeFibersAlternate(t *testing.T) {
	const yields = 4
	log := buildThreeFiberLog(yields)

	var resumes []int
	for _, e := range log {
		if e.Kind == EventResume {
			resumes = append(resumes, e.FiberID)
		}
	}
	// 4 yields per fiber means 5 resumes per fiber (the last runs to exit).
	if got, want := len(resumes), 3*(yields+1); got != want {
		t.Fatalf("resume count = %d, want %d", got, want)
	}
	for i, id := range resumes {
		if id != i%3 {
			t.Fatalf("resume[%d] = fiber %d, want fiber %d (round-robin order broken)", i, id, i%3)
		}
	}
}

// TestLogSequenceShape checks the exact expected event stream for a small run.
func TestLogSequenceShape(t *testing.T) {
	log := buildThreeFiberLog(2)

	type step struct {
		id   int
		kind EventKind
	}
	var got []step
	for _, e := range log {
		got = append(got, step{e.FiberID, e.Kind})
	}

	// Each fiber yields twice, so it is resumed three times: the third
	// resume runs the body to completion and records EXIT.
	want := []step{
		{0, EventSpawn}, {1, EventSpawn}, {2, EventSpawn},
		{0, EventResume}, {0, EventYield},
		{1, EventResume}, {1, EventYield},
		{2, EventResume}, {2, EventYield},
		{0, EventResume}, {0, EventYield},
		{1, EventResume}, {1, EventYield},
		{2, EventResume}, {2, EventYield},
		{0, EventResume}, {0, EventExit},
		{1, EventResume}, {1, EventExit},
		{2, EventResume}, {2, EventExit},
	}
	if len(got) != len(want) {
		t.Fatalf("log length = %d, want %d\nlog:\n%s", len(got), len(want), render(log))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v\nlog:\n%s", i, got[i], want[i], render(log))
		}
	}

	// Ticks must be dense: 0,1,2,...,len-1.
	for i, e := range log {
		if e.Tick != uint64(i) {
			t.Fatalf("event %d has tick %d, ticks must be dense", i, e.Tick)
		}
	}
}

// TestDeterministicAcrossRuns runs the same program many times and requires
// byte-identical logs: no concurrency nondeterminism may leak into the log.
func TestDeterministicAcrossRuns(t *testing.T) {
	const runs = 200
	reference := render(buildThreeFiberLog(10))
	for i := 0; i < runs; i++ {
		if got := render(buildThreeFiberLog(10)); got != reference {
			t.Fatalf("run %d diverged from reference log\nreference:\n%s\ngot:\n%s", i, reference, got)
		}
	}
}

// TestSpawnFromFiber verifies fibers can spawn new fibers mid-run and the
// new fiber joins the back of the ready queue deterministically.
func TestSpawnFromFiber(t *testing.T) {
	s := New()
	s.Spawn("parent", func(f *Fiber) {
		f.sched.Spawn("child", func(c *Fiber) {
			c.Yield()
		})
		f.Yield()
	})
	s.Run()

	var resumes []string
	for _, e := range s.Log() {
		if e.Kind == EventResume {
			resumes = append(resumes, e.Name)
		}
	}
	// parent spawns child (enqueued behind parent), then yields and is
	// itself requeued behind child: FIFO gives parent, child, parent, child.
	want := []string{"parent", "child", "parent", "child"}
	if fmt.Sprint(resumes) != fmt.Sprint(want) {
		t.Fatalf("resume order = %v, want %v", resumes, want)
	}
}

// TestZeroFiberRun is a sanity check: an empty scheduler terminates immediately.
func TestZeroFiberRun(t *testing.T) {
	s := New()
	if ticks := s.Run(); ticks != 0 {
		t.Fatalf("empty run produced %d ticks, want 0", ticks)
	}
}

func render(log []Event) string {
	s := ""
	for _, e := range log {
		s += e.String() + "\n"
	}
	return s
}
