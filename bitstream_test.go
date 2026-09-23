package bitstream

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// bitArray is the independent per-bit reference model. It mirrors the
// specification: a value is split into bytes by byte order, and each
// byte is serialized into stream bits by bit order.
type bitArray struct {
	bits []uint8
}

// refPut appends the stream bits for the low n bits of v.
func refPut(a *bitArray, v uint64, n int, cfg Config) {
	nBytes := (n + 7) / 8
	for i := 0; i < nBytes; i++ {
		lo := i * 8
		width := n - lo
		if width > 8 {
			width = 8
		}
		var logicalByte uint8
		if cfg.Bytes == LittleEndian {
			logicalByte = uint8(v >> uint(lo))
		} else {
			logicalByte = uint8(v >> uint(n-lo-width))
		}
		for k := 0; k < width; k++ {
			var bit uint8
			if cfg.Bits == LSBFirst {
				bit = (logicalByte >> uint(k)) & 1
			} else {
				bit = (logicalByte >> uint(width-1-k)) & 1
			}
			a.bits = append(a.bits, bit)
		}
	}
}

// refGather reads n stream bits at off and reassembles the value.
func refGather(a *bitArray, off, n int, cfg Config) uint64 {
	var v uint64
	nBytes := (n + 7) / 8
	for i := 0; i < nBytes; i++ {
		lo := i * 8
		width := n - lo
		if width > 8 {
			width = 8
		}
		var logicalByte uint8
		for k := 0; k < width; k++ {
			if a.bits[off+lo+k] == 1 {
				if cfg.Bits == LSBFirst {
					logicalByte |= 1 << uint(k)
				} else {
					logicalByte |= 1 << uint(width-1-k)
				}
			}
		}
		if cfg.Bytes == LittleEndian {
			v |= uint64(logicalByte) << uint(lo)
		} else {
			v |= uint64(logicalByte) << uint(n-lo-width)
		}
	}
	return v
}

// logicalBits extracts stream bits 0..n-1 from packed bytes.
func logicalBits(raw []byte, n int, bo BitOrder, by ByteOrder) []uint8 {
	out := make([]uint8, n)
	for p := 0; p < n; p++ {
		byteIdx, bitIdx := p/8, uint(p%8)
		var b uint8
		if bo == LSBFirst {
			b = (raw[byteIdx] >> bitIdx) & 1
		} else {
			b = (raw[byteIdx] >> (7 - bitIdx)) & 1
		}
		out[p] = b
	}
	return out
}

var configs = []Config{
	{MSBFirst, BigEndian},
	{MSBFirst, LittleEndian},
	{LSBFirst, BigEndian},
	{LSBFirst, LittleEndian},
}

func cfgName(c Config) string {
	bo := "MSB"
	if c.Bits == LSBFirst {
		bo = "LSB"
	}
	by := "BE"
	if c.Bytes == LittleEndian {
		by = "LE"
	}
	return bo + "/" + by
}

// Exhaustive round trip over every value for widths 1..12, checking the
// packed byte layout against the per-bit model.
func TestExhaustiveShortValues(t *testing.T) {
	for _, cfg := range configs {
		for width := 1; width <= 12; width++ {
			limit := 1 << uint(width)
			for v := 0; v < limit; v++ {
				w := NewWriter(cfg)
				if err := w.Write(uint64(v), width); err != nil {
					t.Fatalf("%s w=%d v=%d: %v", cfgName(cfg), width, v, err)
				}
				raw := w.Bytes()
				ref := &bitArray{}
				refPut(ref, uint64(v), width, cfg)
				got := logicalBits(raw, width, cfg.Bits, cfg.Bytes)
				if !bytes.Equal(got, ref.bits) {
					t.Fatalf("%s w=%d v=%0*b layout mismatch\nwant %v\nraw  %08b\ngot  %v",
						cfgName(cfg), width, width, v, ref.bits, raw, got)
				}
				c := &Chain{}
				c.AppendBytes(raw, width)
				r := NewReader(cfg, c)
				gotv, err := r.Read(width)
				if err != nil || gotv != uint64(v) {
					t.Fatalf("%s w=%d v=%d: got %d err %v", cfgName(cfg), width, v, gotv, err)
				}
			}
		}
	}
}

