package bitstream

import (
	"math/rand"
	"testing"
)

// Nested length fields: outer wraps inner; lengths are back-patched and
// verified by reading the stream back.
func TestNestedLengthFields(t *testing.T) {
	w := NewWriter()
	if err := w.WriteBits(0x5, 3); err != nil { // non-aligned prefix
		t.Fatal(err)
	}
	if err := w.BeginLenField(8); err != nil { // outer
		t.Fatal(err)
	}
	if err := w.WriteBits(0x2, 2); err != nil {
		t.Fatal(err)
	}
	if err := w.BeginLenField(4); err != nil { // inner
		t.Fatal(err)
	}
	if err := w.WriteBits(0x1F, 5); err != nil { // inner content: 5 bits
		t.Fatal(err)
	}
	if err := w.EndLenField(); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBits(0x1, 1); err != nil { // more outer content
		t.Fatal(err)
	}
	if err := w.EndLenField(); err != nil {
		t.Fatal(err)
	}

	r, err := NewReader(w.Bytes(), w.Len())
	if err != nil {
		t.Fatal(err)
	}
	prefix, _ := r.ReadBits(3)
	outerLen, _ := r.ReadBits(8)
	pre, _ := r.ReadBits(2)
	innerLen, _ := r.ReadBits(4)
	inner, _ := r.ReadBits(5)
	tail, _ := r.ReadBits(1)
	if prefix != 0x5 || pre != 0x2 || inner != 0x1F || tail != 0x1 {
		t.Fatalf("content mismatch: %b %b %b %b", prefix, pre, inner, tail)
	}
	// inner content = 5 bits; outer content = 2 + 4 + 5 + 1 = 12 bits
	if innerLen != 5 || outerLen != 12 {
		t.Fatalf("lengths: outer=%d inner=%d", outerLen, innerLen)
	}
	if r.Remaining() != 0 {
		t.Fatalf("remaining %d", r.Remaining())
	}
}

