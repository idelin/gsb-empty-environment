// Command demo runs a live priority-inheritance scenario against the
// real clock and prints a timeline:
//
//  1. Low-priority C holds lock-2; mid-priority B holds lock-1 and
//     then blocks on lock-2 -> C inherits B's priority.
//  2. High-priority A blocks on lock-1 -> the boost propagates
//     transitively: B and C both run at A's priority.
//  3. A is canceled -> the boosts fall back along the chain.
//  4. C releases lock-2 -> B acquires it, finishes, and priorities
//     revert to base.
package main

import (
	"fmt"
	"time"

	"tasklock"
)

var started = time.Now()

func logf(format string, args ...any) {
	fmt.Printf("[%6.1fs] %s\n", time.Since(started).Seconds(), fmt.Sprintf(format, args...))
}

func main() {
	rt := tasklock.NewRuntime(nil) // real clock
	lock1 := rt.NewLock("lock-1")
	lock2 := rt.NewLock("lock-2")

	a := rt.NewTask("A(high)", 9)
	b := rt.NewTask("B(mid)", 5)
	c := rt.NewTask("C(low)", 1)

	show := func(note string) {
		logf("%-34s A.eff=%d B.eff=%d C.eff=%d", note, a.Priority(), b.Priority(), c.Priority())
	}

	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}

	// C(low) holds lock-2; B(mid) holds lock-1.
	must(c.Acquire(lock2, 0))
	must(b.Acquire(lock1, 0))
	show("C holds lock-2, B holds lock-1")

	// B blocks on lock-2: C inherits B's priority.
	bDone := rt.Go(b, func(t *tasklock.Task) error { return t.Acquire(lock2, 0) })
	time.Sleep(100 * time.Millisecond)
	show("B blocked on lock-2")

	// A blocks on lock-1: A's priority propagates B -> C.
	aDone := rt.Go(a, func(t *tasklock.Task) error { return t.Acquire(lock1, 0) })
	time.Sleep(100 * time.Millisecond)
	show("A blocked on lock-1 (transitive)")

	// A is canceled: its boost is revoked along the whole chain.
	rt.Cancel(a)
	logf("A canceled: acquire returned %v", <-aDone)
	time.Sleep(50 * time.Millisecond)
	show("after A cancel (boost revoked)")

	// C releases lock-2: B is granted it, then unwinds.
	must(c.Release(lock2))
	must(<-bDone)
	time.Sleep(50 * time.Millisecond)
	show("C released lock-2, B granted")

	must(b.Release(lock2))
	must(b.Release(lock1))
	time.Sleep(50 * time.Millisecond)
	show("B released both locks")

	// A tries again, now with a timeout while C re-acquires lock-1.
	must(c.Acquire(lock1, 0))
	err := a.Acquire(lock1, 150*time.Millisecond)
	logf("A acquire with 150ms timeout while C holds lock-1: %v", err)
	show("after A timeout (boost revoked)")
	must(c.Release(lock1))

	logf("demo done")
}
