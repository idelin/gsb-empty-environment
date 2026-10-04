package idset

import (
	"math/rand"
	"testing"
)

func arrayContainer(vals ...uint16) *container {
	c := &container{typ: cArray}
	for _, v := range vals {
		c, _ = c.add(v)
	}
	return c
}

func bitmapContainer(vals ...uint16) *container {
	c := &container{typ: cBitmap, bm: new([bitmapWords]uint64)}
	for _, v := range vals {
		nc, delta := c.add(v)
		c = nc
		_ = delta
	}
	return c
}

func runContainer(ivs ...ival) *container {
	card := 0
	for _, iv := range ivs {
		card += int(iv.hi) - int(iv.lo) + 1
	}
	return &container{typ: cRun, card: card, runs: append([]ival(nil), ivs...)}
}

func containerValues(c *container) map[uint16]bool {
	out := map[uint16]bool{}
	it := c.iter()
	for v, ok := it.next(); ok; v, ok = it.next() {
		out[v] = true
	}
	return out
}

// TestOpsAllContainerCombos exercises AND/OR/XOR for all 3x3 container
// type pairs against a brute-force reference.
func TestOpsAllContainerCombos(t *testing.T) {
	sparse := []uint16{0, 3, 7, 100, 101, 500, 4096, 10000, 40000, 65535}
	var dense []uint16 // 5000 evens: canonical bitmap
	for i := 0; i < 10000; i += 2 {
		dense = append(dense, uint16(i))
	}
	runs1 := []ival{{0, 9}, {100, 101}, {500, 600}, {4096, 4096}, {9998, 10002}, {40000, 40010}, {65530, 65535}}
	runs2 := []ival{{5, 12}, {101, 103}, {550, 700}, {9999, 10001}, {65535, 65535}}

	mk := func(typ ctype, second bool) *container {
		switch typ {
		case cArray:
			if second {
				return arrayContainer(1, 3, 8, 101, 102, 600, 4096, 9999, 40001, 65534)
			}
			return arrayContainer(sparse...)
		case cBitmap:
			if second {
				var odd []uint16
				for i := 1; i < 10001; i += 2 {
					odd = append(odd, uint16(i))
				}
				return bitmapContainer(odd...)
			}
			return bitmapContainer(dense...)
		default:
			if second {
				return runContainer(runs2...)
			}
			return runContainer(runs1...)
		}
	}

	types := []ctype{cArray, cBitmap, cRun}
	names := map[ctype]string{cArray: "array", cBitmap: "bitmap", cRun: "run"}
	ops := []struct {
		name string
		op   setOp
		f    func(a, b map[uint16]bool) map[uint16]bool
	}{
		{"AND", opAnd, func(a, b map[uint16]bool) map[uint16]bool {
			out := map[uint16]bool{}
			for v := range a {
				if b[v] {
					out[v] = true
				}
			}
			return out
		}},
		{"OR", opOr, func(a, b map[uint16]bool) map[uint16]bool {
			out := map[uint16]bool{}
			for v := range a {
				out[v] = true
			}
			for v := range b {
				out[v] = true
			}
			return out
		}},
		{"XOR", opXor, func(a, b map[uint16]bool) map[uint16]bool {
			out := map[uint16]bool{}
			for v := range a {
				if !b[v] {
					out[v] = true
				}
			}
			for v := range b {
				if !a[v] {
					out[v] = true
				}
			}
			return out
		}},
	}
	for _, t1 := range types {
		for _, t2 := range types {
			for _, o := range ops {
				a, b := mk(t1, false), mk(t2, true)
				got := combine(a, b, o.op)
				want := o.f(containerValues(a), containerValues(b))
				gotVals := map[uint16]bool{}
				if got != nil {
					if got.card != len(containerValues(got)) {
						t.Fatalf("%s %s-%s: card mismatch", o.name, names[t1], names[t2])
					}
					gotVals = containerValues(got)
				}
				if len(gotVals) != len(want) {
					t.Fatalf("%s %s-%s: got %d values, want %d", o.name, names[t1], names[t2], len(gotVals), len(want))
				}
				for v := range want {
					if !gotVals[v] {
						t.Fatalf("%s %s-%s: missing %d", o.name, names[t1], names[t2], v)
					}
				}
			}
		}
	}
}

