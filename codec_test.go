package erasure

import (
	"bytes"
	"math/rand"
	"testing"
)

func sampleData(n int) []byte {
	r := rand.New(rand.NewSource(42))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Intn(256))
	}
	return b
}

func marshalAll(blocks []*Block) [][]byte {
	raw := make([][]byte, len(blocks))
	for i, b := range blocks {
		raw[i] = b.Marshal()
	}
	return raw
}

func TestGFArithmetic(t *testing.T) {
	for a := 1; a < 256; a++ {
		for b := 1; b < 256; b++ {
			x, y := byte(a), byte(b)
			if got := gfDiv(gfMul(x, y), y); got != x {
				t.Fatalf("div(mul(%d,%d),%d) = %d", x, y, y, got)
			}
			if got := gfMul(x, gfInv(x)); got != 1 {
				t.Fatalf("mul(%d, inv(%d)) = %d", x, x, got)
			}
		}
	}
	if gfMul(0, 5) != 0 || gfMul(5, 0) != 0 {
		t.Fatal("multiplication by zero must be zero")
	}
}

func TestMatrixInvert(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 50; trial++ {
		n := 1 + r.Intn(8)
		m := make([][]byte, n)
		for i := range m {
			m[i] = make([]byte, n)
			for j := range m[i] {
				m[i][j] = byte(r.Intn(256))
			}
		}
		orig := make([][]byte, n)
		for i := range m {
			orig[i] = make([]byte, n)
			copy(orig[i], m[i])
		}
		inv, err := gfMatrixInvert(m) // note: inverts in place, m is consumed
		if err == errSingular {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		// verify orig * inv == I
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				var acc byte
				for c := 0; c < n; c++ {
					acc ^= gfMul(orig[i][c], inv[c][j])
				}
				want := byte(0)
				if i == j {
					want = 1
				}
				if acc != want {
					t.Fatalf("trial %d: product[%d][%d] = %d, want %d", trial, i, j, acc, want)
				}
			}
		}
	}
}

