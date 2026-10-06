package fragmentagg

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

func makeFrags(n int) [][]byte {
	frags := make([][]byte, n)
	for i := range frags {
		frags[i] = []byte(fmt.Sprintf("frag-%03d|", i))
	}
	return frags
}

func wantAssembly(frags [][]byte) []byte {
	var out []byte
	for _, f := range frags {
		out = append(out, f...)
	}
	return out
}

// TestRandomPermutations feeds fragments plus the End declaration in many
// random orders and requires the assembled output to appear exactly once,
// always identical, with full cleanup afterwards.
func TestRandomPermutations(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		n := 1 + rng.Intn(12)
		frags := makeFrags(n)
		key := fmt.Sprintf("key-%d", trial)
		a := New(-1)

		type op struct {
			isEnd bool
			seq   uint64
		}
		ops := make([]op, 0, n+1)
		for i := 0; i < n; i++ {
			ops = append(ops, op{seq: uint64(i)})
		}
		ops = append(ops, op{isEnd: true})
		rng.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })

		completions := 0
		for _, o := range ops {
			var out []byte
			var done bool
			var err error
			if o.isEnd {
				out, done, err = a.End(key, uint64(n-1))
			} else {
				out, done, err = a.Add(key, o.seq, frags[o.seq])
			}
			if err != nil {
				t.Fatalf("trial %d: unexpected error: %v", trial, err)
			}
			if done {
				completions++
				if !bytes.Equal(out, wantAssembly(frags)) {
					t.Fatalf("trial %d: wrong assembly %q", trial, out)
				}
			}
		}
		if completions != 1 {
			t.Fatalf("trial %d: completed %d times, want 1", trial, completions)
		}
		if a.PendingKeys() != 0 || a.StagedBytes() != 0 {
			t.Fatalf("trial %d: state not cleaned: keys=%d bytes=%d",
				trial, a.PendingKeys(), a.StagedBytes())
		}
	}
}

// TestGapBlocksOutput drops one random fragment per trial: the stream must
// never complete, and staged bytes must equal the delivered payload total.
func TestGapBlocksOutput(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 100; trial++ {
		n := 2 + rng.Intn(10)
		missing := rng.Intn(n)
		frags := makeFrags(n)
		a := New(-1)
		key := "gap"

		if _, done, err := a.End(key, uint64(n-1)); err != nil || done {
			t.Fatalf("trial %d: End completed early: done=%v err=%v", trial, done, err)
		}
		staged := 0
		perm := rng.Perm(n)
		for _, i := range perm {
			if i == missing {
				continue
			}
			_, done, err := a.Add(key, uint64(i), frags[i])
			if err != nil {
				t.Fatalf("trial %d: unexpected error: %v", trial, err)
			}
			if done {
				t.Fatalf("trial %d: completed despite missing fragment %d", trial, missing)
			}
			staged += len(frags[i])
		}
		if a.StagedBytes() != staged {
			t.Fatalf("trial %d: staged=%d want %d", trial, a.StagedBytes(), staged)
		}
		if a.PendingKeys() != 1 {
			t.Fatalf("trial %d: pending keys=%d want 1", trial, a.PendingKeys())
		}
		// Delivering the missing fragment completes the stream.
		out, done, err := a.Add(key, uint64(missing), frags[missing])
		if err != nil || !done {
			t.Fatalf("trial %d: final add: done=%v err=%v", trial, done, err)
		}
		if !bytes.Equal(out, wantAssembly(frags)) {
			t.Fatalf("trial %d: wrong assembly", trial)
		}
		if a.PendingKeys() != 0 || a.StagedBytes() != 0 {
			t.Fatalf("trial %d: not cleaned up", trial)
		}
	}
}

// TestEndConflicts exercises contradictory End declarations and fragments
// exceeding the declared maximum, in random arrival orders.
func TestEndConflicts(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 100; trial++ {
		n := 3 + rng.Intn(7)
		frags := makeFrags(n)
		key := "k"
		a := New(-1)

		// Stage a random proper prefix of at least two fragments.
		prefix := 2 + rng.Intn(n-2)
		for i := 0; i < prefix; i++ {
			if _, _, err := a.Add(key, uint64(i), frags[i]); err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
		}

		// End with a maximum below the highest staged fragment conflicts.
		if _, _, err := a.End(key, uint64(prefix-2)); err == nil {
			t.Fatalf("trial %d: low End accepted", trial)
		} else if _, ok := err.(*ConflictError); !ok {
			t.Fatalf("trial %d: want ConflictError, got %T", trial, err)
		}

		// The correct End is accepted; a different redeclaration conflicts.
		if _, _, err := a.End(key, uint64(n-1)); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if _, _, err := a.End(key, uint64(n)); err == nil {
			t.Fatalf("trial %d: redeclared End accepted", trial)
		}
		// Same maximum repeated is idempotent.
		if _, done, err := a.End(key, uint64(n-1)); err != nil || done {
			t.Fatalf("trial %d: idempotent End: done=%v err=%v", trial, done, err)
		}

		// A fragment beyond the declared maximum conflicts.
		if _, _, err := a.Add(key, uint64(n), []byte("overflow")); err == nil {
			t.Fatalf("trial %d: overflow fragment accepted", trial)
		} else if _, ok := err.(*ConflictError); !ok {
			t.Fatalf("trial %d: want ConflictError, got %T", trial, err)
		}

		// Stream still completes normally afterwards.
		var out []byte
		done := false
		for i := prefix; i < n; i++ {
			out, done, _ = a.Add(key, uint64(i), frags[i])
		}
		if !done || !bytes.Equal(out, wantAssembly(frags)) {
			t.Fatalf("trial %d: completion after conflicts failed", trial)
		}
		if a.PendingKeys() != 0 || a.StagedBytes() != 0 {
			t.Fatalf("trial %d: not cleaned up", trial)
		}
	}
}

