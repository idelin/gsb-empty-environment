package checker

import (
	"fmt"
	"math/rand"
	"testing"
)

// bruteForceGreatest enumerates every cut with -1 <= cut[p] <= target[p] and
// returns the componentwise greatest consistent one. Consistent cuts are
// closed under componentwise join, so the result is unique.
func bruteForceGreatest(l *Log, target Cut) (Cut, bool) {
	names := l.Order
	best := Cut{}
	found := false
	var rec func(i int, cur Cut)
	rec = func(i int, cur Cut) {
		if i == len(names) {
			if len(l.ValidateCut(cur)) != 0 {
				return
			}
			if !found {
				best = cur.Copy()
				found = true
				return
			}
			for _, n := range names {
				if cur[n] > best[n] {
					best[n] = cur[n]
				}
			}
			return
		}
		n := names[i]
		for v := Version(-1); v <= target[n]; v++ {
			cur[n] = v
			rec(i+1, cur)
		}
	}
	rec(0, Cut{})
	return best, found
}

func assertGreatest(t *testing.T, l *Log, target Cut) {
	t.Helper()
	got, _ := l.FindRecoverableCut(target)
	want, ok := bruteForceGreatest(l, target)
	if !ok {
		t.Fatalf("no consistent cut exists below target %v", target)
	}
	for _, n := range l.Order {
		if got[n] != want[n] {
			t.Fatalf("target %v: partition %s: got v%d, brute-force greatest is v%d (full got=%v want=%v)",
				target, n, got[n], want[n], got, want)
		}
	}
	if vs := l.ValidateCut(got); len(vs) > 0 {
		t.Fatalf("target %v: computed cut %v still has %d violations", target, got, len(vs))
	}
}

// TestEnumerateSmallTxnSets randomly generates small logs (2-3 partitions,
// 1-4 transactions, commit versions 1..4, pending/abort outcomes) and checks
// FindRecoverableCut against brute force for every random target.
func TestEnumerateSmallTxnSets(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		l := NewLog()
		nParts := 2 + rng.Intn(2)
		parts := make([]string, nParts)
		for i := range parts {
			parts[i] = fmt.Sprintf("p%d", i)
			l.AddPartition(parts[i])
		}
		nTxns := 1 + rng.Intn(4)
		for ti := 0; ti < nTxns; ti++ {
			txnID := fmt.Sprintf("t%d", ti)
			// random participant subset, size 1..nParts
			perm := rng.Perm(nParts)
			size := 1 + rng.Intn(nParts)
			var ps []string
			for _, idx := range perm[:size] {
				ps = append(ps, parts[idx])
			}
			l.AddTxn(txnID, ps...)
			for _, p := range ps {
				if rng.Intn(2) == 0 {
					l.AddEntry(p, Entry{Kind: EntryData, TxnID: txnID, Key: "k", Version: Version(1 + rng.Intn(3))})
				}
				switch rng.Intn(3) {
				case 0: // committed
					l.AddEntry(p, Entry{Kind: EntryCommit, TxnID: txnID, Version: Version(1 + rng.Intn(4))})
				case 1: // aborted
					l.AddEntry(p, Entry{Kind: EntryAbort, TxnID: txnID, Version: Version(1 + rng.Intn(4))})
				default: // pending: no marker
				}
			}
		}
		l.Sort()
		if err := l.Validate(); err != nil {
			t.Fatalf("trial %d: invalid generated log: %v", trial, err)
		}
		for tgt := 0; tgt < 5; tgt++ {
			target := Cut{}
			for _, p := range parts {
				target[p] = Version(rng.Intn(5))
			}
			assertGreatest(t, l, target)
		}
	}
}

func TestLateCommitMarker(t *testing.T) {
	l := NewLog()
	l.AddPartition("us")
	l.AddPartition("eu")
	l.AddTxn("t1", "us", "eu")
	l.AddEntry("us", Entry{Kind: EntryData, TxnID: "t1", Key: "a", Version: 3})
	l.AddEntry("us", Entry{Kind: EntryCommit, TxnID: "t1", Version: 5})
	l.AddEntry("eu", Entry{Kind: EntryData, TxnID: "t1", Key: "a", Version: 4})
	l.AddEntry("eu", Entry{Kind: EntryCommit, TxnID: "t1", Version: 12})
	l.Sort()

	target := Cut{"us": 10, "eu": 10}
	vs := l.ValidateCut(target)
	if len(vs) != 1 || vs[0].TxnID != "t1" {
		t.Fatalf("expected one violation for t1, got %+v", vs)
	}

	cut, lowerings := l.FindRecoverableCut(target)
	if cut["us"] != 4 || cut["eu"] != 10 {
		t.Fatalf("got cut %v, want us<=4 eu<=10", cut)
	}
	if len(lowerings) != 1 || lowerings[0].TxnID != "t1" {
		t.Fatalf("expected one lowering for t1, got %+v", lowerings)
	}
	miss := lowerings[0].Missing
	if len(miss) != 1 || miss[0].Partition != "eu" || miss[0].Outcome != OutcomeCommitted {
		t.Fatalf("missing-participant evidence wrong: %+v", miss)
	}
}

