package netsec

import "testing"

func mustHandle(t *testing.T, m *Monitor, ev Event) {
	t.Helper()
	if err := m.Handle(ev); err != nil {
		t.Fatalf("Handle(%+v) unexpected error: %v", ev, err)
	}
}

func ident(requestID, principal string) Identity {
	return Identity{RequestID: requestID, Principal: principal}
}

func kindCount(findings []Finding, kind FindingKind) int {
	n := 0
	for _, f := range findings {
		if f.Kind == kind {
			n++
		}
	}
	return n
}

// TestCleanLifecycleIsSilent proves a request that installs identity, borrows
// nested scopes, unwinds them in order and exits leaves zero frames and produces
// no findings.
func TestCleanLifecycleIsSilent(t *testing.T) {
	m := New()
	const w = "w-1"

	mustHandle(t, m, Event{Kind: EventSwitch, ThreadID: w, RequestID: "r-1"})
	mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: w, Identity: ident("r-1", "alice")})
	mustHandle(t, m, Event{Kind: EventBorrow, ThreadID: w, Identity: ident("r-1", "svc-a"), Tag: "a"})
	mustHandle(t, m, Event{Kind: EventBorrow, ThreadID: w, Identity: ident("r-1", "svc-b"), Tag: "b"})

	if got := m.StackDepth(w); got != 3 {
		t.Fatalf("depth before returns = %d, want 3", got)
	}

	mustHandle(t, m, Event{Kind: EventReturn, ThreadID: w, Tag: "b"})
	mustHandle(t, m, Event{Kind: EventReturn, ThreadID: w, Tag: "a"})
	if got := m.StackDepth(w); got != 1 {
		t.Fatalf("depth after returns = %d, want 1 (base frame only)", got)
	}

	mustHandle(t, m, Event{Kind: EventExit, ThreadID: w})
	if got := m.StackDepth(w); got != 0 {
		t.Fatalf("depth after exit = %d, want 0", got)
	}
	if findings := m.Findings(); len(findings) != 0 {
		t.Fatalf("clean lifecycle produced findings: %+v", findings)
	}
}

// TestThreadReuseStaleIdentity drives two requests over the same worker. The
// first request leaks its identity (open borrow, no exit); the second request
// must observe a stale read whose source chain points back at the first request.
func TestThreadReuseStaleIdentity(t *testing.T) {
	m := New()
	const w = "w-pool-7"

	mustHandle(t, m, Event{Kind: EventSwitch, ThreadID: w, RequestID: "r-old"})
	mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: w, Identity: ident("r-old", "alice")})
	mustHandle(t, m, Event{Kind: EventBorrow, ThreadID: w, Identity: ident("r-old", "downstream"), Tag: "call-1"})

	// The pool hands the still-dirty worker to a brand new request.
	mustHandle(t, m, Event{Kind: EventSwitch, ThreadID: w, RequestID: "r-new"})

	stale := m.FindingsByKind(StaleIdentityRead)
	if len(stale) != 1 {
		t.Fatalf("stale reads after reuse switch = %d, want 1", len(stale))
	}

	// Explicit read from the new request returns the old identity plus the chain.
	read := m.Read(w)
	if !read.Found || read.Identity.RequestID != "r-old" || read.Identity.Principal != "downstream" {
		t.Fatalf("read returned %+v, want stale r-old/downstream", read)
	}
	if len(read.Chain) != 2 {
		t.Fatalf("source chain length = %d, want 2 (set_identity -> borrow)", len(read.Chain))
	}
	if read.Chain[0].Kind != EventSetIdentity || read.Chain[1].Kind != EventBorrow {
		t.Fatalf("chain order = %v, want set_identity then borrow", read.Chain)
	}
	if read.Chain[1].Principal != "downstream" {
		t.Fatalf("chain top principal = %q, want downstream", read.Chain[1].Principal)
	}

	// Second read keeps producing evidence (the leak is still live).
	if len(m.FindingsByKind(StaleIdentityRead)) != 2 {
		t.Fatalf("stale reads after explicit Read = %d, want 2", len(m.FindingsByKind(StaleIdentityRead)))
	}

	// The new request installing its own identity also reports the overwrite and
	// guarantees the old frame stack is fully replaced.
	mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: w, Identity: ident("r-new", "bob")})
	if got := m.StackDepth(w); got != 1 {
		t.Fatalf("depth after overwrite = %d, want 1", got)
	}
	if now := m.Read(w); now.Identity.RequestID != "r-new" {
		t.Fatalf("read after reset = %+v, want r-new", now)
	}

	// Lifecycle sequence proves reuse happened on the same thread.
	log := m.ThreadLog(w)
	if len(log) != 2 || log[0].RequestID != "r-old" || log[1].RequestID != "r-new" {
		t.Fatalf("thread log = %+v, want r-old then r-new switch", log)
	}
}

// TestSourceChainAcrossBorrows verifies nested borrows accumulate a hop per
// scope and the chain survives in the stale-read finding.
func TestSourceChainAcrossBorrows(t *testing.T) {
	m := New()
	const w = "w-2"

	mustHandle(t, m, Event{Kind: EventSwitch, ThreadID: w, RequestID: "r-1"})
	mustHandle(t, m, Event{Kind: EventSetIdentity, ThreadID: w, Identity: ident("r-1", "alice")})
	mustHandle(t, m, Event{Kind: EventBorrow, ThreadID: w, Identity: ident("r-1", "role-x"), Tag: "x"})
	mustHandle(t, m, Event{Kind: EventBorrow, ThreadID: w, Identity: ident("r-1", "role-y"), Tag: "y"})

	mustHandle(t, m, Event{Kind: EventSwitch, ThreadID: w, RequestID: "r-2"})

	stale := m.FindingsByKind(StaleIdentityRead)
	if len(stale) != 1 {
		t.Fatalf("stale reads = %d, want 1", len(stale))
	}
	chain := stale[0].Chain
	if len(chain) != 3 {
		t.Fatalf("chain hops = %d, want 3", len(chain))
	}
	wantKinds := []EventKind{EventSetIdentity, EventBorrow, EventBorrow}
	for i, want := range wantKinds {
		if chain[i].Kind != want {
			t.Fatalf("chain[%d] = %s, want %s", i, chain[i].Kind, want)
		}
	}
	if got := stale[0].SourceChain(); got == "" {
		t.Fatal("SourceChain() rendered empty string")
	}
}
