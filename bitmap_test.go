package bitmap

import (
	"encoding/binary"
	"hash/crc32"
	"math/bits"
	"math/rand"
	"sort"
	"testing"
)

// checkCanonical verifies the structural invariants of the normalized form.
func checkCanonical(t *testing.T, b *Bitmap) {
	t.Helper()
	sum := 0
	for i, key := range b.keys {
		if i > 0 && key <= b.keys[i-1] {
			t.Fatalf("keys not strictly increasing at %d", i)
		}
		c := b.containers[i]
		if c.cardinality() == 0 {
			t.Fatalf("empty container at key %d", key)
		}
		switch ct := c.(type) {
		case *arrayContainer:
			if len(ct.values) > b.threshold {
				t.Fatalf("array container over threshold: %d > %d", len(ct.values), b.threshold)
			}
			for j := 1; j < len(ct.values); j++ {
				if ct.values[j] <= ct.values[j-1] {
					t.Fatalf("array container not sorted/deduped")
				}
			}
		case *bitmapContainer:
			if ct.card <= b.threshold {
				t.Fatalf("bitmap container under threshold: %d <= %d", ct.card, b.threshold)
			}
			n := 0
			for _, w := range ct.bits {
				n += bits.OnesCount64(w)
			}
			if n != ct.card {
				t.Fatalf("stale bitmap cardinality cache: got %d, want %d", ct.card, n)
			}
		}
		sum += c.cardinality()
	}
	if b.cardValid && b.card != sum {
		t.Fatalf("stale bitmap-level cache: got %d, want %d", b.card, sum)
	}
	if got := b.Cardinality(); got != sum {
		t.Fatalf("Cardinality() = %d, want %d", got, sum)
	}
}

