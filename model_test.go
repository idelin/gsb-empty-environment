package bitstream

import (
	"math/rand"
	"testing"
)

// bitModel is the reference model: a plain array of booleans.
type bitModel struct {
	bits []bool
	pos  int
	hi   int
}

func (m *bitModel) write(v uint64, n int) {
	for len(m.bits) < m.pos+n {
		m.bits = append(m.bits, false)
	}
	for i := 0; i < n; i++ {
		m.bits[m.pos+i] = (v>>uint(n-1-i))&1 == 1
	}
	m.pos += n
	if m.pos > m.hi {
		m.hi = m.pos
	}
}

func (m *bitModel) bytes() []byte {
	out := make([]byte, (m.hi+7)/8)
	for i := 0; i < m.hi; i++ {
		if m.bits[i] {
			out[i>>3] |= 1 << uint(7-(uint(i)&7))
		}
	}
	return out
}

func (m *bitModel) snapshot() (bits []bool, pos, hi int) {
	cp := make([]bool, len(m.bits))
	copy(cp, m.bits)
	return cp, m.pos, m.hi
}

func (m *bitModel) restore(bits []bool, pos, hi int) {
	m.bits = bits
	m.pos = pos
	m.hi = hi
}

func checkWriter(t *testing.T, w *Writer, m *bitModel) {
	t.Helper()
	got, want := w.Bytes(), m.bytes()
	if w.Len() != m.hi {
		t.Fatalf("Len: got %d want %d", w.Len(), m.hi)
	}
	if len(got) != len(want) {
		t.Fatalf("Bytes len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d: got %08b want %08b", i, got[i], want[i])
		}
	}
}

// Random writes and seeks (overlapping overwrites included), checked
// against the boolean-array model after every operation, then a
// random-chunk read roundtrip with bookmarks.
func TestModelRandomOps(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	w := NewWriter()
	m := &bitModel{}
	var marks []Bookmark
	var mpos []int

	for op := 0; op < 4000; op++ {
		switch rng.Intn(10) {
		case 0, 1: // seek to a previous bookmark (overwrite path)
			if len(marks) == 0 {
				continue
			}
			k := rng.Intn(len(marks))
			if err := w.Seek(marks[k]); err != nil {
				t.Fatalf("seek: %v", err)
			}
			m.pos = mpos[k]
		case 2: // record bookmark
			marks = append(marks, w.Bookmark())
			mpos = append(mpos, m.pos)
		default: // write 1..64 bits
			n := 1 + rng.Intn(64)
			v := rng.Uint64()
			if n < 64 {
				v &= (uint64(1) << uint(n)) - 1
			}
			if err := w.WriteBits(v, n); err != nil {
				t.Fatalf("write: %v", err)
			}
			m.write(v, n)
		}
		checkWriter(t, w, m)
	}

	// Roundtrip: read back in random chunks, with bookmark resets.
	r, err := NewReader(w.Bytes(), w.Len())
	if err != nil {
		t.Fatal(err)
	}
	pos := 0
	for pos < m.hi {
		if rng.Intn(8) == 0 {
			bm := r.Bookmark()
			n := 1 + rng.Intn(8)
			if pos+n > m.hi {
				n = m.hi - pos
			}
			if n > 0 {
				if _, err := r.ReadBits(n); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Reset(bm); err != nil {
				t.Fatal(err)
			}
		}
		n := 1 + rng.Intn(64)
		if pos+n > m.hi {
			n = m.hi - pos
		}
		v, err := r.ReadBits(n)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			want := uint64(0)
			if m.bits[pos+i] {
				want = 1
			}
			if got := (v >> uint(n-1-i)) & 1; got != want {
				t.Fatalf("roundtrip bit %d: got %d want %d", pos+i, got, want)
			}
		}
		pos += n
	}
	if r.Remaining() != 0 {
		t.Fatalf("remaining: %d", r.Remaining())
	}
}

// Non-byte-aligned writes must leave zero padding in the final byte.
func TestPaddingBitsAreZero(t *testing.T) {
	w := NewWriter()
	if err := w.WriteBits(0x7, 3); err != nil { // 111
		t.Fatal(err)
	}
	if got := w.Bytes(); len(got) != 1 || got[0] != 0xE0 {
		t.Fatalf("got %08b", got)
	}
	if err := w.WriteBits(0x1, 1); err != nil { // 1111
		t.Fatal(err)
	}
	if got := w.Bytes(); got[0] != 0xF0 {
		t.Fatalf("got %08b", got)
	}
	// Overwrite the low half; the high bits stay, padding stays zero.
	bm := w.Bookmark()
	if err := w.WriteBits(0, 4); err != nil {
		t.Fatal(err)
	}
	if err := w.Seek(bm); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBits(0xA, 4); err != nil { // 1010
		t.Fatal(err)
	}
	if got := w.Bytes(); got[0] != 0xFA {
		t.Fatalf("got %08b", got)
	}
	// Shorten back below a byte boundary: padding must read as zero.
	w2 := NewWriter()
	if err := w2.WriteBits(0x1F, 5); err != nil { // 11111
		t.Fatal(err)
	}
	if got := w2.Bytes(); got[0] != 0xF8 {
		t.Fatalf("got %08b", got)
	}
}

func TestSignExtension(t *testing.T) {
	cases := []struct {
		v int64
		n int
	}{
		{-1, 4}, {-8, 4}, {7, 4}, {-1, 1}, {0, 1},
		{-123456789, 31}, {123456789, 31},
		{-1, 64}, {1<<63 - 1, 64}, {-1 << 63, 64}, {5, 64},
	}
	w := NewWriter()
	for _, c := range cases {
		if err := w.WriteSigned(c.v, c.n); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewReader(w.Bytes(), w.Len())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got, err := r.ReadSigned(c.n)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.v {
			t.Fatalf("ReadSigned(%d): got %d want %d", c.n, got, c.v)
		}
	}
}

func TestReadUnderflowNoAdvance(t *testing.T) {
	w := NewWriter()
	if err := w.WriteBits(0xAB, 8); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(w.Bytes(), w.Len())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadBits(4); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadBits(5); err != ErrUnderflow {
		t.Fatalf("want ErrUnderflow, got %v", err)
	}
	if r.Pos() != 4 {
		t.Fatalf("position advanced on underflow: %d", r.Pos())
	}
	v, err := r.ReadBits(4)
	if err != nil || v != 0xB {
		t.Fatalf("got %x, %v", v, err)
	}
	if _, err := r.ReadBits(1); err != ErrUnderflow {
		t.Fatalf("want Err underflow at end, got %v", err)
	}
}
