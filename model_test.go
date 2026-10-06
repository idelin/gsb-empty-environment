package bitstream

import (
	"math/rand"
	"testing"
)

// modelWriter is an independent bit-level transaction model: a plain
// bool slice with a cursor, a capacity, a write version and a stack of
// open regions. Every operation is specified in the simplest possible
// way so it can serve as ground truth for the real Writer.
type modelWriter struct {
	bits     []bool
	cursor   int
	capacity int
	version  uint64
	regions  []modelRegion
}

type modelRegion struct {
	start     int
	fieldBits int
	saved     []bool
}

func (m *modelWriter) write(pos int, v uint64, n int) bool {
	if n < 1 || n > 64 {
		return false
	}
	if m.capacity >= 0 && pos+n > m.capacity {
		return false
	}
	for i := 0; i < n; i++ {
		bit := v>>uint(n-1-i)&1 == 1
		p := pos + i
		for len(m.bits) < p {
			m.bits = append(m.bits, false)
		}
		if p < len(m.bits) {
			m.bits[p] = bit
		} else {
			m.bits = append(m.bits, bit)
		}
	}
	return true
}

func (m *modelWriter) writeBits(v uint64, n int) bool {
	if !m.write(m.cursor, v, n) {
		return false
	}
	m.cursor += n
	return true
}

func (m *modelWriter) beginRegion(fieldBits int) bool {
	if fieldBits < 1 || fieldBits > 64 {
		return false
	}
	r := modelRegion{
		start:     m.cursor,
		fieldBits: fieldBits,
		saved:     append([]bool(nil), m.bits[m.cursor:]...),
	}
	if !m.writeBits(0, fieldBits) {
		return false
	}
	m.regions = append(m.regions, r)
	return true
}

// closeTop closes the innermost open region; ok=false if the length
// would overflow the reserved field.
func (m *modelWriter) closeTop() (ok bool) {
	if len(m.regions) == 0 {
		return false
	}
	r := m.regions[len(m.regions)-1]
	content := m.cursor - r.start - r.fieldBits
	if content < 0 {
		return false
	}
	if r.fieldBits < 64 && uint64(content) >= 1<<uint(r.fieldBits) {
		return false
	}
	m.write(r.start, uint64(content), r.fieldBits)
	m.regions = m.regions[:len(m.regions)-1]
	return true
}

// abortTop rolls back the innermost open region.
func (m *modelWriter) abortTop() bool {
	if len(m.regions) == 0 {
		return false
	}
	r := m.regions[len(m.regions)-1]
	m.regions = m.regions[:len(m.regions)-1]
	m.bits = append(m.bits[:r.start:r.start], r.saved...)
	m.cursor = r.start
	m.version++
	return true
}

