package eliasfano

import (
	"encoding/binary"
	"hash/crc64"
	"math"
	"math/rand"
	"sort"
	"testing"
)

// --- independent array reference ---

func refLowerBound(arr []uint64, v uint64) uint64 {
	return uint64(sort.Search(len(arr), func(i int) bool { return arr[i] >= v }))
}

func randSeq(rng *rand.Rand, n, universe uint64) []uint64 {
	arr := make([]uint64, n)
	for i := range arr {
		arr[i] = rng.Uint64() % universe
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i] < arr[j] })
	return arr
}

func buildFrom(t *testing.T, arr []uint64, universe uint64) *EliasFano {
	t.Helper()
	ef, err := New(arr, universe)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := ef.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return ef
}

func TestAccessAndLowerBoundAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cases := []struct {
		n, universe uint64
	}{
		{1, 1},
		{2, 2},
		{10, 5}, // max value smaller than count -> l = 0
		{100, 8},
		{257, 1 << 12},
		{1000, 1 << 20},
		{500, 1 << 62},
		{10000, 1 << 30},
		{2048, math.MaxUint64}, // full-width universe
	}
	for _, tc := range cases {
		arr := randSeq(rng, tc.n, tc.universe)
		ef := buildFrom(t, arr, tc.universe)
		for i := uint64(0); i < ef.N(); i++ {
			if got := ef.Access(i); got != arr[i] {
				t.Fatalf("Access(%d)=%d want %d (n=%d U=%d)", i, got, arr[i], tc.n, tc.universe)
			}
		}
		queries := []uint64{0, 1, tc.universe - 1, tc.universe, tc.universe + 1, math.MaxUint64}
		if arr[0] > 0 {
			queries = append(queries, arr[0]-1)
		}
		queries = append(queries, arr[0], arr[tc.n-1])
		if arr[tc.n-1] < math.MaxUint64 {
			queries = append(queries, arr[tc.n-1]+1)
		}
		for k := 0; k < 200; k++ {
			queries = append(queries, rng.Uint64())
		}
		for _, q := range queries {
			want := refLowerBound(arr, q)
			if got := ef.LowerBound(q); got != want {
				t.Fatalf("LowerBound(%d)=%d want %d (n=%d U=%d)", q, got, want, tc.n, tc.universe)
			}
			var cwant uint64
			for _, v := range arr {
				if v == q {
					cwant++
				}
			}
			if got := ef.Count(q); got != cwant {
				t.Fatalf("Count(%d)=%d want %d (n=%d U=%d)", q, got, cwant, tc.n, tc.universe)
			}
		}
	}
}

func TestExactRanksWithDuplicates(t *testing.T) {
	arr := []uint64{2, 2, 2, 5, 5, 9}
	ef := buildFrom(t, arr, 16)
	wantLB := map[uint64]uint64{
		0: 0, 1: 0, 2: 0, 3: 3, 4: 3, 5: 3, 6: 5, 8: 5, 9: 5, 10: 6, 15: 6, 16: 6,
	}
	for v, want := range wantLB {
		if got := ef.LowerBound(v); got != want {
			t.Fatalf("LowerBound(%d)=%d want %d", v, got, want)
		}
	}
	if ef.Count(2) != 3 || ef.Count(5) != 2 || ef.Count(9) != 1 || ef.Count(7) != 0 {
		t.Fatalf("duplicate multiplicities wrong: %d %d %d %d",
			ef.Count(2), ef.Count(5), ef.Count(9), ef.Count(7))
	}
	if ef.LowerBound(math.MaxUint64) != 6 {
		t.Fatal("LowerBound above last value must return n")
	}
}

