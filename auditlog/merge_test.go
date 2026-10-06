package auditlog

import "testing"

func prefixSeqs(t *testing.T, r MergeResult) []uint64 {
	t.Helper()
	var out []uint64
	for _, e := range r.TrustedPrefix {
		out = append(out, e.Seq)
	}
	return out
}

func expectPrefix(t *testing.T, r MergeResult, wantLast uint64, wantReason StopReason) {
	t.Helper()
	got := prefixSeqs(t, r)
	if uint64(len(got)) != wantLast {
		t.Fatalf("prefix length = %d (%v), want %d", len(got), got, wantLast)
	}
	for i, s := range got {
		if s != uint64(i+1) {
			t.Fatalf("prefix not contiguous from genesis: %v", got)
		}
	}
	if r.StopReason != wantReason {
		t.Fatalf("stop reason = %q, want %q", r.StopReason, wantReason)
	}
}

// Deletion: replicas each miss different segments; the union must still
// reconstruct the full chain.
func TestMergeDeletedSegments(t *testing.T) {
	full := MakeChain(10, "op")
	a := append([]Entry{}, full[0:3]...) // 1-3
	a = append(a, full[6:]...)           // + 7-10, missing 4-6
	b := append([]Entry{}, full[3:8]...) // 4-8
	c := append([]Entry{}, full[8:]...)  // 9-10 (redundant)

	r := Merge([]Source{
		{Name: "a", Entries: a},
		{Name: "b", Entries: b},
		{Name: "c", Entries: c},
	})
	expectPrefix(t, r, 10, StopExhausted)
	if len(r.Forks) != 0 || len(r.Tampered) != 0 {
		t.Fatalf("unexpected evidence: %+v", r)
	}
}

// Deletion in every replica at the same spot: the prefix must stop at the
// gap instead of skipping ahead to higher sequence numbers.
func TestMergeGapInAllReplicas(t *testing.T) {
	full := MakeChain(10, "op")
	drop := func() []Entry {
		out := append([]Entry{}, full[0:4]...) // 1-4
		return append(out, full[6:]...)        // 7-10, 5-6 gone everywhere
	}
	r := Merge([]Source{
		{Name: "a", Entries: drop()},
		{Name: "b", Entries: drop()},
	})
	expectPrefix(t, r, 4, StopGap)
}

// Duplication: duplicated entries inside and across replicas must collapse
// and never break or inflate the chain.
func TestMergeDuplicates(t *testing.T) {
	full := MakeChain(6, "op")
	dup := []Entry{}
	for _, e := range full {
		dup = append(dup, e, e, e) // triple every entry
	}
	// shuffle-free but interleaved second copy
	r := Merge([]Source{
		{Name: "a", Entries: dup},
		{Name: "b", Entries: append(append([]Entry{}, full...), full...)},
	})
	expectPrefix(t, r, 6, StopExhausted)
	if len(r.Forks) != 0 {
		t.Fatalf("duplicates must not be reported as forks: %+v", r.Forks)
	}
}

// Tampering: a modified payload invalidates the entry's hash. The tampered
// copy is excluded and reported; intact replicas still cover the chain.
func TestMergeTamperedEntry(t *testing.T) {
	full := MakeChain(8, "op")
	bad := append([]Entry{}, full...)
	bad[4].Payload = "forged" // seq 5, hash now stale

	r := Merge([]Source{
		{Name: "honest", Entries: full},
		{Name: "attacker", Entries: bad},
	})
	expectPrefix(t, r, 8, StopExhausted)
	if len(r.Tampered) != 1 || r.Tampered[0].Source != "attacker" || r.Tampered[0].Entry.Seq != 5 {
		t.Fatalf("tamper evidence missing or wrong: %+v", r.Tampered)
	}
}