// TestDuplicates: identical re-adds are accepted (and do not double-count
// bytes); differing payloads for a staged sequence number conflict.
func TestDuplicates(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for trial := 0; trial < 100; trial++ {
		n := 1 + rng.Intn(8)
		frags := makeFrags(n)
		a := New(-1)
		key := "dup"

		staged := 0
		for _, i := range rng.Perm(n) {
			if _, _, err := a.Add(key, uint64(i), frags[i]); err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
			staged += len(frags[i])
			// Identical duplicate: accepted, no extra bytes, no completion
			// (End not declared yet).
			if _, done, err := a.Add(key, uint64(i), frags[i]); err != nil || done {
				t.Fatalf("trial %d: identical dup: done=%v err=%v", trial, done, err)
			}
			if a.StagedBytes() != staged {
				t.Fatalf("trial %d: duplicate double-counted bytes", trial)
			}
			// Differing duplicate: conflict.
			bad := append([]byte(nil), frags[i]...)
			bad[0] ^= 0xff
			if _, _, err := a.Add(key, uint64(i), bad); err == nil {
				t.Fatalf("trial %d: conflicting dup accepted", trial)
			} else if _, ok := err.(*ConflictError); !ok {
				t.Fatalf("trial %d: want ConflictError, got %T", trial, err)
			}
		}
		out, done, err := a.End(key, uint64(n-1))
		if err != nil || !done {
			t.Fatalf("trial %d: End: done=%v err=%v", trial, done, err)
		}
		if !bytes.Equal(out, wantAssembly(frags)) {
			t.Fatalf("trial %d: wrong assembly", trial)
		}
		if a.PendingKeys() != 0 || a.StagedBytes() != 0 {
			t.Fatalf("trial %d: not cleaned up", trial)
		}
	}
}

// TestByteLimit: the quota counts staged payload bytes; over-quota adds
// are rejected without staging, and completion releases the budget.
func TestByteLimit(t *testing.T) {
	frags := makeFrags(4) // 9 bytes each, 36 total
	total := 0
	for _, f := range frags {
		total += len(f)
	}

	a := New(total - 1)
	if _, _, err := a.Add("k", 0, frags[0]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Add("k", 1, frags[1]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Add("k", 2, frags[2]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Add("k", 3, frags[3]); err != ErrLimitExceeded {
		t.Fatalf("want ErrLimitExceeded, got %v", err)
	}
	if a.StagedBytes() != total-len(frags[3]) {
		t.Fatalf("rejected add changed staged bytes: %d", a.StagedBytes())
	}

	// A second key shares the same budget.
	if _, _, err := a.Add("other", 0, []byte("0123456789")); err != ErrLimitExceeded {
		t.Fatalf("shared budget: want ErrLimitExceeded, got %v", err)
	}

	// Identical duplicates are free even at the limit.
	if _, _, err := a.Add("k", 0, frags[0]); err != nil {
		t.Fatalf("duplicate at limit: %v", err)
	}

	// Raising the budget lets the stream complete and release its bytes.
	a2 := New(total)
	for i, f := range frags {
		if _, _, err := a2.Add("k", uint64(i), f); err != nil {
			t.Fatal(err)
		}
	}
	out, done, err := a2.End("k", 3)
	if err != nil || !done {
		t.Fatalf("End: done=%v err=%v", done, err)
	}
	if !bytes.Equal(out, wantAssembly(frags)) {
		t.Fatal("wrong assembly")
	}
	if a2.StagedBytes() != 0 || a2.PendingKeys() != 0 {
		t.Fatal("budget not released after completion")
	}
}

// TestEndFirstZeroLength: End arriving before everything, including the
// degenerate max=0 stream.
func TestEndFirstZeroLength(t *testing.T) {
	a := New(-1)
	if _, done, err := a.End("empty", 0); err != nil || done {
		t.Fatalf("End alone: done=%v err=%v", done, err)
	}
	out, done, err := a.Add("empty", 0, []byte("solo"))
	if err != nil || !done {
		t.Fatalf("Add: done=%v err=%v", done, err)
	}
	if string(out) != "solo" {
		t.Fatalf("out=%q", out)
	}
	if a.PendingKeys() != 0 || a.StagedBytes() != 0 {
		t.Fatal("not cleaned up")
	}
}