func TestRoundTripAllBlocks(t *testing.T) {
	data := []byte("systematic erasure coding over GF(256) with explicit polynomial 0x11d")
	blocks, err := Encode(data, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(marshalAll(blocks), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("got %q, want %q", got, data)
	}
}

func TestRecoverWithinTolerance(t *testing.T) {
	data := sampleData(1000)
	k, m := 6, 3
	blocks, err := Encode(data, k, m)
	if err != nil {
		t.Fatal(err)
	}
	raw := marshalAll(blocks)
	r := rand.New(rand.NewSource(1))
	// Try many random subsets that drop exactly m blocks (the tolerance).
	for trial := 0; trial < 100; trial++ {
		perm := r.Perm(k + m)
		dropped := make(map[int]bool)
		for _, idx := range perm[:m] {
			dropped[idx] = true
		}
		var subset [][]byte
		for i, b := range raw {
			if !dropped[i] {
				subset = append(subset, b)
			}
		}
		got, err := Decode(subset, 1<<20)
		if err != nil {
			t.Fatalf("trial %d dropped %v: %v", trial, dropped, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("trial %d dropped %v: data mismatch", trial, dropped)
		}
	}
}

func TestBeyondToleranceInsufficient(t *testing.T) {
	data := sampleData(500)
	k, m := 4, 2
	blocks, err := Encode(data, k, m)
	if err != nil {
		t.Fatal(err)
	}
	raw := marshalAll(blocks)
	// Drop m+1 blocks: only k-1 remain.
	got, err := Decode(raw[:k-1], 1<<20)
	if err != ErrInsufficient {
		t.Fatalf("err = %v, want ErrInsufficient", err)
	}
	if got != nil {
		t.Fatal("output must be nil on failure")
	}
}

func TestDuplicateIDRejected(t *testing.T) {
	data := sampleData(200)
	blocks, err := Encode(data, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	raw := marshalAll(blocks)
	dup := [][]byte{raw[0], raw[0], raw[1], raw[2]}
	if _, err := Decode(dup, 1<<20); err != ErrDuplicateID {
		t.Fatalf("err = %v, want ErrDuplicateID", err)
	}
}

func TestCorruptedBlockRejectedAndExcluded(t *testing.T) {
	data := sampleData(800)
	k, m := 4, 2
	blocks, err := Encode(data, k, m)
	if err != nil {
		t.Fatal(err)
	}
	raw := marshalAll(blocks)

	// Corrupt one parity block's payload: checksum must fail on parse.
	corrupted := make([]byte, len(raw[k]))
	copy(corrupted, raw[k])
	corrupted[headerSize] ^= 0xff
	if _, err := UnmarshalBlock(corrupted); err != ErrChecksum {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}

	// With the corrupted block plus k valid blocks, decode must succeed
	// and the corrupted block must not pollute the result.
	subset := append([][]byte{corrupted}, raw[:k]...)
	got, err := Decode(subset, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("corrupted block influenced the decoded output")
	}

	// Corrupt so many blocks that fewer than k valid remain.
	all := make([][]byte, 0, k+m)
	for _, r := range raw {
		c := make([]byte, len(r))
		copy(c, r)
		c[headerSize] ^= 0x01
		all = append(all, c)
	}
	all[0] = raw[0]
	all[1] = raw[1]
	all[2] = raw[2] // only 3 valid of k=4
	if _, err := Decode(all, 1<<20); err != ErrInsufficient {
		t.Fatalf("err = %v, want ErrInsufficient", err)
	}
}

func TestVersionRejected(t *testing.T) {
	blocks, err := Encode(sampleData(64), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw := blocks[0].Marshal()
	raw[0] = 99
	if _, err := UnmarshalBlock(raw); err != ErrVersion {
		t.Fatalf("err = %v, want ErrVersion", err)
	}
}

func TestTruncatedRejected(t *testing.T) {
	blocks, err := Encode(sampleData(64), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw := blocks[0].Marshal()
	if _, err := UnmarshalBlock(raw[:headerSize-1]); err != ErrTruncated {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	if _, err := UnmarshalBlock(raw[:len(raw)-1]); err != ErrTruncated {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestInvalidIDRejected(t *testing.T) {
	blocks, err := Encode(sampleData(64), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw := blocks[0].Marshal()
	raw[3] = 3 // k+m == 3, so id 3 is out of range
	if _, err := UnmarshalBlock(raw); err != ErrInvalidID {
		t.Fatalf("err = %v, want ErrInvalidID", err)
	}
}

func TestInconsistentHeadersRejected(t *testing.T) {
	a, err := Encode(sampleData(64), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encode(sampleData(128), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw := [][]byte{a[0].Marshal(), b[1].Marshal()}
	if _, err := Decode(raw, 1<<20); err != ErrInconsistent {
		t.Fatalf("err = %v, want ErrInconsistent", err)
	}
}

func TestOutputLimitEnforced(t *testing.T) {
	data := sampleData(300)
	blocks, err := Encode(data, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(marshalAll(blocks), len(data)-1); err != ErrOutputLimit {
		t.Fatalf("err = %v, want ErrOutputLimit", err)
	}
	got, err := Decode(marshalAll(blocks), len(data))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("data mismatch at exact output limit")
	}
}

func TestBadParams(t *testing.T) {
	if _, err := Encode(sampleData(10), 0, 1); err != ErrBadParams {
		t.Fatalf("err = %v, want ErrBadParams", err)
	}
	if _, err := Encode(sampleData(10), 200, 100); err != ErrBadParams {
		t.Fatalf("err = %v, want ErrBadParams", err)
	}
}

func TestSystematicLayout(t *testing.T) {
	data := sampleData(97)
	k, m := 4, 2
	blocks, err := Encode(data, k, m)
	if err != nil {
		t.Fatal(err)
	}
	// Data shards concatenated must be the zero-padded input.
	var joined []byte
	for i := 0; i < k; i++ {
		joined = append(joined, blocks[i].Data...)
	}
	if !bytes.Equal(joined[:len(data)], data) {
		t.Fatal("data shards are not systematic")
	}
	if len(joined) <= len(data) {
		t.Fatal("expected non-zero padding for 97 bytes over 4 shards")
	}
	for _, b := range joined[len(data):] {
		if b != 0 {
			t.Fatal("padding is not zero")
		}
	}
}