// Bit-level transaction model: random nested regions, aborts, overwrites
// and capacity limits, all checked against the boolean-array model.
func TestTransactionModel(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	w := NewWriterCap(512)
	m := &bitModel{}

	type frame struct {
		bits     []bool
		pos      int
		hi       int
		fieldPos int
		width    int
	}
	var stack []frame

	for op := 0; op < 3000; op++ {
		switch rng.Intn(10) {
		case 0, 1: // begin region
			width := 1 + rng.Intn(8)
			bits, pos, hi := m.snapshot()
			if err := w.BeginLenField(width); err != nil {
				if err != ErrCapacity {
					t.Fatalf("begin: %v", err)
				}
				checkWriter(t, w, m) // failed begin leaves state untouched
				continue
			}
			stack = append(stack, frame{bits, pos, hi, pos, width})
			m.write(0, width)
		case 2, 3: // abort innermost region
			if len(stack) == 0 {
				continue
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if err := w.Abort(); err != nil {
				t.Fatal(err)
			}
			m.restore(f.bits, f.pos, f.hi)
		case 4: // end region: back-patch content length in the model too
			if len(stack) == 0 {
				continue
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			content := m.pos - (f.fieldPos + f.width)
			err := w.EndLenField()
			if f.width < 64 && uint64(content) >= uint64(1)<<uint(f.width) {
				if err != ErrLenOverflow {
					t.Fatalf("want ErrLenOverflow, got %v", err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("end: %v", err)
			}
			saved := m.pos
			m.pos = f.fieldPos
			m.write(uint64(content), f.width)
			m.pos = saved
		default: // write random bits
			n := 1 + rng.Intn(16)
			v := rng.Uint64() & ((uint64(1) << uint(n)) - 1)
			if err := w.WriteBits(v, n); err != nil {
				if err != ErrCapacity {
					t.Fatalf("write: %v", err)
				}
				checkWriter(t, w, m) // failed write leaves state untouched
				continue
			}
			m.write(v, n)
		}
		checkWriter(t, w, m)
	}
}

// Aborting a child region must restore the parent's placeholder and any
// neighbour bits sharing a byte, and invalidate prior bookmarks.
func TestNestedRollbackRestores(t *testing.T) {
	w := NewWriter()
	if err := w.WriteBits(0x5, 3); err != nil { // 101, non-aligned
		t.Fatal(err)
	}
	base := w.Bytes()
	baseLen := w.Len()
	stale := w.Bookmark()

	if err := w.BeginLenField(6); err != nil { // outer
		t.Fatal(err)
	}
	if err := w.WriteBits(0xFF, 8); err != nil {
		t.Fatal(err)
	}
	if err := w.BeginLenField(4); err != nil { // inner
		t.Fatal(err)
	}
	if err := w.WriteBits(0xFF, 8); err != nil { // "failing" child content
		t.Fatal(err)
	}
	if err := w.Abort(); err != nil { // roll child back
		t.Fatal(err)
	}
	if err := w.Abort(); err != nil { // roll parent back
		t.Fatal(err)
	}

	if w.Len() != baseLen {
		t.Fatalf("len after rollback: got %d want %d", w.Len(), baseLen)
	}
	got := w.Bytes()
	if len(got) != len(base) || got[0] != base[0] {
		t.Fatalf("bits after rollback: got %08b want %08b", got, base)
	}
	if err := w.Seek(stale); err != ErrStaleBookmark {
		t.Fatalf("want ErrStaleBookmark, got %v", err)
	}
}

// Overlapping overwrite must flip only the targeted bits.
func TestOverlappingOverwrite(t *testing.T) {
	w := NewWriter()
	if err := w.WriteBits(0xF, 4); err != nil {
		t.Fatal(err)
	}
	bm4 := w.Bookmark()
	if err := w.WriteBits(0xFFF, 12); err != nil {
		t.Fatal(err)
	}
	bm16 := w.Bookmark()
	if err := w.WriteBits(0x3, 2); err != nil { // tail into third byte
		t.Fatal(err)
	}
	if err := w.Seek(bm4); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBits(0x0, 4); err != nil { // clear bits 4..7
		t.Fatal(err)
	}
	if err := w.Seek(bm16); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBits(0x0, 2); err != nil { // rewrite tail
		t.Fatal(err)
	}
	// expect 1111 0000 1111 1111 00
	got := w.Bytes()
	want := []byte{0xF0, 0xFF, 0x00}
	if w.Len() != 18 || len(got) != 3 {
		t.Fatalf("len %d bytes %v", w.Len(), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d: got %08b want %08b", i, got[i], want[i])
		}
	}
}

// Capacity: writes and region placeholders beyond the limit fail without
// modifying any state.
func TestCapacityExceeded(t *testing.T) {
	w := NewWriterCap(10)
	if err := w.WriteBits(0xFF, 8); err != nil {
		t.Fatal(err)
	}
	before := w.Bytes()
	if err := w.WriteBits(0x7, 3); err != ErrCapacity {
		t.Fatalf("want ErrCapacity, got %v", err)
	}
	if err := w.BeginLenField(4); err != ErrCapacity {
		t.Fatalf("want ErrCapacity, got %v", err)
	}
	if w.Len() != 8 || w.Pos() != 8 {
		t.Fatalf("state mutated: len=%d pos=%d", w.Len(), w.Pos())
	}
	after := w.Bytes()
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("byte %d changed on failed write", i)
		}
	}
	// Exactly fitting write still works.
	if err := w.WriteBits(0x3, 2); err != nil {
		t.Fatal(err)
	}
	if w.Len() != 10 {
		t.Fatalf("len %d", w.Len())
	}
}

// Length field too small for the content is reported.
func TestLengthOverflow(t *testing.T) {
	w := NewWriter()
	if err := w.BeginLenField(2); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBits(0xF, 4); err != nil { // 4 > 3 max
		t.Fatal(err)
	}
	if err := w.EndLenField(); err != ErrLenOverflow {
		t.Fatalf("want ErrLenOverflow, got %v", err)
	}
}
