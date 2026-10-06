package auditlog

import "testing"

func payloads(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte{byte('a' + i%26), byte(i)}
	}
	return out
}

func mustVerify(t *testing.T, entries []Entry) {
	t.Helper()
	if err := Verify(entries); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyValidChain(t *testing.T) {
	mustVerify(t, BuildChain(payloads(10)...))
}

func TestVerifyRejectsGap(t *testing.T) {
	chain := BuildChain(payloads(5)...)
	broken := append([]Entry{chain[0]}, chain[2:]...)
	if err := Verify(broken); err == nil {
		t.Fatal("expected error for missing entry")
	}
}

func TestMergeIdenticalReplicas(t *testing.T) {
	chain := BuildChain(payloads(8)...)
	res := Merge(chain, chain, chain)
	if len(res.Prefix) != 8 || len(res.Forks) != 0 || len(res.Issues) != 0 {
		t.Fatalf("got prefix=%d forks=%d issues=%d", len(res.Prefix), len(res.Forks), len(res.Issues))
	}
	mustVerify(t, res.Prefix)
}

// Deletion: replicas missing different segments still merge to the
// full chain, because every sequence number is covered and agreed on.
func TestMergeReplicasWithMissingSegments(t *testing.T) {
	chain := BuildChain(payloads(10)...)
	a := append([]Entry{}, chain[:6]...)                       // missing tail
	b := append([]Entry{}, chain[4:]...)                       // missing head
	c := append(append([]Entry{}, chain[:3]...), chain[7:]...) // hole in the middle
	res := Merge(a, b, c)
	if len(res.Prefix) != 10 {
		t.Fatalf("prefix=%d, want 10 (issues=%v)", len(res.Prefix), res.Issues)
	}
	mustVerify(t, res.Prefix)
}

// Deletion from every replica at the same point truncates the prefix:
// nothing past the gap can be anchored to genesis.
func TestMergeStopsAtCommonGap(t *testing.T) {
	chain := BuildChain(payloads(10)...)
	res := Merge(chain[:5], chain[:5])
	if len(res.Prefix) != 5 {
		t.Fatalf("prefix=%d, want 5", len(res.Prefix))
	}
}

// Duplication: repeated or shuffled entries must not change the result.
func TestMergeWithDuplicates(t *testing.T) {
	chain := BuildChain(payloads(6)...)
	dup := []Entry{chain[3], chain[0], chain[3], chain[1], chain[5], chain[2], chain[4], chain[4], chain[0]}
	res := Merge(dup, chain)
	if len(res.Prefix) != 6 || len(res.Forks) != 0 {
		t.Fatalf("prefix=%d forks=%d", len(res.Prefix), len(res.Forks))
	}
	mustVerify(t, res.Prefix)
}

// Tampering: a modified payload no longer matches its hash and is
// rejected; honest replicas still determine the prefix.
func TestMergeRejectsTamperedEntry(t *testing.T) {
	chain := BuildChain(payloads(6)...)
	evil := make([]Entry, len(chain))
	copy(evil, chain)
	evil[2].Data = []byte("forged") // hash left stale on purpose
	res := Merge(chain, evil)
	if len(res.Issues) == 0 {
		t.Fatal("expected tamper issue")
	}
	if len(res.Prefix) != 6 {
		t.Fatalf("prefix=%d, want 6", len(res.Prefix))
	}
	mustVerify(t, res.Prefix)
}

// Fork: two valid but divergent chains. Both sides are kept as
// evidence, the prefix stops at the divergence, and the longer fork
// must not win by height.
func TestMergeForkKeepsBothSides(t *testing.T) {
	honest := BuildChain(payloads(4)...)
	fork := append([]Entry{}, honest[:2]...)
	prev := fork[1].Hash
	for i := 0; i < 6; i++ { // fork is LONGER than the honest chain
		e := NewEntry(uint64(i)+3, prev, []byte("evil"))
		fork = append(fork, e)
		prev = e.Hash
	}
	res := Merge(honest, fork)
	if len(res.Prefix) != 2 {
		t.Fatalf("prefix=%d, want 2", len(res.Prefix))
	}
	if len(res.Forks) == 0 || res.Forks[0].Seq != 3 {
		t.Fatalf("forks=%+v, want first fork at seq 3", res.Forks)
	}
	for _, f := range res.Forks {
		if f.A.Hash == f.B.Hash {
			t.Fatalf("fork at seq %d must contain two distinct sides", f.Seq)
		}
	}
	mustVerify(t, res.Prefix)
}

// A tail that does not anchor to the agreed prefix is ignored, even
// though its entries are internally consistent and reach higher seqs.
func TestMergeIgnoresUnanchoredTail(t *testing.T) {
	chain := BuildChain(payloads(3)...)
	var anchor [32]byte
	anchor[0] = 0xff // not the hash of any honest entry
	tail := []Entry{NewEntry(4, anchor, []byte("x"))}
	tail = append(tail, NewEntry(5, tail[0].Hash, []byte("y")))
	tail = append(tail, NewEntry(6, tail[1].Hash, []byte("z")))
	res := Merge(chain, chain, tail)
	if len(res.Prefix) != 3 {
		t.Fatalf("prefix=%d, want 3", len(res.Prefix))
	}
	if len(res.Forks) != 0 {
		t.Fatalf("no fork expected, got %+v", res.Forks)
	}
}