// Tampering with a recomputed hash: the attacker re-hashes the forged entry
// so it is self-consistent but no longer chains to the true predecessor.
// With no honest copy the prefix must stop before the forgery.
func TestMergeRehashedForgery(t *testing.T) {
	full := MakeChain(8, "op")
	forged := append([]Entry{}, full[:4]...)
	forged = append(forged, Append(forged, "forged")) // self-consistent seq 5

	r := Merge([]Source{{Name: "attacker", Entries: forged}})
	expectPrefix(t, r, 5, StopExhausted) // valid chain, just shorter

	// Mixing forged and honest replicas: conflict at seq 5 => fork evidence,
	// prefix stops at 4, no side auto-selected.
	r = Merge([]Source{
		{Name: "honest", Entries: full},
		{Name: "attacker", Entries: forged},
	})
	expectPrefix(t, r, 4, StopFork)
	if len(r.Forks) != 1 || r.Forks[0].Seq != 5 || len(r.Forks[0].Variants) != 2 {
		t.Fatalf("fork evidence wrong: %+v", r.Forks)
	}
}

// Fork: two replicas diverge at seq 4. Both branches must be preserved as
// evidence and the merge must not pick either side — even though one
// branch is longer.
func TestMergeForkKeepsBothSides(t *testing.T) {
	base := MakeChain(3, "op")
	left := append(append([]Entry{}, base...), Append(base, "left-4"))
	left = append(left, Append(left, "left-5"))
	left = append(left, Append(left, "left-6")) // longer branch
	right := append(append([]Entry{}, base...), Append(base, "right-4"))

	r := Merge([]Source{
		{Name: "left", Entries: left},
		{Name: "right", Entries: right},
	})
	expectPrefix(t, r, 3, StopFork)
	if len(r.Forks) != 1 {
		t.Fatalf("want exactly one fork, got %+v", r.Forks)
	}
	f := r.Forks[0]
	if f.Seq != 4 || len(f.Variants) != 2 {
		t.Fatalf("fork evidence incomplete: %+v", f)
	}
	payloads := map[string]bool{}
	for _, v := range f.Variants {
		payloads[v.Entry.Payload] = true
	}
	if !payloads["left-4"] || !payloads["right-4"] {
		t.Fatalf("both branches must be kept as evidence: %+v", f)
	}
	// The longer branch must not leak into the prefix.
	for _, e := range r.TrustedPrefix {
		if e.Seq > 3 {
			t.Fatalf("auto-selected a side: %+v", r.TrustedPrefix)
		}
	}
}

// Max-seq trap: a replica that simply runs to the highest sequence number
// must not win. A fabricated longer chain with a broken link contributes
// nothing beyond the point where it stops agreeing with the verified chain.
func TestMergeIgnoresHighestSeq(t *testing.T) {
	honest := MakeChain(5, "op")

	// Attacker copies the honest chain, then appends entries that skip the
	// link: seq 6 with PrevHash of seq 4 (chains around seq 5's real hash).
	fake := append([]Entry{}, honest[:4]...)
	e6 := Entry{Seq: 6, Payload: "fake-6", PrevHash: honest[3].Hash}
	e6.Hash = ComputeHash(e6.Seq, e6.Payload, e6.PrevHash)
	e7 := Append([]Entry{e6}, "fake-7")
	fake = append(fake, honest[4], e6, e7) // seqs 1-5,6,7 but 6 mislinked

	r := Merge([]Source{
		{Name: "honest", Entries: honest},
		{Name: "long-but-wrong", Entries: fake},
	})
	// Seq 6 has two variants (honest has none at 6... attacker has e6).
	// The honest chain ends at 5; attacker's 6 does not chain to 5's hash,
	// so the prefix stops at 5 with a broken link, not at 7.
	expectPrefix(t, r, 5, StopBrokenLink)
}

// A replica that only carries a higher-numbered segment cannot extend the
// trusted prefix on its own: without genesis the prefix is empty.
func TestMergeNoGenesis(t *testing.T) {
	full := MakeChain(10, "op")
	r := Merge([]Source{{Name: "tail-only", Entries: full[5:]}})
	expectPrefix(t, r, 0, StopNoGenesis)
}

// Empty input must be handled gracefully.
func TestMergeEmpty(t *testing.T) {
	r := Merge(nil)
	expectPrefix(t, r, 0, StopNoGenesis)
}

// Unordered entries within a source must still merge correctly.
func TestMergeUnorderedSource(t *testing.T) {
	full := MakeChain(7, "op")
	rev := make([]Entry, len(full))
	for i, e := range full {
		rev[len(full)-1-i] = e
	}
	r := Merge([]Source{{Name: "reversed", Entries: rev}})
	expectPrefix(t, r, 7, StopExhausted)
}
