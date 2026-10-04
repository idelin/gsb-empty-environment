package blockmatch

import (
	"bytes"
	"math/rand"
	"reflect"
	"testing"
)

func randomBytes(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	r.Read(b)
	return b
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// checkOps verifies that ops cover the new stream exactly (in order, no
// gaps, no overlaps) and reconstruct it from the old data.
func checkOps(t *testing.T, old, newData []byte, ops []Op) {
	t.Helper()
	pos := 0
	var out []byte
	for i, op := range ops {
		if op.Length <= 0 {
			t.Fatalf("op %d: non-positive length %d", i, op.Length)
		}
		switch op.Kind {
		case Literal:
			if op.Offset != pos {
				t.Fatalf("op %d: literal offset %d, want %d (gap or overlap)", i, op.Offset, pos)
			}
			if len(op.Data) != op.Length {
				t.Fatalf("op %d: literal length %d but %d data bytes", i, op.Length, len(op.Data))
			}
			out = append(out, op.Data...)
		case Match:
			if op.Offset < 0 || op.Offset+op.Length > len(old) {
				t.Fatalf("op %d: match [%d:%d] out of old-data bounds %d", i, op.Offset, op.Offset+op.Length, len(old))
			}
			out = append(out, old[op.Offset:op.Offset+op.Length]...)
		default:
			t.Fatalf("op %d: unknown kind %d", i, op.Kind)
		}
		pos += op.Length
	}
	if pos != len(newData) {
		t.Fatalf("ops cover %d bytes, new stream has %d", pos, len(newData))
	}
	if !bytes.Equal(out, newData) {
		t.Fatal("reconstructed stream differs from new data")
	}
}

func countKinds(ops []Op) (literals, matches int) {
	for _, op := range ops {
		if op.Kind == Literal {
			literals++
		} else {
			matches++
		}
	}
	return
}

// matchCovered returns true if any Match op intersects [start, end) of the
// new stream.
func matchCovered(ops []Op, start, end int) bool {
	pos := 0
	for _, op := range ops {
		next := pos + op.Length
		if op.Kind == Match && pos < end && next > start {
			return true
		}
		pos = next
	}
	return false
}

// findCollidingBlock returns a block with the same truncated weak key as
// target but different content, proving collisions only yield candidates.
func findCollidingBlock(t *testing.T, opts Options, target []byte, forbidden [][]byte) []byte {
	t.Helper()
	want := opts.weakKey(Fingerprint(target))
	cand := make([]byte, len(target))
	for i := 1; ; i++ {
		for j := range cand {
			cand[j] = byte(i >> (uint(j%4) * 8))
		}
		if opts.weakKey(Fingerprint(cand)) != want || bytes.Equal(cand, target) {
			continue
		}
		clash := false
		for _, f := range forbidden {
			if bytes.Equal(cand, f) {
				clash = true
				break
			}
		}
		if !clash {
			return cand
		}
	}
}

// TestCollidingFingerprintRepeatedBlocks uses a deliberately tiny weak key
// space so fingerprint collisions are constant. Repeated blocks must still
// match, and a block that collides weakly but differs in content must NOT
// be reused as a match.
func TestCollidingFingerprintRepeatedBlocks(t *testing.T) {
	opts := Options{BlockSize: 16, WeakBits: 4}

	// Old data: a few distinct blocks, each repeated several times.
	blkA := randomBytes(opts.BlockSize, 1)
	blkB := randomBytes(opts.BlockSize, 2)
	blkC := randomBytes(opts.BlockSize, 3)
	old := concat(blkA, blkB, blkC, blkA, blkB, blkC, blkA, blkB)
	idx := BuildIndex(old, opts)

	// A block that weakly collides with blkA but has different content.
	evil := findCollidingBlock(t, opts, blkA, [][]byte{blkA, blkB, blkC})

	newData := concat(blkA, blkB, evil, blkC, blkA, blkB)

	m := NewMatcher(idx)
	m.Write(newData)
	ops := m.Finish()

	checkOps(t, old, newData, ops)
	lits, mats := countKinds(ops)
	if mats == 0 {
		t.Fatal("expected repeated blocks to match")
	}
	if lits == 0 {
		t.Fatal("expected the colliding impostor block to stay literal")
	}
	// The impostor occupies [32, 48) of the new stream; no match may cover it.
	if matchCovered(ops, 2*opts.BlockSize, 3*opts.BlockSize) {
		t.Fatal("weak-collision candidate was reused without content verification")
	}
}

// TestTailShorterThanWindow checks that a trailing partial window is
// emitted as literal bytes and coverage stays exact.
func TestTailShorterThanWindow(t *testing.T) {
	opts := Options{BlockSize: 32, WeakBits: 8}
	old := randomBytes(3*opts.BlockSize, 7)
	tail := randomBytes(opts.BlockSize-1, 8) // one byte short of a full window
	newData := concat(old, tail)

	m := NewMatcher(BuildIndex(old, opts))
	m.Write(newData)
	ops := m.Finish()

	checkOps(t, old, newData, ops)
	last := ops[len(ops)-1]
	if last.Kind != Literal || last.Length != len(tail) {
		t.Fatalf("expected final literal of %d bytes, got %+v", len(tail), last)
	}
	if !bytes.Equal(last.Data, tail) {
		t.Fatal("tail literal content mismatch")
	}
	lits, mats := countKinds(ops)
	if lits != 1 || mats != 3 {
		t.Fatalf("expected 3 matches + 1 literal, got %d matches + %d literals", mats, lits)
	}
}

// TestAllZeroBytes exercises degenerate input where every window has the
// same fingerprint (all zeros), producing maximal weak collisions.
func TestAllZeroBytes(t *testing.T) {
	opts := Options{BlockSize: 16, WeakBits: 4}
	old := make([]byte, 4096)
	newData := make([]byte, 4096+13) // zeros with a short zero tail

	m := NewMatcher(BuildIndex(old, opts))
	m.Write(newData)
	ops := m.Finish()

	checkOps(t, old, newData, ops)
	_, mats := countKinds(ops)
	if mats == 0 {
		t.Fatal("expected zero blocks to match")
	}
	// Every Match must point at actual zero bytes of old.
	for _, op := range ops {
		if op.Kind != Match {
			continue
		}
		for _, b := range old[op.Offset : op.Offset+op.Length] {
			if b != 0 {
				t.Fatalf("match at %d covers non-zero old byte", op.Offset)
			}
		}
	}
}

// TestPauseResume feeds the same stream with and without a snapshot/restore
// in the middle and requires identical output.
func TestPauseResume(t *testing.T) {
	opts := Options{BlockSize: 32, WeakBits: 8}
	old := randomBytes(2048, 11)
	newData := concat(old[100:500], randomBytes(37, 12), old[1000:1900], randomBytes(13, 13))
	idx := BuildIndex(old, opts)

	m1 := NewMatcher(idx)
	m1.Write(newData)
	ops1 := m1.Finish()
	checkOps(t, old, newData, ops1)

	// Pause mid-window (117 is not a multiple of the block size) and resume
	// from a snapshot in a fresh matcher.
	m2 := NewMatcher(idx)
	m2.Write(newData[:117])
	st := m2.Snapshot()
	m3 := RestoreMatcher(idx, st)
	m3.Write(newData[117:])
	ops2 := m3.Finish()

	if !reflect.DeepEqual(ops1, ops2) {
		t.Fatalf("resumed ops differ from one-shot ops:\n%v\n%v", ops1, ops2)
	}
	checkOps(t, old, newData, ops2)
}

// TestChangeBlockSize switches to a shorter block length mid-stream via
// Reset with a new index; coverage and reconstruction must be preserved.
func TestChangeBlockSize(t *testing.T) {
	old := randomBytes(4096, 21)
	idx32 := BuildIndex(old, Options{BlockSize: 32, WeakBits: 8})
	idx16 := BuildIndex(old, Options{BlockSize: 16, WeakBits: 8})

	newData := concat(old[64:1024], randomBytes(50, 22), old[2048:3000], randomBytes(7, 23))

	m := NewMatcher(idx32)
	m.Write(newData[:1500])
	m.Reset(idx16) // change block length mid-stream
	m.Write(newData[1500:])
	ops := m.Finish()

	checkOps(t, old, newData, ops)
	_, mats := countKinds(ops)
	if mats == 0 {
		t.Fatal("expected matches after changing block size")
	}
}

// TestRollerSnapshot verifies the roller itself resumes identically.
func TestRollerSnapshot(t *testing.T) {
	data := randomBytes(500, 31)
	r1 := NewRoller(24)
	var sums1 []uint64
	for _, b := range data {
		sums1 = append(sums1, r1.Roll(b))
	}

	r2 := NewRoller(24)
	for _, b := range data[:233] {
		r2.Roll(b)
	}
	r3 := RestoreRoller(r2.Snapshot())
	var sums2 []uint64
	for _, b := range data[233:] {
		sums2 = append(sums2, r3.Roll(b))
	}
	if !reflect.DeepEqual(sums1[233:], sums2) {
		t.Fatal("restored roller diverged")
	}
}
