package lockmgr

import (
	"testing"
	"time"
)

func TestWaitingForGraphMultipleEdges(t *testing.T) {
	m := newManager(t)
	db, table, page, row := testResources()

	s1 := m.Begin(1)
	s2 := m.Begin(2)
	s3 := m.Begin(3)
	for _, txn := range []*Transaction{s1, s2, s3} {
		acquireHierarchy(t, m, txn, db, table, page, S)
		mustLock(t, m, txn, row, S)
	}

	writer := m.Begin(4)
	acquireHierarchy(t, m, writer, db, table, page, X)
	ch := asyncLock(m, writer, row, X)
	waitFor(t, time.Second, func() bool { return len(m.Snapshot(row)) == 1 })

	edges := m.WaitForEdges()
	if len(edges) != 3 {
		t.Fatalf("edge count = %d, want one edge per shared holder (3)", len(edges))
	}
	targets := map[uint64]int{}
	for _, e := range edges {
		if e.From != 4 {
			t.Fatalf("edge from %d, want only writer 4", e.From)
		}
		targets[e.To]++
	}
	for _, id := range []uint64{1, 2, 3} {
		if targets[id] != 1 {
			t.Fatalf("edge to %d count = %d, want 1", id, targets[id])
		}
	}

	// No cycle yet with a single blocked writer.
	if cycles := m.DeadlockCycles(); len(cycles) != 0 {
		t.Fatalf("unexpected cycles: %v", cycles)
	}

	// Self edges never appear: s1's re-entrant request is granted directly.
	if err := m.Lock(s1, row, S); err != nil {
		t.Fatalf("re-entrant S: %v", err)
	}
	for _, e := range m.WaitForEdges() {
		if e.From == e.To {
			t.Fatalf("self edge present: %+v", e)
		}
	}

	if err := m.Abort(writer); err != nil {
		t.Fatal(err)
	}
	<-ch
}

func TestCascadeWakeupOrder(t *testing.T) {
	m := newManager(t)
	db, table, page, row := testResources()

	// t1 holds X; a chain of waiters queues: X(t2), S(t3), IS(t4).
	owner := m.Begin(1)
	acquireHierarchy(t, m, owner, db, table, page, X)
	mustLock(t, m, owner, row, X)

	t2 := m.Begin(2)
	acquireHierarchy(t, m, t2, db, table, page, X)
	t3 := m.Begin(3)
	acquireHierarchy(t, m, t3, db, table, page, S)
	t4 := m.Begin(4)
	acquireHierarchy(t, m, t4, db, table, page, IS)

	// Enqueue under the manager lock in a deterministic order: X(t2), S(t3),
	// IS(t4), so the test isolates queue/cascade behavior from goroutine
	// scheduling rather than lock semantics.
	m.mu.Lock()
	w2 := m.enqueue(m.state(row), t2, X, false)
	w3 := m.enqueue(m.state(row), t3, S, false)
	w4 := m.enqueue(m.state(row), t4, IS, false)
	m.resolveDeadlocks()
	m.mu.Unlock()
	ch2, ch3, ch4 := w2.ch, w3.ch, w4.ch

	waitFor(t, time.Second, func() bool { return len(m.Snapshot(row)) == 3 })

	// Abort the owner: t2's X is granted first and blocks t3/t4.
	if err := m.Abort(owner); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return m.HeldLock(t2, row) == X })
	if m.HeldLock(t2, row) != X {
		t.Fatalf("t2 mode = %s, want X granted at head", m.HeldLock(t2, row))
	}
	select {
	case r := <-ch2:
		if r != nil {
			t.Fatalf("t2: %v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("t2 result never delivered")
	}

	// Then abort t2: the two compatible readers t3(S) and t4(IS) must both be
	// granted in queue order from a single pump cycle.
	if err := m.Abort(t2); err != nil {
		t.Fatal(err)
	}

	waitFor(t, time.Second, func() bool {
		return m.HeldLock(t3, row) == S && m.HeldLock(t4, row) == IS
	})
	select {
	case r := <-ch3:
		if r != nil {
			t.Fatalf("t3 result: %v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("t3 result never delivered")
	}
	select {
	case r := <-ch4:
		if r != nil {
			t.Fatalf("t4 result: %v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("t4 result never delivered")
	}
	if m.HeldLock(t3, row) != S || m.HeldLock(t4, row) != IS {
		t.Fatalf("cascade grants wrong: t3=%s t4=%s", m.HeldLock(t3, row), m.HeldLock(t4, row))
	}
	if len(m.Snapshot(row)) != 0 {
		t.Fatalf("queue should be empty after cascade: %+v", m.Snapshot(row))
	}
}

func TestCascadeAcrossResourcesAfterSavepoint(t *testing.T) {
	m := newManager(t)
	db, table, page, row := testResources()
	otherRow := NewRow("db1", "t1", "p1", "r2")

	// Two independent resources are blocked by one victim; its savepoint
	// rollback must repump both queues.
	victim := m.Begin(1)
	waiterA := m.Begin(2)
	waiterB := m.Begin(3)

	// Ancestor intention locks are acquired before the savepoint and survive
	// the rollback; the two row X locks come after it and get released.
	acquireHierarchy(t, m, victim, db, table, page, X)
	if _, err := m.CreateSavepoint(victim, "before-rows"); err != nil {
		t.Fatal(err)
	}
	mustLock(t, m, victim, row, X)
	mustLock(t, m, victim, otherRow, X)

	acquireHierarchy(t, m, waiterA, db, table, page, S)
	acquireHierarchy(t, m, waiterB, db, table, page, S)
	chA := asyncLock(m, waiterA, row, S)
	chB := asyncLock(m, waiterB, otherRow, S)
	waitFor(t, time.Second, func() bool {
		return len(m.Snapshot(row)) == 1 && len(m.Snapshot(otherRow)) == 1
	})

	// A direct user savepoint rollback releases both X locks and cascades the
	// grants without any deadlock machinery.
	sps := victim.savepoints
	if err := m.RollbackToSavepoint(victim, sps[0].Savepoint); err != nil {
		t.Fatal(err)
	}

	for name, ch := range map[string]<-chan lockResult{"r1": chA, "r2": chB} {
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("%s grant: %v", name, r.err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s was not cascaded awake", name)
		}
	}
	if m.HeldLock(waiterA, row) != S || m.HeldLock(waiterB, otherRow) != S {
		t.Fatalf("cross-resource cascade failed: %s %s", m.HeldLock(waiterA, row), m.HeldLock(waiterB, otherRow))
	}
	// Ancestor intention locks acquired before the savepoint survive.
	if m.HeldLock(victim, db) == None {
		t.Fatal("victim lost pre-savepoint ancestor locks")
	}
}