func TestOpsSetLevel(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 20; trial++ {
		r1 := randRef(rng, 3000, 0.8)
		r2 := randRef(rng, 3000, 0.8)
		s1, s2 := setFromRef(r1), setFromRef(r2)
		s1.Optimize()
		s2.Optimize()
		probes := []uint32{0, 65535, 65536, 1<<32 - 1, rng.Uint32(), rng.Uint32()}
		checkEqual(t, s1.And(s2), r1.and(r2), probes...)
		checkEqual(t, s1.Or(s2), r1.or(r2), probes...)
		checkEqual(t, s1.Xor(s2), r1.xor(r2), probes...)
		// Operands must be unchanged.
		checkEqual(t, s1, r1, probes...)
		checkEqual(t, s2, r2, probes...)
	}
}

func TestOpsEmptyResults(t *testing.T) {
	a := New()
	b := New()
	for i := 0; i < 100; i++ {
		a.Add(uint32(i))
		b.Add(uint32(100 + i))
	}
	if got := a.And(b); got.Size() != 0 || len(got.keys) != 0 {
		t.Fatalf("disjoint AND: size=%d blocks=%v", got.Size(), got.keys)
	}
	if got := a.Xor(a); got.Size() != 0 {
		t.Fatalf("self XOR: size=%d", got.Size())
	}
	full := New()
	for i := 0; i < 65536; i++ {
		full.Add(uint32(i))
	}
	if got := full.And(a); got.Size() != 100 {
		t.Fatalf("AND with full block: size=%d", got.Size())
	}
	if got := full.Xor(full); got.Size() != 0 {
		t.Fatalf("full XOR full: size=%d", got.Size())
	}
}

// TestConversionTies pins the fixed tie-break priority array > run > bitmap.
func TestConversionTies(t *testing.T) {
	// array vs bitmap tie: 4096 elements -> 8192 == 8192, array wins.
	var vals []uint16
	for i := 0; i < 8192; i += 2 {
		vals = append(vals, uint16(i))
	}
	c := bitmapContainer(vals...).canonical()
	if c.typ != cArray {
		t.Fatalf("4096-element tie: got type %d, want array", c.typ)
	}
	// array vs run tie: runs of length 2 -> 2*card == 4*nruns, array wins.
	var ivs []ival
	for i := 0; i < 10; i++ {
		ivs = append(ivs, ival{uint16(i * 4), uint16(i*4 + 1)})
	}
	c = runContainer(ivs...).canonical()
	if c.typ != cArray {
		t.Fatalf("length-2 runs tie: got type %d, want array", c.typ)
	}
	// run vs bitmap tie: 2048 runs of length 3 -> 8192 == 8192, run wins.
	ivs = ivs[:0]
	for i := 0; i < 2048; i++ {
		ivs = append(ivs, ival{uint16(i * 8), uint16(i*8 + 2)})
	}
	c = runContainer(ivs...).canonical()
	if c.typ != cRun {
		t.Fatalf("2048-run tie: got type %d, want run", c.typ)
	}
	// bitmap strictly wins: 5000 alternating elements (5000 runs).
	vals = vals[:0]
	for i := 0; i < 10000; i += 2 {
		vals = append(vals, uint16(i))
	}
	c = bitmapContainer(vals...).canonical()
	if c.typ != cBitmap {
		t.Fatalf("dense scattered: got type %d, want bitmap", c.typ)
	}
	// run strictly wins: one long interval.
	c = runContainer(ival{0, 65535}).canonical()
	if c.typ != cRun {
		t.Fatalf("full range: got type %d, want run", c.typ)
	}
}

// TestLazyThenCanonicalEncoding: updates may leave containers
// non-canonical, but Encode must produce the unique canonical bytes.
func TestLazyThenCanonicalEncoding(t *testing.T) {
	s := New()
	// 5000 scattered values stay a (non-canonical) array after adds.
	for i := 0; i < 10000; i += 2 {
		s.Add(uint32(i))
	}
	if s.cons[0].typ != cArray {
		t.Fatalf("expected lazy array, got %d", s.cons[0].typ)
	}
	enc := s.Encode()
	dec, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec.cons[0].typ != cBitmap {
		t.Fatalf("decoded type = %d, want bitmap", dec.cons[0].typ)
	}
	if enc2 := dec.Encode(); string(enc2) != string(enc) {
		t.Fatal("re-encode differs: encoding not canonical/unique")
	}
	// Same content built via a different path encodes identically.
	s2 := New()
	s2.Add(0)
	for i := 0; i < 10000; i += 2 {
		s2.Remove(0)
		s2.Add(uint32(i))
		s2.Add(0)
	}
	if enc3 := s2.Encode(); string(enc3) != string(enc) {
		t.Fatal("equal content produced different encodings")
	}
}
