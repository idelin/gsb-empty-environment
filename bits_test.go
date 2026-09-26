package rlebits

import (
	"math/rand"
	"testing"
)

// naive is a plain []byte bit model used as ground truth.
type naive []byte

func (n *naive) flip(l, r int) {
	for i := l; i < r; i++ {
		(*n)[i] ^= 1
	}
}

func (n *naive) insert(pos int, val byte, count int) {
	add := make([]byte, count)
	for i := range add {
		add[i] = val
	}
	*n = append((*n)[:pos], append(add, (*n)[pos:]...)...)
}

func (n *naive) delete(l, r int) {
	*n = append((*n)[:l], (*n)[r:]...)
}

func (n naive) rank(pos int, val byte) uint64 {
	var c uint64
	for i := 0; i < pos; i++ {
		if n[i] == val {
			c++
		}
	}
	return c
}

func (n naive) sel(k uint64, val byte) int64 {
	var c uint64
	for i, b := range n {
		if b == val {
			if c == k {
				return int64(i)
			}
			c++
		}
	}
	return -1
}

func mustInvariants(t *testing.T, b *Bits) {
	t.Helper()
	if err := b.CheckInvariants(); err != nil {
		t.Fatalf("invariant violated: %v (runs=%v)", err, b.lens)
	}
}

// assertSame compares the full sequence plus rank/select at every position.
func assertSame(t *testing.T, b *Bits, n naive) {
	t.Helper()
	mustInvariants(t, b)
	if b.Len() != uint64(len(n)) {
		t.Fatalf("Len=%d, want %d", b.Len(), len(n))
	}
	if got := b.String(); got != string(mapBytes(n)) {
		t.Fatalf("bits=%q, want %q", got, string(mapBytes(n)))
	}
	for pos := uint64(0); pos <= uint64(len(n)); pos++ {
		if got, want := b.Rank(pos, 1), n.rank(int(pos), 1); got != want {
			t.Fatalf("Rank(%d,1)=%d, want %d", pos, got, want)
		}
		if got, want := b.Rank(pos, 0), n.rank(int(pos), 0); got != want {
			t.Fatalf("Rank(%d,0)=%d, want %d", pos, got, want)
		}
	}
	for k := uint64(0); k <= uint64(len(n)); k++ {
		if got, want := b.Select(k, 1), n.sel(k, 1); got != want {
			t.Fatalf("Select(%d,1)=%d, want %d", k, got, want)
		}
		if got, want := b.Select(k, 0), n.sel(k, 0); got != want {
			t.Fatalf("Select(%d,0)=%d, want %d", k, got, want)
		}
	}
}

func mapBytes(n naive) []byte {
	out := make([]byte, len(n))
	for i, v := range n {
		out[i] = '0' + v
	}
	return out
}

func toNaive(s string) naive {
	n := make(naive, len(s))
	for i := range s {
		n[i] = s[i] - '0'
	}
	return n
}

func TestNewRankSelect(t *testing.T) {
	s := "0001100101111000"
	b := New(s)
	assertSame(t, b, toNaive(s))
	if b.Runs() != 7 {
		t.Fatalf("Runs=%d, want 7", b.Runs())
	}
}

func TestFlipTwiceRestores(t *testing.T) {
	cases := []struct{ l, r uint64 }{
		{0, 5}, {3, 9}, {0, 16}, {7, 8}, {2, 14}, {15, 16},
	}
	for _, c := range cases {
		b := New("0001100101111000")
		before := b.Encode()
		b.Flip(c.l, c.r)
		mustInvariants(t, b)
		b.Flip(c.l, c.r)
		if got := b.Encode(); string(got) != string(before) {
			t.Fatalf("flip(%d,%d) twice: got %q, want original", c.l, c.r, b.String())
		}
	}
}

func TestFlipMatchesNaive(t *testing.T) {
	s := "0001100101111000"
	for l := 0; l <= len(s); l++ {
		for r := l; r <= len(s); r++ {
			b := New(s)
			n := toNaive(s)
			b.Flip(uint64(l), uint64(r))
			n.flip(l, r)
			assertSame(t, b, n)
		}
	}
}

