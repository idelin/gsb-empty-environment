package nsec3

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"testing"
)

var (
	testSalt = []byte{0xde, 0xad, 0xbe, 0xef}
	testIter = uint16(10)
)

func hashForTest(t *testing.T, name string, iter uint16) []byte {
	t.Helper()
	v := &Verifier{} // zero value: default limits
	h, err := v.hashName(name, testSalt, iter)
	if err != nil {
		t.Fatalf("hashName(%q): %v", name, err)
	}
	return h
}

// chain builds NSEC3 records linking the given existing names in hash
// order, so every other hash is covered by exactly one record.
func chain(t *testing.T, iter uint16, names ...string) []Record {
	t.Helper()
	hashes := make([][]byte, len(names))
	for i, n := range names {
		hashes[i] = hashForTest(t, n, iter)
	}
	sort.Slice(hashes, func(i, j int) bool {
		return bytes.Compare(hashes[i], hashes[j]) < 0
	})
	recs := make([]Record, len(hashes))
	for i := range hashes {
		recs[i] = Record{
			HashAlg:    HashSHA1,
			Iterations: iter,
			Salt:       testSalt,
			OwnerHash:  hashes[i],
			NextHashed: hashes[(i+1)%len(hashes)],
		}
	}
	return recs
}

func TestNXDOMAINProof(t *testing.T) {
	recs := chain(t, testIter, "example.", "www.example.")
	v := NewVerifier()
	got, err := v.Verify("q.example.", recs)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != NXDOMAIN {
		t.Fatalf("got %v, want NXDOMAIN", got)
	}
}

func TestNoData(t *testing.T) {
	recs := chain(t, testIter, "example.", "www.example.")
	v := NewVerifier()
	got, err := v.Verify("www.example.", recs)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != NoData {
		t.Fatalf("got %v, want NODATA", got)
	}
}

func TestWildcard(t *testing.T) {
	recs := chain(t, testIter, "example.", "*.example.")
	v := NewVerifier()
	got, err := v.Verify("q.example.", recs)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != Wildcard {
		t.Fatalf("got %v, want Wildcard", got)
	}
}

func TestOverLimitIterations(t *testing.T) {
	recs := chain(t, testIter, "example.", "www.example.")
	for i := range recs {
		recs[i].Iterations = 65535
	}
	v := NewVerifier()
	got, err := v.Verify("q.example.", recs)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != Insecure {
		t.Fatalf("got %v, want Insecure", got)
	}
	if v.HashCount != 0 {
		t.Fatalf("HashCount = %d, want 0 (no hashing allowed)", v.HashCount)
	}
}

func TestIterationBoundary(t *testing.T) {
	for _, iter := range []uint16{100, 101} {
		recs := chain(t, iter, "example.", "www.example.")
		v := NewVerifier()
		got, err := v.Verify("q.example.", recs)
		if err != nil {
			t.Fatalf("iter=%d: Verify: %v", iter, err)
		}
		want := NXDOMAIN
		if iter > 100 {
			want = Insecure
		}
		if got != want {
			t.Fatalf("iter=%d: got %v, want %v", iter, got, want)
		}
	}
}

func TestSaltTooLong(t *testing.T) {
	recs := chain(t, testIter, "example.", "www.example.")
	for i := range recs {
		recs[i].Salt = make([]byte, 9)
	}
	v := NewVerifier()
	got, err := v.Verify("q.example.", recs)
	if !errors.Is(err, ErrSaltTooLong) {
		t.Fatalf("err = %v, want ErrSaltTooLong", err)
	}
	if got != Insecure {
		t.Fatalf("got %v, want Insecure", got)
	}
	if v.HashCount != 0 {
		t.Fatalf("HashCount = %d, want 0", v.HashCount)
	}
}

