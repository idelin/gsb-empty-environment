package main

import (
	"io/ioutil"
	"os"
	"testing"
)

// Two competitors interleaved, plus crash/recovery of both the lease
// manager and the resource. Invariants:
//  1. accepted write fences are strictly increasing;
//  2. every stale-leader write is rejected with StaleFenceError;
//  3. recovery never lowers a fence (no token reuse, no fence rollback);
//  4. final data belongs to the highest-fence writer.
func TestFencingNoStaleOverwrite(t *testing.T) {
	dir, err := ioutil.TempDir("", "leasesim-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	clock := &Clock{}
	lmStore, _ := NewStore(dir, "lm.json")
	resStore, _ := NewStore(dir, "res.json")
	lm := NewLeaseManager(clock, lmStore, 10)
	res := NewResource(clock, resStore)

	mustGrant := func(holder string) *Lease {
		l, err := lm.Grant(holder)
		if err != nil {
			t.Fatalf("grant %s: %v", holder, err)
		}
		return l
	}
	mustWrite := func(l *Lease, v string) {
		if err := res.Write(l, v); err != nil {
			t.Fatalf("write %q by %s: %v", v, l.Holder, err)
		}
	}
	mustReject := func(l *Lease, v string) {
		err := res.Write(l, v)
		if _, ok := err.(*StaleFenceError); !ok {
			t.Fatalf("write %q by %s: want StaleFenceError, got %v", v, l.Holder, err)
		}
	}

	// A leads, stalls, B takes over.
	a1 := mustGrant("A")
	mustWrite(a1, "A:init")
	clock.Advance(5)
	if _, err := lm.Renew("A", a1.Fence); err != nil {
		t.Fatalf("renew A: %v", err)
	}
	clock.Advance(15) // A's lease lapses at t=15
	b1 := mustGrant("B")
	if b1.Fence <= a1.Fence {
		t.Fatalf("fence not increasing: A=%d B=%d", a1.Fence, b1.Fence)
	}
	mustWrite(b1, "B:takeover")
	mustReject(a1, "A:late-overwrite") // stale leader fenced out
	if _, err := lm.Renew("A", a1.Fence); err == nil {
		t.Fatal("expired A renewed its lease")
	}
	mustWrite(b1, "B:work")

	// Crash both sides; recovery must not lower any fence.
	res = NewResource(clock, resStore)
	if err := res.Recover(); err != nil {
		t.Fatal(err)
	}
	if res.LastFence() != b1.Fence {
		t.Fatalf("resource fence regressed: got %d want %d", res.LastFence(), b1.Fence)
	}
	lm = NewLeaseManager(clock, lmStore, 10)
	if err := lm.Recover(); err != nil {
		t.Fatal(err)
	}
	mustWrite(b1, "B:work") // same-epoch retry after restart: idempotent, safe

	// A comes back with a fresh, higher fence; B is now the zombie.
	clock.Advance(11)
	a2 := mustGrant("A")
	if a2.Fence <= b1.Fence {
		t.Fatalf("fence reused after recovery: %d", a2.Fence)
	}
	mustWrite(a2, "A:back")
	mustReject(b1, "B:zombie")

	// Invariant 1: accepted history is non-decreasing in fence, and an
	// equal fence only ever comes from the same writer (same epoch).
	hist := res.History()
	for i := 1; i < len(hist); i++ {
		if hist[i].Fence < hist[i-1].Fence {
			t.Fatalf("history fence not increasing: %+v", hist)
		}
		if hist[i].Fence == hist[i-1].Fence && hist[i].Writer != hist[i-1].Writer {
			t.Fatalf("same fence from two writers: %+v", hist)
		}
	}
	// Invariant 4: final data is the highest-fence write.
	if got := res.Data(); got != "A:back" {
		t.Fatalf("final data = %q, want %q (stale leader overwrote!)", got, "A:back")
	}
	if len(hist) != 5 {
		t.Fatalf("accepted writes = %d, want 5: %+v", len(hist), hist)
	}
}

// Recovery can only keep or raise the fence counter, never lower it,
// even if the durable state were somehow older.
func TestRecoverNeverLowersFence(t *testing.T) {
	dir, err := ioutil.TempDir("", "leasesim-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	clock := &Clock{}
	lmStore, _ := NewStore(dir, "lm.json")
	lm := NewLeaseManager(clock, lmStore, 10)
	if _, err := lm.Grant("A"); err != nil {
		t.Fatal(err)
	}
	if err := lm.Recover(); err != nil {
		t.Fatal(err)
	}
	clock.Advance(11)
	l, err := lm.Grant("B")
	if err != nil {
		t.Fatal(err)
	}
	if l.Fence != 2 {
		t.Fatalf("fence after recovery = %d, want 2 (no reuse)", l.Fence)
	}
}
