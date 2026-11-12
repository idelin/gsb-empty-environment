package netsec

import (
	"sync"
	"testing"
)

// TestReentrancyExceptions covers every LIFO violation: return on an empty
// stack, a return whose tag does not match the innermost borrow, and a return
// that would pop the base identity frame. In all cases the stack must stay
// well-defined rather than corrupting identity state.
func TestReentrancyExceptions(t *testing.T) {
	t.Run("return on empty stack", func(t *testing.T) {
		m := New()
		mustHandle(t, m, Event{Kind: EventSwitch, ThreadID: "w", RequestID: "r"})
		mustHandle(t, m, Event{Kind: EventReturn, ThreadID: "w", Tag: "ghost"})

		if got := kindCount(m.Findings(), ReentrancyViolation); got != 1 {
			t.Fatalf("reentrancy findings = %d, want 1", got)
		}
		if m.StackDepth("w") != 0 {
			t.Fatal("empty-stack return must leave depth at 0")
		}
	})

	t.Run("mismatched borrow tag", func(t *testing.T) {
		m := New()
		mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: "w", Identity: ident("r", "alice")})
		mustHandle(t, m, Event{Kind: EventBorrow, ThreadID: "w", Identity: ident("r", "svc-a"), Tag: "outer"})
		mustHandle(t, m, Event{Kind: EventBorrow, ThreadID: "w", Identity: ident("r", "svc-b"), Tag: "inner"})

		mustHandle(t, m, Event{Kind: EventReturn, ThreadID: "w", Tag: "outer"})

		if got := kindCount(m.Findings(), ReentrancyViolation); got != 1 {
			t.Fatalf("reentrancy findings = %d, want 1", got)
		}
		if got := m.StackDepth("w"); got != 3 {
			t.Fatalf("depth after bad return = %d, want 3 (no frame popped)", got)
		}

		// Correct LIFO unwind still works after the rejected return.
		mustHandle(t, m, Event{Kind: EventReturn, ThreadID: "w", Tag: "inner"})
		mustHandle(t, m, Event{Kind: EventReturn, ThreadID: "w", Tag: "outer"})
		if got := m.StackDepth("w"); got != 1 {
			t.Fatalf("depth after correct unwind = %d, want 1", got)
		}
		if got := kindCount(m.Findings(), ReentrancyViolation); got != 1 {
			t.Fatalf("reentrancy findings = %d, still want 1", got)
		}
	})

	t.Run("return would pop base frame", func(t *testing.T) {
		m := New()
		mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: "w", Identity: ident("r", "alice")})
		mustHandle(t, m, Event{Kind: EventReturn, ThreadID: "w", Tag: "base"})

		findings := m.FindingsByKind(ReentrancyViolation)
		if len(findings) != 1 {
			t.Fatalf("reentrancy findings = %d, want 1", len(findings))
		}
		if m.StackDepth("w") != 1 {
			t.Fatal("base frame must survive a rejected return")
		}
		if len(findings[0].Chain) != 1 || findings[0].Chain[0].Kind != EventSetIdentity {
			t.Fatalf("finding chain = %+v, want the base set_identity hop", findings[0].Chain)
		}
	})

	t.Run("same identity nested borrow is allowed", func(t *testing.T) {
		m := New()
		mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: "w", Identity: ident("r", "alice")})
		mustHandle(t, m, Event{Kind: EventBorrow, ThreadID: "w", Identity: ident("r", "alice"), Tag: "reentrant"})
		mustHandle(t, m, Event{Kind: EventReturn, ThreadID: "w", Tag: "reentrant"})
		mustHandle(t, m, Event{Kind: EventExit, ThreadID: "w"})
		if len(m.Findings()) != 0 {
			t.Fatalf("legitimate reentrant borrow produced findings: %+v", m.Findings())
		}
	})
}