func TestCoversWrapAround(t *testing.T) {
	cases := []struct {
		owner, next, hash byte
		want              bool
	}{
		{0x10, 0x20, 0x18, true},  // plain interval, inside
		{0x10, 0x20, 0x05, false}, // plain interval, below
		{0x10, 0x20, 0x20, false}, // plain interval, boundary excluded
		{0xf0, 0x10, 0xf5, true},  // wrap, above owner
		{0xf0, 0x10, 0x05, true},  // wrap, below next
		{0xf0, 0x10, 0x80, false}, // wrap, outside
		{0xf0, 0x10, 0xf0, false}, // wrap, owner excluded
		{0xf0, 0x10, 0x10, false}, // wrap, next excluded
		{0x42, 0x42, 0x42, false}, // degenerate: excludes only itself
		{0x42, 0x42, 0x43, true},  // degenerate: covers everything else
	}
	for _, c := range cases {
		got := covers([]byte{c.owner}, []byte{c.next}, []byte{c.hash})
		if got != c.want {
			t.Errorf("covers(%#02x, %#02x, %#02x) = %v, want %v",
				c.owner, c.next, c.hash, got, c.want)
		}
	}
}

// TestWrapAroundProof runs an end-to-end verification where the next
// closer name is covered by the record whose interval wraps around the
// end of the hash space (owner > next).
func TestWrapAroundProof(t *testing.T) {
	recs := chain(t, testIter, "example.", "www.example.")
	var wrap *Record
	for i := range recs {
		if bytes.Compare(recs[i].OwnerHash, recs[i].NextHashed) > 0 {
			wrap = &recs[i]
		}
	}
	if wrap == nil {
		t.Fatal("test chain has no wrap-around record")
	}
	// Find a query whose next-closer hash lands in the wrap interval.
	for i := 0; ; i++ {
		qname := fmt.Sprintf("x%d.example.", i)
		h := hashForTest(t, qname, testIter)
		if covers(wrap.OwnerHash, wrap.NextHashed, h) {
			v := NewVerifier()
			got, err := v.Verify(qname, recs)
			if err != nil {
				t.Fatalf("Verify(%q): %v", qname, err)
			}
			if got != NXDOMAIN {
				t.Fatalf("Verify(%q) = %v, want NXDOMAIN", qname, got)
			}
			return
		}
		if i > 1000 {
			t.Fatal("no query name found in wrap-around interval")
		}
	}
}

func TestOptOutOnNextCloser(t *testing.T) {
	recs := chain(t, testIter, "example.", "www.example.")
	// Baseline: without opt-out the proof yields NXDOMAIN.
	v := NewVerifier()
	if got, _ := v.Verify("q.example.", recs); got != NXDOMAIN {
		t.Fatalf("baseline got %v, want NXDOMAIN", got)
	}
	// Set opt-out on the record covering the next closer name.
	nch := hashForTest(t, "q.example.", testIter)
	marked := false
	for i := range recs {
		if covers(recs[i].OwnerHash, recs[i].NextHashed, nch) {
			recs[i].Flags |= FlagOptOut
			marked = true
		}
	}
	if !marked {
		t.Fatal("no record covers the next closer name")
	}
	v = NewVerifier()
	got, err := v.Verify("q.example.", recs)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != Insecure {
		t.Fatalf("got %v, want Insecure (opt-out forbids NXDOMAIN)", got)
	}
}

func TestHashBudgetCap(t *testing.T) {
	recs := chain(t, testIter, "example.", "www.example.")
	v := NewVerifier()
	v.MaxHashes = 2
	got, err := v.Verify("a.b.c.d.example.", recs)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != Insecure {
		t.Fatalf("got %v, want Insecure", got)
	}
	if v.HashCount != 2 {
		t.Fatalf("HashCount = %d, want 2 (budget exhausted, not exceeded)", v.HashCount)
	}
}

func TestUnsupportedHashAlg(t *testing.T) {
	recs := chain(t, testIter, "example.")
	recs[0].HashAlg = 2
	v := NewVerifier()
	if _, err := v.Verify("q.example.", recs); !errors.Is(err, ErrUnsupportedHash) {
		t.Fatalf("err = %v, want ErrUnsupportedHash", err)
	}
}

func TestDecodeHashRoundTrip(t *testing.T) {
	// Base32hex of 20 zero octets, unpadded.
	h, err := DecodeHash("00000000000000000000000000000000")
	if err != nil {
		t.Fatalf("DecodeHash: %v", err)
	}
	if len(h) != 20 || !bytes.Equal(h, make([]byte, 20)) {
		t.Fatalf("DecodeHash = %x, want 20 zero octets", h)
	}
}