func TestAllSameValuesZeroLowBits(t *testing.T) {
	const n = uint64(1000)
	bld := NewBuilder(n, 6) // U < n -> l must be 0, no division by zero
	for i := uint64(0); i < n; i++ {
		if err := bld.Add(5); err != nil {
			t.Fatal(err)
		}
	}
	ef := bld.MustBuild()
	if ef.LowBits() != 0 || len(ef.low) != 0 {
		t.Fatalf("expected zero low bits, got l=%d lowWords=%d", ef.LowBits(), len(ef.low))
	}
	if ef.Access(0) != 5 || ef.Access(n-1) != 5 {
		t.Fatal("Access on all-same sequence wrong")
	}
	if ef.LowerBound(5) != 0 || ef.LowerBound(6) != n || ef.Count(5) != n {
		t.Fatal("ranks on all-same sequence wrong")
	}
	if err := ef.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEmptySet(t *testing.T) {
	for _, u := range []uint64{0, 1, 1 << 40} {
		ef := NewBuilder(0, u).MustBuild()
		if ef.N() != 0 || ef.LowerBound(0) != 0 || ef.LowerBound(math.MaxUint64) != 0 {
			t.Fatal("empty set queries must return 0")
		}
		if _, ok := ef.Iter().Next(); ok {
			t.Fatal("empty iterator must be exhausted")
		}
		data := ef.Marshal()
		if len(data) >= 40 {
			t.Fatalf("empty set must be tiny, got %d bytes", len(data))
		}
		back, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		if back.N() != 0 || back.LowerBound(123) != 0 {
			t.Fatal("round-tripped empty set wrong")
		}
	}
}

func TestHugeUniverseBoundedMemory(t *testing.T) {
	const n = uint64(4)
	bld := NewBuilder(n, 1<<62)
	for _, v := range []uint64{0, 1 << 40, 1 << 61, (1 << 62) - 1} {
		if err := bld.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	ef := bld.MustBuild()
	if got := ef.SizeBytes(); got >= 128 {
		t.Fatalf("huge universe must not cause huge allocation: %d bytes", got)
	}
	for i, v := range []uint64{0, 1 << 40, 1 << 61, (1 << 62) - 1} {
		if ef.Access(uint64(i)) != v {
			t.Fatalf("Access(%d) wrong under huge U", i)
		}
	}
	if r := ef.LowerBound((1 << 61) + 1); r != 3 {
		t.Fatalf("LowerBound huge-U: %d want 3", r)
	}
	if r := ef.LowerBound(1 << 62); r != n {
		t.Fatalf("LowerBound at universe must be n, got %d", r)
	}
}

func TestBuilderRejectsBadStreams(t *testing.T) {
	b := NewBuilder(3, 10)
	if err := b.Add(1); err != nil {
		t.Fatal(err)
	}
	if err := b.Add(0); err == nil {
		t.Fatal("decreasing value must be rejected")
	}
	b2 := NewBuilder(2, 10)
	_ = b2.Add(1)
	if err := b2.Add(10); err == nil {
		t.Fatal("value >= universe must be rejected")
	}
	b3 := NewBuilder(2, 10)
	_ = b3.Add(1)
	if _, err := b3.Build(); err == nil {
		t.Fatal("truncated stream must fail Build")
	}
	b4 := NewBuilder(1, 10)
	_ = b4.Add(1)
	if err := b4.Add(2); err == nil {
		t.Fatal("extra value must be rejected")
	}
	b5 := NewBuilder(1, 0)
	if err := b5.Add(0); err == nil {
		t.Fatal("value in empty universe must be rejected")
	}
}

func TestMergeAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	cases := []struct {
		na, ua, nb, ub uint64
	}{
		{0, 0, 0, 0},
		{0, 0, 100, 64},
		{100, 64, 0, 0},
		{200, 1000, 150, 1 << 20}, // different l values
		{500, 16, 500, 16},        // heavy duplicates
		{1000, 1 << 40, 777, 1 << 62},
	}
	for _, tc := range cases {
		var a, b []uint64
		var ea, eb *EliasFano
		if tc.na > 0 {
			a = randSeq(rng, tc.na, tc.ua)
			ea = buildFrom(t, a, tc.ua)
		} else {
			ea = NewBuilder(0, tc.ua).MustBuild()
		}
		if tc.nb > 0 {
			b = randSeq(rng, tc.nb, tc.ub)
			eb = buildFrom(t, b, tc.ub)
		} else {
			eb = NewBuilder(0, tc.ub).MustBuild()
		}
		merged := Merge(ea, eb)
		ref := append(append([]uint64{}, a...), b...)
		sort.Slice(ref, func(i, j int) bool { return ref[i] < ref[j] })
		if merged.N() != uint64(len(ref)) {
			t.Fatalf("merge n=%d want %d", merged.N(), len(ref))
		}
		for i, v := range ref {
			if merged.Access(uint64(i)) != v {
				t.Fatalf("merged Access(%d)=%d want %d", i, merged.Access(uint64(i)), v)
			}
		}
		it := merged.Iter()
		for i, want := range ref {
			got, ok := it.Next()
			if !ok || got != want {
				t.Fatalf("merged iterator[%d] got %d ok=%v want %d", i, got, ok, want)
			}
		}
		if _, ok := it.Next(); ok {
			t.Fatal("merge iterator not exhausted")
		}
		if err := merged.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSerializeRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for _, tc := range []struct{ n, u uint64 }{
		{0, 0}, {1, 1}, {100, 6}, {333, 1 << 20}, {2000, 1 << 50},
	} {
		arr := randSeq(rng, tc.n, tc.u)
		ef := buildFrom(t, arr, tc.u)
		data := ef.Marshal()
		if len(data) != ef.SizeBytes() {
			t.Fatalf("SizeBytes %d != serialized %d", ef.SizeBytes(), len(data))
		}
		back, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		if back.Layout() != ef.Layout() {
			t.Fatalf("layout mismatch: %+v vs %+v", back.Layout(), ef.Layout())
		}
		for i := uint64(0); i < tc.n; i++ {
			if back.Access(i) != arr[i] {
				t.Fatalf("round-trip Access(%d) wrong", i)
			}
		}
	}
}

func TestRandomBitMutationDetected(t *testing.T) {
	ebase := randSeq(rand.New(rand.NewSource(5)), 2000, 1<<24)
	ef := buildFrom(t, ebase, 1<<24)
	orig := ef.Marshal()
	rng := rand.New(rand.NewSource(123))
	for iter := 0; iter < 1000; iter++ {
		bad := make([]byte, len(orig))
		copy(bad, orig)
		off := rng.Intn(len(bad))
		bad[off] ^= 1 << uint(rng.Intn(8)) // single random bit flip
		if _, err := Parse(bad); err == nil {
			t.Fatalf("iteration %d: flipped bit at byte %d was not detected", iter, off)
		}
	}
	if _, err := Parse(orig[:len(orig)-1]); err == nil {
		t.Fatal("truncated blob accepted")
	}
	garbage := make([]byte, len(orig)+1)
	copy(garbage, orig)
	if _, err := Parse(garbage); err == nil {
		t.Fatal("blob with trailing garbage accepted")
	}
}

// A blob with a *valid checksum* but absurd header claims must be
// rejected by structural validation before any payload allocation.
func TestForgedHeaderCannotCauseHugeAllocation(t *testing.T) {
	forge := func(n, universe uint64) []byte {
		body := append([]byte{}, magic[:]...)
		body = binary.AppendUvarint(body, n)
		body = binary.AppendUvarint(body, universe)
		l := uint64(0)
		if n > 0 && universe > n {
			l = uint64(63 - nlz64(universe/n))
		}
		body = binary.AppendUvarint(body, l)
		body = binary.AppendUvarint(body, uint64(1)<<40) // numBuckets lie
		body = binary.AppendUvarint(body, uint64(1)<<40) // lowWords lie
		body = binary.AppendUvarint(body, uint64(1)<<40) // highWords lie
		var crc [8]byte
		binary.LittleEndian.PutUint64(crc[:], crc64.Checksum(body, crcTable))
		return append(body, crc[:]...)
	}
	for _, blob := range [][]byte{
		forge(1<<60, 1<<62), // absurd n
		forge(3, 64),        // plausible n, absurd word counts
	} {
		if _, err := Parse(blob); err == nil {
			t.Fatal("forged header accepted")
		}
	}
}

func nlz64(v uint64) uint64 {
	n := uint64(0)
	for i := 63; i >= 0; i-- {
		if v&(1<<uint(i)) != 0 {
			return n
		}
		n++
	}
	return 64
}

func TestByteAccounting(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for _, tc := range []struct{ n, u uint64 }{
		{0, 0}, {1, 1}, {50, 6}, {1000, 1 << 20}, {10000, 1 << 40},
	} {
		var ef *EliasFano
		if tc.n == 0 {
			ef = NewBuilder(0, tc.u).MustBuild()
		} else {
			ef = buildFrom(t, randSeq(rng, tc.n, tc.u), tc.u)
		}
		lay := ef.Layout()
		// Exact formula: magic + 6 uvarints + payload words + CRC64.
		header := 8 + uvarintLen(lay.N) + uvarintLen(lay.Universe) +
			uvarintLen(lay.LowBits) + uvarintLen(lay.NumBuckets) +
			uvarintLen(uint64(lay.LowWords)) + uvarintLen(uint64(lay.HighWords))
		want := header + 8*(lay.LowWords+lay.HighWords) + 8
		if ef.SizeBytes() != want {
			t.Fatalf("SizeBytes=%d want formula %d", ef.SizeBytes(), want)
		}
		if len(ef.Marshal()) != want {
			t.Fatalf("Marshal length %d != formula %d", len(ef.Marshal()), want)
		}
		// Space bound from the parameter derivation:
		// payload bits <= n*l + (n + numBuckets) <= n*(l+3).
		payloadBits := uint64(8 * lay.PayloadBytes)
		bound := tc.n*(lay.LowBits+3) + 64 // +64: word rounding
		if payloadBits > bound {
			t.Fatalf("payload %d bits exceeds derived bound %d (n=%d U=%d l=%d)",
				payloadBits, bound, tc.n, tc.u, lay.LowBits)
		}
		// numBuckets <= 2n always; >= n when the universe exceeds n.
		if tc.n > 0 && tc.u > 0 {
			if lay.NumBuckets > 2*tc.n {
				t.Fatalf("numBuckets=%d exceeds 2n for n=%d", lay.NumBuckets, tc.n)
			}
			if tc.u > tc.n && lay.NumBuckets < tc.n {
				t.Fatalf("numBuckets=%d below n=%d though U>n", lay.NumBuckets, tc.n)
			}
		}
	}
}
