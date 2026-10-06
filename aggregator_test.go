package fragagg

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// op is one delivery event in a shuffled schedule: either a fragment or
// the end declaration.
type op struct {
	finish  bool
	seq     uint64
	payload []byte
	maxSeq  uint64
}

func makeFrags(r *rand.Rand, n int) [][]byte {
	frags := make([][]byte, n)
	for i := range frags {
		p := make([]byte, 1+r.Intn(16))
		r.Read(p)
		frags[i] = p
	}
	return frags
}

func want(frags [][]byte) []byte {
	return bytes.Join(frags, nil)
}

// schedule interleaves all fragment deliveries and the Finish call in a
// uniformly random order.
func schedule(r *rand.Rand, frags [][]byte) []op {
	ops := make([]op, 0, len(frags)+1)
	for i, p := range frags {
		ops = append(ops, op{seq: uint64(i), payload: p})
	}
	ops = append(ops, op{finish: true, maxSeq: uint64(len(frags) - 1)})
	r.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })
	return ops
}

func run(t *testing.T, a *Aggregator, key string, ops []op) (out []byte, done bool) {
	t.Helper()
	for _, o := range ops {
		var err error
		if o.finish {
			out, done, err = a.Finish(key, o.maxSeq)
		} else {
			out, done, err = a.Add(key, o.seq, o.payload)
		}
		if err != nil {
			t.Fatalf("key %q op %+v: unexpected error %v", key, o, err)
		}
		if done {
			return out, true
		}
	}
	return nil, false
}

// TestRandomPermutations feeds complete streams in random orders, with the
// end declaration landing anywhere, and expects exactly one emission with
// the concatenated payload and full cleanup afterwards.
func TestRandomPermutations(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 500; trial++ {
		n := 1 + r.Intn(12)
		frags := makeFrags(r, n)
		a := New(-1)
		key := fmt.Sprintf("k%d", trial)
		out, done := run(t, a, key, schedule(r, frags))
		if !done {
			t.Fatalf("trial %d: complete stream never emitted", trial)
		}
		if !bytes.Equal(out, want(frags)) {
			t.Fatalf("trial %d: assembled payload mismatch", trial)
		}
		if got := a.StagedBytes(); got != 0 {
			t.Fatalf("trial %d: %d bytes still staged after emission", trial, got)
		}
	}
}

// TestGapNeverEmits drops one random fragment per trial; no schedule may
// produce output, and Reset must reclaim every staged byte.
func TestGapNeverEmits(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for trial := 0; trial < 300; trial++ {
		n := 2 + r.Intn(10)
		frags := makeFrags(r, n)
		missing := r.Intn(n)
		present := append([][]byte{}, frags[:missing]...)
		present = append(present, frags[missing+1:]...)
		ops := schedule(r, present)
		// The end still declares the true maximum, so the gap at
		// `missing` must block emission.
		for i := range ops {
			if ops[i].finish {
				ops[i].maxSeq = uint64(n - 1)
			}
		}
		a := New(-1)
		if _, done := run(t, a, "gap", ops); done {
			t.Fatalf("trial %d: emitted despite missing sequence %d", trial, missing)
		}
		if got := a.StagedBytes(); got == 0 {
			t.Fatalf("trial %d: expected staged bytes for incomplete stream", trial)
		}
		a.Reset("gap")
		if got := a.StagedBytes(); got != 0 {
			t.Fatalf("trial %d: Reset left %d staged bytes", trial, got)
		}
	}
}

// TestEndConflict declares two different maxima in random order and
// expects a ConflictError; re-declaring the same maximum is accepted.
func TestEndConflict(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for trial := 0; trial < 200; trial++ {
		lo := uint64(r.Intn(20))
		hi := lo + 1 + uint64(r.Intn(20))
		first, second := lo, hi
		if r.Intn(2) == 0 {
			first, second = second, first
		}
		a := New(-1)
		if _, _, err := a.Finish("k", first); err != nil {
			t.Fatalf("trial %d: first Finish: %v", trial, err)
		}
		if _, _, err := a.Finish("k", first); err != nil {
			t.Fatalf("trial %d: identical re-declare rejected: %v", trial, err)
		}
		_, _, err := a.Finish("k", second)
		if _, ok := err.(*ConflictError); !ok {
			t.Fatalf("trial %d: conflicting maxima: got %v, want *ConflictError", trial, err)
		}
	}
}

// TestSeqBeyondMax stages fragments past the declared maximum, both before
// and after Finish, and expects conflicts.
func TestSeqBeyondMax(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	for trial := 0; trial < 200; trial++ {
		max := uint64(r.Intn(10))
		a := New(-1)
		if r.Intn(2) == 0 {
			if _, _, err := a.Finish("k", max); err != nil {
				t.Fatalf("trial %d: Finish: %v", trial, err)
			}
			if _, _, err := a.Add("k", max+1+uint64(r.Intn(5)), []byte("x")); err == nil {
				t.Fatalf("trial %d: Add beyond maximum accepted", trial)
			} else if _, ok := err.(*ConflictError); !ok {
				t.Fatalf("trial %d: got %v, want *ConflictError", trial, err)
			}
		} else {
			if _, _, err := a.Add("k", max+1+uint64(r.Intn(5)), []byte("x")); err != nil {
				t.Fatalf("trial %d: Add before Finish: %v", trial, err)
			}
			if _, _, err := a.Finish("k", max); err == nil {
				t.Fatalf("trial %d: Finish below staged sequence accepted", trial)
			} else if _, ok := err.(*ConflictError); !ok {
				t.Fatalf("trial %d: got %v, want *ConflictError", trial, err)
			}
		}
	}
}

