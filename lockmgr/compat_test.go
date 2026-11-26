package lockmgr

import "testing"

func TestCompatibilityMatrix(t *testing.T) {
	cases := []struct {
		a, b LockMode
		want bool
	}{
		{IS, IS, true}, {IS, IX, true}, {IS, S, true}, {IS, SIX, true}, {IS, X, false},
		{IX, IS, true}, {IX, IX, true}, {IX, S, false}, {IX, SIX, false}, {IX, X, false},
		{S, IS, true}, {S, IX, false}, {S, S, true}, {S, SIX, false}, {S, X, false},
		{SIX, IS, true}, {SIX, IX, false}, {SIX, S, false}, {SIX, SIX, false}, {SIX, X, false},
		{X, IS, false}, {X, IX, false}, {X, S, false}, {X, SIX, false}, {X, X, false},
	}
	for _, c := range cases {
		if got := compatible(c.a, c.b); got != c.want {
			t.Errorf("compatible(%s,%s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSIXCompatibilityBehavior(t *testing.T) {
	m := newManager(t)
	db, table, _, _ := testResources()

	holder := m.Begin(1)
	mustLock(t, m, holder, db, IX)
	mustLock(t, m, holder, table, SIX)

	// SIX is compatible with IS from another transaction.
	isTxn := m.Begin(2)
	if err := m.Lock(isTxn, db, IS); err != nil {
		t.Fatalf("IS on DB should be compatible with SIX path: %v", err)
	}
	if err := m.TryLock(isTxn, table, IS); err != nil {
		t.Fatalf("IS should be compatible with SIX: %v", err)
	}

	// SIX conflicts with S, IX, SIX and X.
	for _, mode := range []LockMode{S, IX, SIX, X} {
		other := m.Begin(0)
		if mode == S {
			mustLock(t, m, other, db, IS)
		} else {
			mustLock(t, m, other, db, IX)
		}
		if err := m.TryLock(other, table, mode); err != ErrIncompatible {
			t.Fatalf("%s against SIX: got %v, want ErrIncompatible", mode, err)
		}
	}

	if err := m.Abort(isTxn); err != nil {
		t.Fatal(err)
	}
	if err := m.Abort(holder); err != nil {
		t.Fatal(err)
	}
}

func TestLockConversions(t *testing.T) {
	cases := []struct {
		a, b, want LockMode
	}{
		{IS, IX, IX},
		{IX, IS, IX},
		{S, IX, SIX},
		{IX, S, SIX},
		{S, IS, S},
		{SIX, IS, SIX},
		{S, X, X},
		{S, SIX, SIX},
	}
	for _, c := range cases {
		if got := unionMode(c.a, c.b); got != c.want {
			t.Errorf("unionMode(%s,%s) = %s, want %s", c.a, c.b, got, c.want)
		}
	}
}

func TestIntentionPrerequisiteEnforced(t *testing.T) {
	m := newManager(t)
	db, table, page, row := testResources()
	txn := m.Begin(1)

	// Locking a table without any DB lock must fail.
	if err := m.TryLock(txn, table, S); err != ErrIntentionViolation {
		t.Fatalf("table S without DB lock: got %v, want ErrIntentionViolation", err)
	}

	mustLock(t, m, txn, db, IS)
	// An IX descendant escalates the held IS ancestor to IX automatically.
	if err := m.TryLock(txn, table, IX); err != nil {
		t.Fatalf("table IX under IS parent should escalate: %v", err)
	}
	if m.HeldLock(txn, db) != IX {
		t.Fatalf("DB mode = %s, want escalated IX", m.HeldLock(txn, db))
	}
	// A genuinely missing ancestor is still rejected.
	orphanPage := NewPage("db9", "t9", "p9")
	if err := m.TryLock(txn, orphanPage, S); err != ErrIntentionViolation {
		t.Fatalf("page with no ancestors: got %v, want ErrIntentionViolation", err)
	}
	mustLock(t, m, txn, table, IS)
	mustLock(t, m, txn, page, IS)

	// Full IS chain permits an S row lock.
	if err := m.TryLock(txn, row, S); err != nil {
		t.Fatalf("row S under full IS chain: %v", err)
	}

	// IX chain is required for an X descendant.
	other := m.Begin(2)
	mustLock(t, m, other, db, IX)
	mustLock(t, m, other, table, IX)
	mustLock(t, m, other, page, IX)
	xRow := NewRow("db1", "t1", "p1", "rx")
	if err := m.TryLock(other, xRow, X); err != nil {
		t.Fatalf("free row X under full IX chain: %v", err)
	}

	// SIX on an ancestor still satisfies the IX requirement below it.
	third := m.Begin(3)
	sixDB := NewDB("db2")
	sixTable := NewTable("db2", "t1")
	sixPage := NewPage("db2", "t1", "p1")
	mustLock(t, m, third, sixDB, SIX)
	mustLock(t, m, third, sixTable, IX)
	mustLock(t, m, third, sixPage, IX)
	emptyRow := NewRow("db2", "t1", "p1", "rx")
	if err := m.TryLock(third, emptyRow, X); err != nil {
		t.Fatalf("row X under SIX ancestor chain: %v", err)
	}
	fourth := m.Begin(4)
	mustLock(t, m, fourth, sixDB, IS)
	if err := m.TryLock(fourth, sixTable, S); err != ErrIncompatible {
		t.Fatalf("table S against held IX: got %v, want ErrIncompatible", err)
	}
}
