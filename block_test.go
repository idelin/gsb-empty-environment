package dod

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"math/rand"
	"testing"
)

func eqI64(a, b []int64) bool {
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

func roundTripBlock(t *testing.T, epoch uint64, ts, vs []int64) *Block {
	t.Helper()
	blk, err := encodeBlock(epoch, ts, vs)
	if err != nil {
		t.Fatalf("encodeBlock: %v", err)
	}
	got, n, err := decodeBlock(blk, 1<<20)
	if err != nil {
		t.Fatalf("decodeBlock: %v", err)
	}
	if n != len(blk) {
		t.Fatalf("consumed %d != encoded %d", n, len(blk))
	}
	if got.Epoch != epoch || !eqI64(got.Times, ts) || !eqI64(got.Values, vs) {
		t.Fatalf("round trip mismatch")
	}
	return got
}

// Constant-step series must have zero second-order deltas and tiny blocks.
func TestConstantStepSecondOrderZero(t *testing.T) {
	const n = 1000
	ts := make([]int64, n)
	vs := make([]int64, n)
	for i := range ts {
		ts[i] = int64(i) * 10
		vs[i] = 5*int64(i) + 3
	}
	// Verify the dods are all zero and the stream picks width 0.
	td := make([]int64, n-2)
	s := newStreamDP()
	for _, x := range td {
		s.push(zigzag(x))
	}
	segs := s.segments()
	if len(segs) != 1 || segs[0].width != 0 || segs[0].count != n-2 {
		t.Fatalf("constant step: want single width-0 segment, got %+v", segs)
	}
	blk, err := encodeBlock(0, ts, vs)
	if err != nil {
		t.Fatal(err)
	}
	if len(blk) > 48 {
		t.Fatalf("constant step block too large: %d bytes for %d points", len(blk), n)
	}
	roundTripBlock(t, 0, ts, vs)
}

// Negative deltas (values and irregular intervals) must decode cleanly,
// never flagged as corruption.
func TestNegativeDeltaRoundTrip(t *testing.T) {
	ts := []int64{100, 103, 107, 108, 250, 251, 900}
	vs := []int64{-5, -9, -100, -3, -4000, 7, -1}
	roundTripBlock(t, 3, ts, vs)
}

// Extreme deltas must not overflow: the encoder cuts a fresh anchor block.
func TestExtremeDeltasNoOverflow(t *testing.T) {
	// Non-decreasing times whose deltas overflow int64 (forces anchor cuts),
	// paired with extreme values exercising dod overflow on every step.
	ts := []int64{math.MinInt64, 0, math.MaxInt64, math.MaxInt64}
	vs := []int64{math.MinInt64, math.MaxInt64, math.MinInt64, 0}
	st := &MemStore{}
	a := NewArchive(st, AllowRegression)
	for i := range ts {
		if err := a.Add(ts[i], vs[i]); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if err := a.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	gotT, gotV, err := a.ReadRange(math.MinInt64, math.MaxInt64, 1<<20)
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if !eqI64(gotT, ts) || !eqI64(gotV, vs) {
		t.Fatalf("extreme round trip mismatch:\n got %v %v\nwant %v %v", gotT, gotV, ts, vs)
	}
}

// Dod overflow inside a block must surface as ErrOverflow from encodeBlock.
func TestEncodeBlockOverflowDetected(t *testing.T) {
	vs := []int64{0, math.MaxInt64, math.MinInt64} // dv0=Max, dod=Min-Max overflows
	ts := []int64{1, 2, 3}
	if _, err := encodeBlock(0, ts, vs); err != ErrOverflow {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
}

func TestChecksumDetectsCorruption(t *testing.T) {
	ts := []int64{1, 5, 9, 40}
	vs := []int64{7, -3, 100, 2}
	blk, err := encodeBlock(0, ts, vs)
	if err != nil {
		t.Fatal(err)
	}
	blk[len(blk)/2] ^= 0xFF
	if _, _, err := decodeBlock(blk, 1<<20); err != ErrChecksum {
		t.Fatalf("want ErrChecksum, got %v", err)
	}
}

func TestBoundedDecode(t *testing.T) {
	ts := make([]int64, 500)
	vs := make([]int64, 500)
	for i := range ts {
		ts[i] = int64(i)
	}
	blk, err := encodeBlock(0, ts, vs)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeBlock(blk, 100); err != ErrTooLarge {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestVersionRejected(t *testing.T) {
	ts := []int64{1, 2, 3}
	vs := []int64{4, 5, 6}
	blk, err := encodeBlock(0, ts, vs)
	if err != nil {
		t.Fatal(err)
	}
	_, k := binary.Uvarint(blk)
	body := blk[k : len(blk)-4]
	body[0] = 99 // bump version, fix crc so only version check fails
	binary.LittleEndian.PutUint32(blk[len(blk)-4:], crc32.ChecksumIEEE(body))
	if _, _, err := decodeBlock(blk, 1<<20); err != ErrVersion {
		t.Fatalf("want ErrVersion, got %v", err)
	}
}

// The analytic block size model must match encodeBlock byte-for-byte;
// this is what makes the boundary DP an exact joint optimization.
func TestBlockSizeMatchesEncode(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(40)
		ts := make([]int64, n)
		vs := make([]int64, n)
		ts[0] = rng.Int63n(1 << 40)
		vs[0] = rng.Int63n(1<<40) - 1<<39
		for i := 1; i < n; i++ {
			ts[i] = ts[i-1] + rng.Int63n(1000)
			vs[i] = vs[i-1] + rng.Int63n(2001) - 1000
		}
		epoch := uint64(rng.Intn(5))
		st, sv := newStreamDP(), newStreamDP()
		var dt0, dv0, dTp, dVp int64
		for j := 1; j <= n; j++ {
			m := j
			if m >= 2 {
				dt, _ := checkedSub(ts[m-1], ts[m-2])
				dv, _ := checkedSub(vs[m-1], vs[m-2])
				if m == 2 {
					dt0, dv0 = dt, dv
				} else {
					ddt, _ := checkedSub(dt, dTp)
					ddv, _ := checkedSub(dv, dVp)
					st.push(zigzag(ddt))
					sv.push(zigzag(ddv))
				}
				dTp, dVp = dt, dv
			}
			want, err := encodeBlock(epoch, ts[:m], vs[:m])
			if err != nil {
				t.Fatal(err)
			}
			got := blockSize(epoch, m, ts[0], vs[0], dt0, dv0, st, sv)
			if got != len(want) {
				t.Fatalf("trial %d prefix %d: model=%d actual=%d", trial, m, got, len(want))
			}
		}
	}
}