// TestDuplicateFragments re-delivers random fragments: identical bytes are
// accepted without double-counting quota, differing bytes conflict.
func TestDuplicateFragments(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for trial := 0; trial < 300; trial++ {
		n := 1 + r.Intn(8)
		frags := makeFrags(r, n)
		a := New(-1)
		staged := 0
		for i, p := range frags {
			if _, _, err := a.Add("k", uint64(i), p); err != nil {
				t.Fatalf("trial %d: Add: %v", trial, err)
			}
			staged += len(p)
		}
		for _, i := range r.Perm(n)[:1+r.Intn(n)] {
			if _, _, err := a.Add("k", uint64(i), frags[i]); err != nil {
				t.Fatalf("trial %d: identical duplicate rejected: %v", trial, err)
			}
		}
		if got := a.StagedBytes(); got != staged {
			t.Fatalf("trial %d: duplicates changed staged bytes to %d, want %d", trial, got, staged)
		}
		bad := r.Intn(n)
		mutant := append([]byte{}, frags[bad]...)
		mutant[0] ^= 0xff
		if _, _, err := a.Add("k", uint64(bad), mutant); err == nil {
			t.Fatalf("trial %d: divergent duplicate accepted", trial)
		} else if _, ok := err.(*ConflictError); !ok {
			t.Fatalf("trial %d: got %v, want *ConflictError", trial, err)
		}
	}
}

// TestQuota rejects the fragment that would exceed the staged-byte quota
// without corrupting state, then completes once quota is freed.
func TestQuota(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	for trial := 0; trial < 200; trial++ {
		n := 2 + r.Intn(8)
		frags := makeFrags(r, n)
		total := 0
		for _, p := range frags {
			total += len(p)
		}
		quota := r.Intn(total) // strictly below the required total
		a := New(quota)
		rejected := false
		emitted := false
		for _, o := range schedule(r, frags) {
			var err error
			var done bool
			if o.finish {
				_, done, err = a.Finish("k", o.maxSeq)
			} else {
				_, done, err = a.Add("k", o.seq, o.payload)
			}
			if err == ErrQuotaExceeded {
				rejected = true
				continue
			}
			if err != nil {
				t.Fatalf("trial %d: unexpected error %v", trial, err)
			}
			emitted = emitted || done
		}
		if !rejected {
			t.Fatalf("trial %d: quota %d never enforced (total %d)", trial, quota, total)
		}
		if emitted {
			t.Fatalf("trial %d: emitted despite rejected fragment", trial)
		}
		if got := a.StagedBytes(); got > quota {
			t.Fatalf("trial %d: staged %d bytes over quota %d", trial, got, quota)
		}
	}
}

// TestKeyReuseAfterEmit completes a key, then reuses it for an unrelated
// stream to confirm emission fully cleaned up its state.
func TestKeyReuseAfterEmit(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	a := New(-1)
	for round := 0; round < 100; round++ {
		n := 1 + r.Intn(6)
		frags := makeFrags(r, n)
		out, done := run(t, a, "reuse", schedule(r, frags))
		if !done || !bytes.Equal(out, want(frags)) {
			t.Fatalf("round %d: reuse of key failed (done=%v)", round, done)
		}
	}
	if got := a.StagedBytes(); got != 0 {
		t.Fatalf("staged bytes leaked across rounds: %d", got)
	}
}

// TestManyKeysInterleaved runs independent streams concurrently under one
// aggregator and checks per-key isolation.
func TestManyKeysInterleaved(t *testing.T) {
	r := rand.New(rand.NewSource(8))
	const keys = 6
	frags := make(map[string][][]byte, keys)
	var all []opKey
	for k := 0; k < keys; k++ {
		key := fmt.Sprintf("key-%d", k)
		n := 1 + r.Intn(8)
		frags[key] = makeFrags(r, n)
		for _, o := range schedule(r, frags[key]) {
			all = append(all, opKey{key: key, op: o})
		}
	}
	r.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	a := New(-1)
	doneCount := 0
	for _, ok := range all {
		var out []byte
		var done bool
		var err error
		if ok.op.finish {
			out, done, err = a.Finish(ok.key, ok.op.maxSeq)
		} else {
			out, done, err = a.Add(ok.key, ok.op.seq, ok.op.payload)
		}
		if err != nil {
			t.Fatalf("key %q: unexpected error %v", ok.key, err)
		}
		if done {
			doneCount++
			if !bytes.Equal(out, want(frags[ok.key])) {
				t.Fatalf("key %q: assembled payload mismatch", ok.key)
			}
		}
	}
	if doneCount != keys {
		t.Fatalf("emitted %d streams, want %d", doneCount, keys)
	}
	if got := a.StagedBytes(); got != 0 {
		t.Fatalf("%d staged bytes remain", got)
	}
}

type opKey struct {
	key string
	op  op
}