func modelSlice(m map[uint32]bool) []uint32 {
	out := make([]uint32, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func checkModel(t *testing.T, b *Bitmap, m map[uint32]bool) {
	t.Helper()
	got := b.ToArray()
	want := modelSlice(m)
	if len(got) != len(want) {
		t.Fatalf("cardinality mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("value mismatch at %d: got %d, want %d", i, got[i], want[i])
		}
	}
	for _, v := range want {
		if !b.Contains(v) {
			t.Fatalf("Contains(%d) = false", v)
		}
	}
	checkCanonical(t, b)
}

// TestModelOps drives Add/Remove/Flip against a plain map model with a tiny
// threshold so containers constantly cross between array and bitmap forms.
func TestModelOps(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const threshold = 8
	b := New(WithArrayMaxSize(threshold))
	model := map[uint32]bool{}
	pick := func() uint32 {
		// Concentrate values in a few chunks to force dense containers.
		return uint32(rng.Intn(3))<<16 | uint32(rng.Intn(64))
	}
	for step := 0; step < 5000; step++ {
		switch rng.Intn(5) {
		case 0, 1:
			x := pick()
			if b.Add(x) != !model[x] {
				t.Fatalf("Add(%d) return mismatch", x)
			}
			model[x] = true
		case 2:
			x := pick()
			if b.Remove(x) != model[x] {
				t.Fatalf("Remove(%d) return mismatch", x)
			}
			delete(model, x)
		case 3: // flip a small range, sometimes crossing chunk boundaries
			lo := uint64(pick()) - 3
			hi := lo + uint64(rng.Intn(40))
			b.Flip(lo, hi)
			for v := lo; v < hi; v++ {
				x := uint32(v)
				if model[x] {
					delete(model, x)
				} else {
					model[x] = true
				}
			}
		case 4: // duplicate adds/removes to exercise no-op paths
			x := pick()
			b.Add(x)
			model[x] = true
			b.Add(x)
		}
		if step%97 == 0 {
			checkModel(t, b, model)
		}
	}
	checkModel(t, b, model)
}

// TestModelSetOps checks And/Or/AndNot against the model.
func TestModelSetOps(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	const threshold = 16
	build := func() (*Bitmap, map[uint32]bool) {
		b := New(WithArrayMaxSize(threshold))
		m := map[uint32]bool{}
		for i := 0; i < 300; i++ {
			x := uint32(rng.Intn(4))<<16 | uint32(rng.Intn(128))
			b.Add(x)
			m[x] = true
		}
		return b, m
	}
	a, ma := build()
	c, mc := build()

	and := a.And(c)
	or := a.Or(c)
	diff := a.AndNot(c)
	mAnd, mOr, mDiff := map[uint32]bool{}, map[uint32]bool{}, map[uint32]bool{}
	for v := range ma {
		mOr[v] = true
		if mc[v] {
			mAnd[v] = true
		} else {
			mDiff[v] = true
		}
	}
	for v := range mc {
		mOr[v] = true
	}
	checkModel(t, and, mAnd)
	checkModel(t, or, mOr)
	checkModel(t, diff, mDiff)

	// Chained ops must stay canonical too.
	checkModel(t, or.AndNot(a), map[uint32]bool(func() map[uint32]bool {
		out := map[uint32]bool{}
		for v := range mc {
			if !ma[v] {
				out[v] = true
			}
		}
		return out
	}()))
}

// TestThresholdCrossing explicitly walks a container across the threshold in
// both directions and checks the representation each step of the way.
func TestThresholdCrossing(t *testing.T) {
	const threshold = 10
	b := New(WithArrayMaxSize(threshold))
	for i := uint32(0); i < threshold*3; i++ {
		b.Add(i << 3)
		wantArray := int(i)+1 <= threshold
		_, isArray := b.containers[0].(*arrayContainer)
		if isArray != wantArray {
			t.Fatalf("after %d adds: array=%v, want %v", i+1, isArray, wantArray)
		}
		if b.Cardinality() != int(i)+1 {
			t.Fatalf("cardinality = %d, want %d", b.Cardinality(), i+1)
		}
	}
	for i := uint32(threshold * 3); i > 0; i-- {
		b.Remove((i - 1) << 3)
		wantArray := int(i)-1 <= threshold && i-1 > 0
		if i-1 == 0 {
			if !b.IsEmpty() {
				t.Fatal("expected empty bitmap")
			}
			continue
		}
		_, isArray := b.containers[0].(*arrayContainer)
		if isArray != wantArray {
			t.Fatalf("down to %d: array=%v, want %v", i-1, isArray, wantArray)
		}
		if b.Cardinality() != int(i)-1 {
			t.Fatalf("cardinality = %d, want %d", b.Cardinality(), i-1)
		}
	}
}

// TestFlipModel flips ranges that straddle chunk boundaries.
func TestFlipModel(t *testing.T) {
	b := New(WithArrayMaxSize(4))
	model := map[uint32]bool{}
	ranges := [][2]uint64{{0, 10}, {5, 1<<16 + 5}, {1<<16 - 2, 2<<16 + 2}, {100, 100}, {3, 7}}
	for _, r := range ranges {
		b.Flip(r[0], r[1])
		for v := r[0]; v < r[1]; v++ {
			x := uint32(v)
			if model[x] {
				delete(model, x)
			} else {
				model[x] = true
			}
		}
		checkModel(t, b, model)
	}
}

func roundTrip(t *testing.T, b *Bitmap) *Bitmap {
	t.Helper()
	data := b.MarshalBinary()
	out, err := UnmarshalBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if !out.Equals(b) {
		t.Fatal("round trip mismatch")
	}
	if out.threshold != b.threshold {
		t.Fatalf("threshold not preserved: got %d, want %d", out.threshold, b.threshold)
	}
	checkCanonical(t, out)
	return out
}

func TestSerializeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	b := New(WithArrayMaxSize(32))
	for i := 0; i < 2000; i++ {
		b.Add(uint32(rng.Intn(5))<<16 | uint32(rng.Intn(200)))
	}
	roundTrip(t, b)
	roundTrip(t, New())
}

// TestUntrustedCardinality writes a lying cardinality hint (with a valid
// checksum) and verifies the reader recomputes from the payload instead.
func TestUntrustedCardinality(t *testing.T) {
	b := New(WithArrayMaxSize(4))
	for i := uint32(0); i < 20; i++ {
		b.Add(i * 2) // dense enough to be a bitmap container
	}
	data := b.MarshalBinary()
	// First container's stored card sits right after magic(4)+version(2)+
	// threshold(4)+count(4)+key(2)+type(1).
	const cardOff = 4 + 2 + 4 + 4 + 2 + 1
	binary.LittleEndian.PutUint32(data[cardOff:], 9999)
	binary.LittleEndian.PutUint32(data[len(data)-4:], crc32.ChecksumIEEE(data[:len(data)-4]))

	out, err := UnmarshalBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if out.Cardinality() != 20 {
		t.Fatalf("trusted stored cardinality: got %d, want 20", out.Cardinality())
	}
	if !out.Equals(b) {
		t.Fatal("content mismatch after ignoring stored cardinality")
	}
}

// TestCorruptRejection feeds damaged files to the decoder.
func TestCorruptRejection(t *testing.T) {
	b := New(WithArrayMaxSize(4))
	for i := uint32(0); i < 50; i++ {
		b.Add(i)
	}
	good := b.MarshalBinary()

	cases := map[string]func([]byte) []byte{
		"empty":     func(d []byte) []byte { return nil },
		"truncated": func(d []byte) []byte { return d[:len(d)-10] },
		"bad magic": func(d []byte) []byte {
			d[0] = 'X'
			binary.LittleEndian.PutUint32(d[len(d)-4:], crc32.ChecksumIEEE(d[:len(d)-4]))
			return d
		},
		"bad version": func(d []byte) []byte {
			binary.LittleEndian.PutUint16(d[4:6], 99)
			binary.LittleEndian.PutUint32(d[len(d)-4:], crc32.ChecksumIEEE(d[:len(d)-4]))
			return d
		},
		"bit flip payload": func(d []byte) []byte {
			d[20] ^= 0xFF // checksum now stale
			return d
		},
		"bad container type": func(d []byte) []byte {
			d[4+2+4+4+2] = 7
			binary.LittleEndian.PutUint32(d[len(d)-4:], crc32.ChecksumIEEE(d[:len(d)-4]))
			return d
		},
		"unsorted array payload": func(d []byte) []byte {
			// Swap the first two array values of a fresh small bitmap.
			small := New(WithArrayMaxSize(64))
			small.Add(1)
			small.Add(2)
			d2 := small.MarshalBinary()
			off := 4 + 2 + 4 + 4 + 2 + 1 + 4
			binary.LittleEndian.PutUint16(d2[off:], 2)
			binary.LittleEndian.PutUint16(d2[off+2:], 1)
			binary.LittleEndian.PutUint32(d2[len(d2)-4:], crc32.ChecksumIEEE(d2[:len(d2)-4]))
			return d2
		},
		"trailing garbage": func(d []byte) []byte {
			body := append(d[:len(d)-4], 0xAB)
			return binary.LittleEndian.AppendUint32(body, crc32.ChecksumIEEE(body))
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := UnmarshalBinary(corrupt(append([]byte(nil), good...))); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