func TestPendingDataReceived(t *testing.T) {
	l := NewLog()
	l.AddPartition("us")
	l.AddPartition("asia")
	l.AddTxn("t2", "us", "asia")
	l.AddEntry("us", Entry{Kind: EntryData, TxnID: "t2", Key: "b", Version: 6})
	l.AddEntry("us", Entry{Kind: EntryCommit, TxnID: "t2", Version: 7})
	l.AddEntry("asia", Entry{Kind: EntryData, TxnID: "t2", Key: "b", Version: 6})
	l.Sort()

	cut, lowerings := l.FindRecoverableCut(Cut{"us": 10, "asia": 10})
	if cut["us"] != 6 {
		t.Fatalf("got us<=v%d, want v6 (just below commit v7)", cut["us"])
	}
	if len(lowerings) != 1 {
		t.Fatalf("expected one lowering, got %+v", lowerings)
	}
	miss := lowerings[0].Missing
	if len(miss) != 1 || miss[0].Partition != "asia" || miss[0].Outcome != OutcomePending {
		t.Fatalf("expected pending asia participant as evidence, got %+v", miss)
	}
	if len(miss[0].DataVersions) != 1 || miss[0].DataVersions[0] != 6 {
		t.Fatalf("evidence should list received data version v6, got %+v", miss[0].DataVersions)
	}
}

func TestAbortedParticipant(t *testing.T) {
	l := NewLog()
	l.AddPartition("a")
	l.AddPartition("b")
	l.AddTxn("t1", "a", "b")
	l.AddEntry("a", Entry{Kind: EntryCommit, TxnID: "t1", Version: 5})
	l.AddEntry("b", Entry{Kind: EntryAbort, TxnID: "t1", Version: 6})
	l.Sort()

	cut, lowerings := l.FindRecoverableCut(Cut{"a": 10, "b": 10})
	if cut["a"] != 4 || cut["b"] != 10 {
		t.Fatalf("got %v, want a<=4 b<=10", cut)
	}
	if len(lowerings) != 1 || lowerings[0].Missing[0].Outcome != OutcomeAborted {
		t.Fatalf("expected aborted participant evidence, got %+v", lowerings)
	}
}

// TestCascade: t1 is fully included at the target cut, but lowering the
// frontier for t2 (whose b-side commit is far in the future) excludes t1 at
// a, so a second fixpoint round must exclude t1 at b as well.
func TestCascade(t *testing.T) {
	l := NewLog()
	l.AddPartition("a")
	l.AddPartition("b")
	l.AddTxn("t1", "a", "b")
	l.AddTxn("t2", "a", "b")
	l.AddEntry("a", Entry{Kind: EntryCommit, TxnID: "t1", Version: 5})
	l.AddEntry("b", Entry{Kind: EntryCommit, TxnID: "t1", Version: 9})
	l.AddEntry("a", Entry{Kind: EntryCommit, TxnID: "t2", Version: 3})
	l.AddEntry("b", Entry{Kind: EntryCommit, TxnID: "t2", Version: 20})
	l.Sort()

	target := Cut{"a": 10, "b": 10}
	cut, lowerings := l.FindRecoverableCut(target)
	want := Cut{"a": 2, "b": 8}
	if cut["a"] != want["a"] || cut["b"] != want["b"] {
		t.Fatalf("got %v, want %v", cut, want)
	}
	if len(lowerings) != 2 {
		t.Fatalf("expected two cascading lowerings (t2 then t1), got %+v", lowerings)
	}
	assertGreatest(t, l, target)
}

func TestSinglePartitionTxnIgnored(t *testing.T) {
	l := NewLog()
	l.AddPartition("a")
	l.AddPartition("b")
	l.AddTxn("t1", "a")
	l.AddEntry("a", Entry{Kind: EntryCommit, TxnID: "t1", Version: 5})
	l.Sort()

	cut, lowerings := l.FindRecoverableCut(Cut{"a": 10, "b": 10})
	if cut["a"] != 10 || cut["b"] != 10 || len(lowerings) != 0 {
		t.Fatalf("single-partition txn must not constrain the cut: cut=%v lowerings=%+v", cut, lowerings)
	}
}

func TestFullyIncludedTxnKept(t *testing.T) {
	l := NewLog()
	l.AddPartition("a")
	l.AddPartition("b")
	l.AddTxn("t1", "a", "b")
	l.AddEntry("a", Entry{Kind: EntryCommit, TxnID: "t1", Version: 5})
	l.AddEntry("b", Entry{Kind: EntryCommit, TxnID: "t1", Version: 7})
	l.Sort()

	cut, lowerings := l.FindRecoverableCut(Cut{"a": 10, "b": 10})
	if cut["a"] != 10 || cut["b"] != 10 || len(lowerings) != 0 {
		t.Fatalf("fully included txn must keep target cut: cut=%v lowerings=%+v", cut, lowerings)
	}
}

func TestValidateRejectsBadLogs(t *testing.T) {
	l := NewLog()
	l.AddPartition("a")
	l.AddTxn("t1", "a")
	l.AddEntry("a", Entry{Kind: EntryCommit, TxnID: "ghost", Version: 1})
	if err := l.Validate(); err == nil {
		t.Fatal("expected error for entry of unknown txn")
	}

	l2 := NewLog()
	l2.AddPartition("a")
	l2.AddPartition("b")
	l2.AddTxn("t1", "a")
	l2.AddEntry("b", Entry{Kind: EntryCommit, TxnID: "t1", Version: 1})
	if err := l2.Validate(); err == nil {
		t.Fatal("expected error for entry in non-participant partition")
	}
}