func equalBits(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func checkWriterAgainstModel(t *testing.T, w *Writer, m *modelWriter, op string) {
	t.Helper()
	if !equalBits(w.bits, m.bits) {
		t.Fatalf("after %s: bits diverge\n got %v\nwant %v", op, w.bits, m.bits)
	}
	if w.cursor != m.cursor {
		t.Fatalf("after %s: cursor %d, model %d", op, w.cursor, m.cursor)
	}
	if w.version != m.version {
		t.Fatalf("after %s: version %d, model %d", op, w.version, m.version)
	}
	if len(w.regions) != len(m.regions) {
		t.Fatalf("after %s: %d open regions, model %d", op, len(w.regions), len(m.regions))
	}
	// Packed bytes must match the model bit-for-bit, padding included.
	packed := w.Bytes()
	for i, b := range m.bits {
		got := packed[i>>3]>>uint(7-(i&7))&1 == 1
		if got != b {
			t.Fatalf("after %s: packed bit %d = %v, model %v", op, i, got, b)
		}
	}
	for i := len(m.bits); i < len(packed)*8; i++ {
		if packed[i>>3]>>uint(7-(i&7))&1 == 1 {
			t.Fatalf("after %s: padding bit %d is 1, must be 0", op, i)
		}
	}
}

// TestWriterTransactionModel drives the real Writer and the bool-array
// model through identical random operation sequences: non-aligned and
// boundary-crossing writes, overlapping overwrites via seeks, nested
// regions, rollbacks and capacity limits.
func TestWriterTransactionModel(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		capacity := 1 + rng.Intn(200)
		w := NewWriter(capacity)
		m := &modelWriter{capacity: capacity}
		// Seed the writer from random bytes so overwrites land on
		// non-trivial original content.
		if rng.Intn(2) == 0 {
			n := rng.Intn(1 + capacity/8)
			data := make([]byte, n)
			for i := range data {
				data[i] = byte(rng.Intn(256))
			}
			w = NewWriterFromBytes(data, capacity)
			m.bits = make([]bool, n*8)
			for i := range m.bits {
				m.bits[i] = data[i>>3]>>uint(7-(i&7))&1 == 1
			}
		}
		var bookmarks []Bookmark
		var modelBookmarkVersion []uint64
		var modelBookmarkPos []int

		for step := 0; step < 400; step++ {
			switch rng.Intn(8) {
			case 0, 1, 2: // write random width at cursor
				n := 1 + rng.Intn(64)
				v := uint64(rng.Int63())<<1 | uint64(rng.Intn(2))
				gotErr := w.WriteBits(v, n)
				modelOK := m.writeBits(v, n)
				if (gotErr == nil) != modelOK {
					t.Fatalf("seed %d step %d: write err=%v, model ok=%v", seed, step, gotErr, modelOK)
				}
				checkWriterAgainstModel(t, w, m, "write")
			case 3: // seek to a remembered bookmark (possibly stale)
				if len(bookmarks) == 0 {
					continue
				}
				i := rng.Intn(len(bookmarks))
				err := w.Seek(bookmarks[i])
				stale := modelBookmarkVersion[i] != m.version
				if stale && err != ErrStaleBookmark {
					t.Fatalf("seed %d step %d: stale bookmark accepted", seed, step)
				}
				if !stale {
					if err != nil {
						t.Fatalf("seed %d step %d: fresh bookmark rejected: %v", seed, step, err)
					}
					m.cursor = modelBookmarkPos[i]
				}
				checkWriterAgainstModel(t, w, m, "seek")
			case 4: // remember bookmark
				bookmarks = append(bookmarks, w.Bookmark())
				modelBookmarkVersion = append(modelBookmarkVersion, m.version)
				modelBookmarkPos = append(modelBookmarkPos, m.cursor)
			case 5: // begin nested region (bounded depth)
				if len(m.regions) >= 4 {
					continue
				}
				fieldBits := 1 + rng.Intn(8)
				_, gotErr := w.BeginRegion(fieldBits)
				modelOK := m.beginRegion(fieldBits)
				if (gotErr == nil) != modelOK {
					t.Fatalf("seed %d step %d: beginRegion err=%v, model ok=%v", seed, step, gotErr, modelOK)
				}
				checkWriterAgainstModel(t, w, m, "beginRegion")
			case 6: // close innermost region
				if len(m.regions) == 0 {
					continue
				}
				top := w.regions[len(w.regions)-1]
				gotErr := top.Close()
				modelOK := m.closeTop()
				if (gotErr == nil) != modelOK {
					t.Fatalf("seed %d step %d: close err=%v, model ok=%v", seed, step, gotErr, modelOK)
				}
				checkWriterAgainstModel(t, w, m, "close")
			case 7: // abort innermost region (rollback)
				if len(m.regions) == 0 {
					continue
				}
				top := w.regions[len(w.regions)-1]
				top.Abort()
				m.abortTop()
				checkWriterAgainstModel(t, w, m, "abort")
			}
		}

		// Final read-back round trip: every model bit must be readable
		// back through a Reader over the packed bytes.
		r := NewReader(w.Bytes())
		for i := 0; i < len(m.bits); {
			n := 1 + rng.Intn(64)
			if i+n > len(m.bits) {
				n = len(m.bits) - i
			}
			v, err := r.ReadBits(n)
			if err != nil {
				t.Fatalf("seed %d: read-back failed at bit %d: %v", seed, i, err)
			}
			for k := 0; k < n; k++ {
				got := v>>uint(n-1-k)&1 == 1
				if got != m.bits[i+k] {
					t.Fatalf("seed %d: read-back bit %d = %v, model %v", seed, i+k, got, m.bits[i+k])
				}
			}
			i += n
		}
	}
}

// modelReader is the bool-array ground truth for Reader.
type modelReader struct {
	bits []bool
	pos  int
}