func TestDeleteAcrossRunsLeavesNoZeroLength(t *testing.T) {
	s := "0001100101111000" // runs: 3,2,2,1,1,1,4,3
	for l := 0; l <= len(s); l++ {
		for r := l; r <= len(s); r++ {
			b := New(s)
			n := toNaive(s)
			b.Delete(uint64(l), uint64(r))
			n.delete(l, r)
			assertSame(t, b, n)
			for i, rl := range b.lens {
				if rl == 0 {
					t.Fatalf("delete [%d,%d): zero-length run at %d", l, r, i)
				}
			}
		}
	}
}

func TestInsertAtRunBoundary(t *testing.T) {
	s := "000111000"
	b := New(s) // boundaries at 0,3,6,9
	// Insert 1s exactly at the boundary between the 1-run and 0-run.
	b.Insert(6, 1, 2)
	if got, want := b.String(), "00011111000"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if b.Runs() != 3 {
		t.Fatalf("Runs=%d, want 3 (boundary insert must extend, not split)", b.Runs())
	}
	// Insert 0s at the same boundary: extends the following 0-run.
	b = New(s)
	b.Insert(6, 0, 2)
	if got, want := b.String(), "00011100000"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if b.Runs() != 3 {
		t.Fatalf("Runs=%d, want 3", b.Runs())
	}
	// Insert opposite value at a boundary: exactly one new run, positions stay unique.
	b = New(s)
	b.Insert(3, 1, 2) // extends existing 1-run
	if got, want := b.String(), "00011111000"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	assertSame(t, b, toNaive("00011111000"))
}

func TestInsertEverywhereMatchesNaive(t *testing.T) {
	s := "0110010"
	for pos := 0; pos <= len(s); pos++ {
		for _, val := range []byte{0, 1} {
			b := New(s)
			n := toNaive(s)
			b.Insert(uint64(pos), val, 3)
			n.insert(pos, val, 3)
			assertSame(t, b, n)
		}
	}
	// Insert into empty sequence.
	b := New("")
	b.Insert(0, 1, 4)
	assertSame(t, b, naive{1, 1, 1, 1})
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, s := range []string{"", "0", "1", "0001100101111000", "111"} {
		b := New(s)
		d, err := Decode(b.Encode())
		if err != nil {
			t.Fatalf("Decode(%q): %v", s, err)
		}
		if d.String() != b.String() || d.first != b.first {
			t.Fatalf("round trip %q: got %q", s, d.String())
		}
		mustInvariants(t, d)
	}
}

func TestFuzzAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20260926))
	b := New("")
	var n naive
	for step := 0; step < 4000; step++ {
		length := len(n)
		op := rng.Intn(5)
		switch op {
		case 0, 1: // insert
			pos := rng.Intn(length + 1)
			val := byte(rng.Intn(2))
			cnt := uint32(1 + rng.Intn(7))
			if length+int(cnt) > 400 {
				continue
			}
			b.Insert(uint64(pos), val, cnt)
			n.insert(pos, val, int(cnt))
		case 2: // delete
			if length == 0 {
				continue
			}
			l := rng.Intn(length)
			r := l + rng.Intn(length-l+1)
			b.Delete(uint64(l), uint64(r))
			n.delete(l, r)
		case 3: // flip
			if length == 0 {
				continue
			}
			l := rng.Intn(length)
			r := l + rng.Intn(length-l+1)
			b.Flip(uint64(l), uint64(r))
			n.flip(l, r)
		case 4: // flip twice must restore
			if length == 0 {
				continue
			}
			l := rng.Intn(length)
			r := l + rng.Intn(length-l+1)
			before := b.Encode()
			b.Flip(uint64(l), uint64(r))
			b.Flip(uint64(l), uint64(r))
			if string(b.Encode()) != string(before) {
				t.Fatalf("step %d: flip(%d,%d) twice did not restore", step, l, r)
			}
		}
		assertSame(t, b, n)
	}
	t.Logf("final: %d bits in %d runs, encoded=%dB heap~%dB",
		b.Len(), b.Runs(), b.EncodedBytes(), b.HeapBytes())
}