// Cross-byte 13-bit reads: three packed values are unaligned and span
// bytes and blocks.
func TestCrossByte13Bits(t *testing.T) {
	vals := []uint64{0x1ABC, 0x0001, 0x1FFF} // 13-bit values
	for _, cfg := range configs {
		w := NewWriter(cfg)
		// Start unaligned with a 3-bit prefix.
		if err := w.Write(0b101, 3); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			if err := w.Write(v, 13); err != nil {
				t.Fatal(err)
			}
		}
		raw := w.Bytes()
		// Split raw into two blocks at an arbitrary byte boundary to
		// prove cross-block reads are zero-copy correct.
		c := &Chain{}
		split := 1
		bitsFirst := split * 8
		bitsTotal := 3 + 13*len(vals)
		c.AppendBytes(raw[:split], min(bitsFirst, bitsTotal))
		c.AppendBytes(raw[split:], bitsTotal-bitsFirst)
		r := NewReader(cfg, c)
		if p, _ := r.Read(3); p != 0b101 {
			t.Fatalf("%s prefix", cfgName(cfg))
		}
		for i, want := range vals {
			got, err := r.Read(13)
			if err != nil || got != want {
				t.Fatalf("%s val %d: got %#x err %v want %#x",
					cfgName(cfg), i, got, err, want)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Random operation sequences validated against the bit-array model,
// checking both returned values and the cursor after every step.
func TestRandomOperationSequences(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, cfg := range configs {
		for trial := 0; trial < 40; trial++ {
			w := NewWriter(cfg)
			ref := &bitArray{}
			// Build a random stream.
			for i := 0; i < 30; i++ {
				n := 1 + rng.Intn(64)
				v := rng.Uint64()
				if n < 64 {
					v &= (1 << uint(n)) - 1
				}
				if err := w.Write(v, n); err != nil {
					t.Fatal(err)
				}
				refPut(ref, v, n, cfg)
			}
			raw := w.Bytes()
			// Random chunking into blocks.
			c := &Chain{}
			pos := 0
			total := len(ref.bits)
			for pos < total {
				n := 1 + rng.Intn(3) // bytes per block
				bitLen := n * 8
				if pos+bitLen > total {
					bitLen = total - pos
				}
				byteOff := pos / 8
				byteEnd := (pos + bitLen + 7) / 8
				c.AppendBytes(raw[byteOff:byteEnd], bitLen)
				pos += bitLen
			}
			r := NewReader(cfg, c)
			cursor := 0
			var marks []int
			for step := 0; step < 120; step++ {
				remain := total - cursor
				if remain < 0 {
					remain = 0
				}
				switch rng.Intn(7) {
				case 0, 1: // read
					if remain == 0 {
						continue
					}
					n := 1 + rng.Intn(min(64, remain))
					got, err := r.Read(n)
					if err != nil {
						t.Fatalf("read: %v", err)
					}
					want := refGather(ref, cursor, n, cfg)
					if got != want {
						t.Fatalf("%s step %d: read(%d)=%#x want %#x",
							cfgName(cfg), step, n, got, want)
					}
					cursor += n
				case 2: // peek
					if remain == 0 {
						continue
					}
					n := 1 + rng.Intn(min(64, remain))
					got, err := r.Peek(n)
					if err != nil {
						t.Fatalf("peek: %v", err)
					}
					want := refGather(ref, cursor, n, cfg)
					if got != want {
						t.Fatalf("%s step %d: peek(%d)=%#x want %#x",
							cfgName(cfg), step, n, got, want)
					}
					if r.Pos() != cursor {
						t.Fatalf("peek moved cursor: %d != %d", r.Pos(), cursor)
					}
				case 3: // mark
					r.Mark()
					marks = append(marks, cursor)
				case 4: // rollback
					if len(marks) == 0 {
						continue
					}
					if err := r.Rollback(); err != nil {
						t.Fatalf("rollback: %v", err)
					}
					cursor = marks[len(marks)-1]
					marks = marks[:len(marks)-1]
				case 5: // commit
					if len(marks) == 0 {
						continue
					}
					if err := r.Commit(); err != nil {
						t.Fatalf("commit: %v", err)
					}
					marks = marks[:len(marks)-1]
				case 6: // align
					next := cursor
					if rem := cursor % 8; rem != 0 {
						next += 8 - rem
					}
					if next > total {
						continue
					}
					r.Align(8)
					cursor = next
				}
				if r.Pos() != cursor {
					t.Fatalf("%s step %d: cursor %d, model %d",
						cfgName(cfg), step, r.Pos(), cursor)
				}
			}
		}
	}
}

// Short reads must fail without consuming any bit.
func TestShortReadConsumesNothing(t *testing.T) {
	for _, cfg := range configs {
		w := NewWriter(cfg)
		_ = w.Write(0xAB, 8)
		c := &Chain{}
		c.AppendBytes(w.Bytes(), 8)
		r := NewReader(cfg, c)
		if _, err := r.Read(9); err != ErrShortBuffer {
			t.Fatalf("want ErrShortBuffer, got %v", err)
		}
		if r.Pos() != 0 {
			t.Fatalf("cursor moved on short read: %d", r.Pos())
		}
		v, err := r.Read(8)
		if err != nil || v != 0xAB {
			t.Fatalf("got %#x err %v", v, err)
		}
	}
}

// Invalid widths are rejected before the buffer is modified.
func TestInvalidWidthRejectedFirst(t *testing.T) {
	w := NewWriter(configs[0])
	_ = w.Write(1, 1)
	before := w.Bytes()
	for _, n := range []int{0, -1, 65, 100} {
		if err := w.Write(0xFF, n); err != ErrInvalidWidth {
			t.Fatalf("write width %d: %v", n, err)
		}
	}
	if !bytes.Equal(before, w.Bytes()) || w.Len() != 1 {
		t.Fatal("buffer modified by invalid write")
	}
	c := &Chain{}
	c.AppendBytes(before, 1)
	r := NewReader(configs[0], c)
	for _, n := range []int{0, -5, 65} {
		if _, err := r.Read(n); err != ErrInvalidWidth {
			t.Fatalf("read width %d: %v", n, err)
		}
		if _, err := r.Peek(n); err != ErrInvalidWidth {
			t.Fatalf("peek width %d: %v", n, err)
		}
	}
	if r.Pos() != 0 {
		t.Fatal("cursor moved by invalid widths")
	}
}

// Quota and cancellation errors consume no input.
func TestQuotaAndCancelConsumeNothing(t *testing.T) {
	w := NewWriter(configs[0])
	_ = w.Write(0xDEAD, 16)
	c := &Chain{}
	c.AppendBytes(w.Bytes(), 16)
	r := NewReader(configs[0], c)
	r.SetQuota(8)
	if _, err := r.Read(9); err != ErrQuotaExceeded {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
	if r.Pos() != 0 {
		t.Fatal("quota error consumed input")
	}
	done := make(chan struct{})
	close(done)
	if _, err := r.ReadCtx(done, 4); err != ErrCanceled {
		t.Fatalf("want ErrCanceled, got %v", err)
	}
	if r.Pos() != 0 {
		t.Fatal("cancel consumed input")
	}
	if _, err := r.Read(8); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(1); err != ErrQuotaExceeded {
		t.Fatalf("quota should be exhausted, got %v", err)
	}
}

// Nested transactions: inner rollback does not disturb the outer mark.
func TestNestedTransactions(t *testing.T) {
	w := NewWriter(configs[0])
	_ = w.Write(0x12, 8)
	_ = w.Write(0x34, 8)
	_ = w.Write(0x56, 8)
	c := &Chain{}
	c.AppendBytes(w.Bytes(), 24)
	r := NewReader(configs[0], c)

	r.Mark() // outer @0
	if v, _ := r.Read(8); v != 0x12 {
		t.Fatal(v)
	}
	r.Mark() // inner @8
	if v, _ := r.Read(8); v != 0x34 {
		t.Fatal(v)
	}
	if err := r.Rollback(); err != nil { // back to 8
		t.Fatal(err)
	}
	if r.Pos() != 8 {
		t.Fatalf("inner rollback pos=%d", r.Pos())
	}
	if v, _ := r.Read(16); v != 0x3456 {
		t.Fatalf("%#x", v)
	}
	if err := r.Rollback(); err != nil { // outer back to 0
		t.Fatal(err)
	}
	if r.Pos() != 0 {
		t.Fatalf("outer rollback pos=%d", r.Pos())
	}
	if err := r.Rollback(); err != ErrNoMark {
		t.Fatalf("want ErrNoMark, got %v", err)
	}
}

// Peek, mark and rollback share one retention water mark: blocks below
// the oldest mark stay alive; once committed they are reclaimed and a
// stale view into them fails.
func TestWatermarkAndReclaim(t *testing.T) {
	cfg := configs[0]
	w := NewWriter(cfg)
	for i := 0; i < 8; i++ {
		_ = w.Write(uint64(i), 8)
	}
	raw := w.Bytes()
	c := &Chain{}
	for i := 0; i < 8; i++ {
		c.AppendBytes(raw[i:i+1], 8) // one block per byte
	}
	base := BufferStats()
	r := NewReader(cfg, c)
	r.Mark()                              // mark at 0 pins block 0
	if _, err := r.Read(32); err != nil { // consume blocks 0..3
		t.Fatal(err)
	}
	// Water mark is the mark at 0: nothing reclaimed yet.
	if got := BufferStats().Reclaimed - base.Reclaimed; got != 0 {
		t.Fatalf("reclaimed %d bytes below mark", got)
	}
	if err := r.Commit(); err != nil {
		t.Fatal(err)
	}
	// Now the water mark is the cursor: blocks 0..3 reclaimed.
	if got := BufferStats().Reclaimed - base.Reclaimed; got != 4 {
		t.Fatalf("reclaimed %d bytes, want 4", got)
	}
	// A rollback now has no mark.
	if err := r.Rollback(); err != ErrNoMark {
		t.Fatalf("want ErrNoMark, got %v", err)
	}
}

// Zero-copy views span blocks and alias the same memory; released
// blocks are not accessible afterwards.
func TestZeroCopyViewAcrossBlocks(t *testing.T) {
	cfg := configs[0]
	w := NewWriter(cfg)
	_ = w.Write(0xCAFE, 16)
	_ = w.Write(0x42, 8)
	_ = w.Write(0x99, 8)
	raw := w.Bytes()
	c := &Chain{}
	c.AppendBytes(raw[:1], 8)
	c.AppendBytes(raw[1:2], 8)
	c.AppendBytes(raw[2:], 16)
	r := NewReader(cfg, c)
	v, err := r.View(24)
	if err != nil {
		t.Fatal(err)
	}
	vr := v.Reader(cfg)
	got, err := vr.Read(16)
	if err != nil || got != 0xCAFE {
		t.Fatalf("view read %#x err %v", got, err)
	}
	if got, _ = vr.Read(8); got != 0x42 {
		t.Fatalf("view tail %#x", got)
	}
	stale := v.Reader(cfg) // unconsumed sub-reader over the same blocks
	v.Close()

	// Once the view is closed and the reader advances past the pinned
	// blocks, they are reclaimed; the stale sub-reader must fail.
	v2, err := r.View(8) // consume the remaining block
	if err != nil {
		t.Fatal(err)
	}
	v2.Close()
	if _, err := stale.Read(1); err != ErrReleased {
		t.Fatalf("stale view: want ErrReleased, got %v", err)
	}
}

// Frame round trip: version, length and checksum are explicit; decode
// output is bounded by maxBits.
func TestFrameRoundTrip(t *testing.T) {
	for _, cfg := range configs {
		w := NewWriter(cfg)
		_ = w.Write(0x1ABC, 13)
		frame, err := Marshal(cfg, w.Bytes(), 13)
		if err != nil {
			t.Fatal(err)
		}
		gotCfg, payload, nbits, err := Unmarshal(frame, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if gotCfg != cfg || nbits != 13 {
			t.Fatalf("cfg %+v bits %d", gotCfg, nbits)
		}
		c := &Chain{}
		c.AppendBytes(payload, nbits)
		v, err := NewReader(cfg, c).Read(13)
		if err != nil || v != 0x1ABC {
			t.Fatalf("%s: %#x err %v", cfgName(cfg), v, err)
		}
		// Corrupt one payload bit: checksum must catch it.
		bad := append([]byte(nil), frame...)
		bad[frameHeader] ^= 0x01
		if _, _, _, err := Unmarshal(bad, 1<<20); err != ErrBadChecksum {
			t.Fatalf("want ErrBadChecksum, got %v", err)
		}
		// Wrong version.
		badV := append([]byte(nil), frame...)
		badV[4] = 99
		if _, _, _, err := Unmarshal(badV, 1<<20); err != ErrBadVersion {
			t.Fatalf("want ErrBadVersion, got %v", err)
		}
		// Quota on declared length.
		if _, _, _, err := Unmarshal(frame, 4); err != ErrQuotaExceeded {
			t.Fatalf("want ErrQuotaExceeded, got %v", err)
		}
		// Truncation.
		if _, _, _, err := Unmarshal(frame[:len(frame)-2], 1<<20); err == nil {
			t.Fatal("truncated frame accepted")
		}
	}
}

func ExampleReader() {
	cfg := Config{MSBFirst, BigEndian}
	w := NewWriter(cfg)
	_ = w.Write(0x1ABC, 13) // crosses a byte boundary
	_ = w.Write(0b101, 3)
	frame, _ := Marshal(cfg, w.Bytes(), w.Len())
	gotCfg, payload, nbits, _ := Unmarshal(frame, 1<<20)
	c := &Chain{}
	c.AppendBytes(payload, nbits)
	r := NewReader(gotCfg, c)
	peeked, _ := r.Peek(13)
	fmt.Printf("peek=%#04x pos=%d\n", peeked, r.Pos())
	v, _ := r.Read(13)
	fmt.Printf("read=%#04x pos=%d\n", v, r.Pos())
	// Output:
	// peek=0x1abc pos=0
	// read=0x1abc pos=13
}