func (m *modelReader) read(n int) (uint64, bool) {
	if n < 1 || n > 64 || len(m.bits)-m.pos < n {
		return 0, false
	}
	var v uint64
	for i := 0; i < n; i++ {
		v <<= 1
		if m.bits[m.pos+i] {
			v |= 1
		}
	}
	m.pos += n
	return v, true
}

// TestReaderModel checks MSB-first reads of 1..64 bits, sign extension,
// bookmark/reset and the no-advance-on-short-read rule against the
// bool-array model, on non-byte-aligned positions.
func TestReaderModel(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		data := make([]byte, 1+rng.Intn(32))
		for i := range data {
			data[i] = byte(rng.Intn(256))
		}
		m := &modelReader{bits: make([]bool, len(data)*8)}
		for i := range m.bits {
			m.bits[i] = data[i>>3]>>uint(7-(i&7))&1 == 1
		}
		r := NewReader(data)
		var bms []ReadBookmark
		var mpos []int
		for step := 0; step < 300; step++ {
			switch rng.Intn(4) {
			case 0, 1: // unsigned read
				n := 1 + rng.Intn(64)
				want, ok := m.read(n)
				posBefore := r.Pos()
				got, err := r.ReadBits(n)
				if !ok {
					if err == nil {
						t.Fatalf("seed %d step %d: read %d bits succeeded, model says short", seed, step, n)
					}
					if r.Pos() != posBefore {
						t.Fatalf("seed %d step %d: failed read advanced pos", seed, step)
					}
				} else {
					if err != nil || got != want {
						t.Fatalf("seed %d step %d: got %x, %v; want %x", seed, step, got, err, want)
					}
				}
				if r.Pos() != m.pos {
					t.Fatalf("seed %d step %d: pos %d, model %d", seed, step, r.Pos(), m.pos)
				}
			case 2: // signed read, verify sign extension bit by bit
				n := 1 + rng.Intn(64)
				if len(m.bits)-m.pos < n {
					continue
				}
				got, err := r.ReadSigned(n)
				if err != nil {
					t.Fatal(err)
				}
				var want uint64
				sign := m.bits[m.pos]
				for i := 0; i < 64; i++ {
					bit := sign
					if i >= 64-n {
						bit = m.bits[m.pos+i-(64-n)]
					}
					if bit {
						want |= 1 << uint(63-i)
					}
				}
				m.pos += n
				if uint64(got) != want {
					t.Fatalf("seed %d step %d: signed got %d, want %d", seed, step, got, int64(want))
				}
			case 3: // bookmark / reset
				if rng.Intn(2) == 0 || len(bms) == 0 {
					bms = append(bms, r.Bookmark())
					mpos = append(mpos, m.pos)
				} else {
					i := rng.Intn(len(bms))
					r.Reset(bms[i])
					m.pos = mpos[i]
				}
				if r.Pos() != m.pos {
					t.Fatalf("seed %d step %d: pos %d, model %d", seed, step, r.Pos(), m.pos)
				}
			}
		}
	}
}

// TestSignedRoundTripModel writes random signed values and reads them
// back across non-byte-aligned offsets.
func TestSignedRoundTripModel(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 200; iter++ {
		w := NewWriter(Unlimited)
		pad := rng.Intn(8)
		if pad > 0 {
			if err := w.WriteBits(0, pad); err != nil {
				t.Fatal(err)
			}
		}
		var widths []int
		var values []int64
		for i := 0; i < 1+rng.Intn(10); i++ {
			n := 1 + rng.Intn(64)
			shift := uint(64 - n)
			v := (rng.Int63() << shift) >> shift // random value fitting n bits
			if err := w.WriteSigned(v, n); err != nil {
				t.Fatal(err)
			}
			widths = append(widths, n)
			values = append(values, v)
		}
		r := NewReader(w.Bytes())
		if pad > 0 {
			if _, err := r.ReadBits(pad); err != nil {
				t.Fatal(err)
			}
		}
		for i, n := range widths {
			got, err := r.ReadSigned(n)
			if err != nil || got != values[i] {
				t.Fatalf("iter %d: ReadSigned(%d) = %d, %v; want %d", iter, n, got, err, values[i])
			}
		}
	}
}