// TestAsyncSpawnSnapshot enforces that async children only receive identity
// through an explicit snapshot, and that the snapshot carries a full provenance
// chain. Implicit inheritance attempts must be flagged and start clean.
func TestAsyncSpawnSnapshot(t *testing.T) {
	t.Run("explicit snapshot is inherited", func(t *testing.T) {
		m := New()
		mustHandle(t, m, Event{Kind: EventSwitch, ThreadID: "parent", RequestID: "r-1"})
		mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: "parent", Identity: ident("r-1", "alice")})
		mustHandle(t, m, Event{Kind: EventBorrow, ThreadID: "parent", Identity: ident("r-1", "svc"), Tag: "call"})

		snap := m.Snapshot("parent")
		if snap.Depth() != 2 || snap.RequestID() != "r-1" {
			t.Fatalf("snapshot = depth %d request %q, want depth 2 r-1", snap.Depth(), snap.RequestID())
		}

		mustHandle(t, m, Event{Kind: EventSpawn, ThreadID: "parent", ChildID: "child", Snapshot: snap})

		read := m.Read("child")
		if !read.Found || read.Identity.Principal != "svc" {
			t.Fatalf("child read = %+v, want borrowed svc identity", read)
		}
		if len(read.Chain) != 3 {
			t.Fatalf("child chain hops = %d, want 3 (set, borrow, snapshot)", len(read.Chain))
		}
		if read.Chain[2].Kind != eventSnapshot {
			t.Fatalf("last chain hop = %s, want snapshot", read.Chain[2].Kind)
		}
		if len(m.Findings()) != 0 {
			t.Fatalf("explicit snapshot spawn produced findings: %+v", m.Findings())
		}

		mustHandle(t, m, Event{Kind: EventExit, ThreadID: "child"})
		mustHandle(t, m, Event{Kind: EventReturn, ThreadID: "parent", Tag: "call"})
		mustHandle(t, m, Event{Kind: EventExit, ThreadID: "parent"})
		if m.StackDepth("parent") != 0 || m.StackDepth("child") != 0 {
			t.Fatal("both threads must be at depth 0 after exit")
		}
	})

	t.Run("implicit inheritance is rejected", func(t *testing.T) {
		m := New()
		mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: "parent", Identity: ident("r-1", "alice")})
		mustHandle(t, m, Event{Kind: EventSpawn, ThreadID: "parent", ChildID: "child", Inherit: true})

		findings := m.FindingsByKind(ImplicitInheritance)
		if len(findings) != 1 {
			t.Fatalf("implicit inheritance findings = %d, want 1", len(findings))
		}
		if m.StackDepth("child") != 0 {
			t.Fatal("implicitly inheriting child must start with an empty stack")
		}
		if m.Read("child").Found {
			t.Fatal("child must not observe the parent identity")
		}
	})

	t.Run("spawn without snapshot starts clean", func(t *testing.T) {
		m := New()
		mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: "parent", Identity: ident("r-1", "alice")})
		mustHandle(t, m, Event{Kind: EventSpawn, ThreadID: "parent", ChildID: "child"})
		if m.StackDepth("child") != 0 || len(m.Findings()) != 0 {
			t.Fatalf("plain spawn leaked state: depth=%d findings=%+v", m.StackDepth("child"), m.Findings())
		}
	})

	t.Run("spawn onto dirty thread resets and reports", func(t *testing.T) {
		m := New()
		mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: "reused", Identity: ident("r-old", "alice")})
		mustHandle(t, m, Event{Kind: EventSpawn, ThreadID: "parent", ChildID: "reused"})

		if len(m.FindingsByKind(StaleIdentityRead)) != 1 {
			t.Fatalf("stale reads = %d, want 1", len(m.FindingsByKind(StaleIdentityRead)))
		}
		if m.StackDepth("reused") != 0 {
			t.Fatal("dirty child thread must be reset on spawn")
		}
	})
}

// TestDirtyExitForcesZero verifies exit always leaves the stack at zero even
// when borrow scopes were never closed.
func TestDirtyExitForcesZero(t *testing.T) {
	m := New()
	mustHandle(t, m, Event{Kind: EventSwitch, ThreadID: "w", RequestID: "r-1"})
	mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: "w", Identity: ident("r-1", "alice")})
	mustHandle(t, m, Event{Kind: EventBorrow, ThreadID: "w", Identity: ident("r-1", "svc"), Tag: "leaked"})
	mustHandle(t, m, Event{Kind: EventExit, ThreadID: "w"})

	if m.StackDepth("w") != 0 {
		t.Fatal("exit must force the stack to zero")
	}
	dirty := m.FindingsByKind(DirtyExit)
	if len(dirty) != 1 || len(dirty[0].Chain) != 2 {
		t.Fatalf("dirty exit finding = %+v, want 1 finding with a 2-hop chain", dirty)
	}

	// The same worker can now be reused without a stale read.
	mustHandle(t, m, Event{Kind: EventSwitch, ThreadID: "w", RequestID: "r-2"})
	if len(m.FindingsByKind(StaleIdentityRead)) != 0 {
		t.Fatalf("reuse after forced-zero exit reported stale read: %+v", m.FindingsByKind(StaleIdentityRead))
	}
}

// TestMalformedEvents verifies input validation surfaces errors rather than
// mutating monitor state.
func TestMalformedEvents(t *testing.T) {
	m := New()
	cases := []Event{
		{Kind: EventSwitch, ThreadID: ""},
		{Kind: EventSetIdentity, ThreadID: "w", Identity: Identity{Principal: "no-request"}},
		{Kind: EventSpawn, ThreadID: "w"},
		{Kind: EventKind(99), ThreadID: "w"},
	}
	for _, ev := range cases {
		if err := m.Handle(ev); err == nil {
			t.Fatalf("event %+v expected error, got %v", ev, err)
		}
	}
	if len(m.Findings()) != 0 {
		t.Fatalf("malformed events must not create findings: %+v", m.Findings())
	}
}

// TestConcurrentIngest exercises the mutex under parallel event streams.
func TestConcurrentIngest(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			thread := "w-" + itoa(g)
			for i := 0; i < 100; i++ {
				requestID := "r-" + itoa(i)
				_ = m.Handle(Event{Kind: EventSwitch, ThreadID: thread, RequestID: requestID})
				_ = m.Handle(Event{Kind: EventSetIdentity, ThreadID: thread, Identity: ident(requestID, "p")})
				_ = m.Handle(Event{Kind: EventExit, ThreadID: thread})
				_ = m.Snapshot(thread)
				_ = m.Read(thread)
			}
		}(g)
	}
	wg.Wait()

	for g := 0; g < 16; g++ {
		if depth := m.StackDepth("w-" + itoa(g)); depth != 0 {
			t.Fatalf("worker %d depth = %d, want 0", g, depth)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
