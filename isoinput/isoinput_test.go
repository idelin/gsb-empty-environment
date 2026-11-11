package isoinput

import (
	"crypto/sha256"
	"errors"
	"io"
	"reflect"
	"testing"
	"unsafe"
)

func concat(blocks ...[]byte) []byte {
	var out []byte
	for _, b := range blocks {
		out = append(out, b...)
	}
	return out
}

// parseRecords is a normal zero-copy parser: it reads length-prefixed
// records ([1 byte len][payload]...) through read-only spans only.
func parseRecords(sd *Side) ([]string, error) {
	var records []string
	var pos int64
	for pos < sd.src.Size() {
		hdr, err := sd.Borrow(pos, 1)
		if err != nil {
			return nil, err
		}
		n := int64(hdr.At(0))
		hdr.Release()
		pos++
		if pos+n > sd.src.Size() {
			return nil, errors.New("truncated record")
		}
		body, err := sd.Borrow(pos, n)
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(body.NewReader())
		body.Release()
		if err != nil {
			return nil, err
		}
		records = append(records, string(raw))
		pos += n
	}
	return records, nil
}

// TestMaliciousWriteCallbackIsolation lends one input to two parsers;
// parser A runs a malicious write callback. The callback can only
// obtain a mutable buffer via Copy, so its writes must not leak into
// parser B's view, and both sides' evidence must still point at the
// same initial input digest.
func TestMaliciousWriteCallbackIsolation(t *testing.T) {
	initial := []byte("\x05hello\x05world")
	src := NewSource(append([]byte(nil), initial...))
	wantDigest := sha256.Sum256(initial)

	sideA := src.Side("parser-a")
	sideB := src.Side("parser-b")

	// Parser B borrows first and keeps its span open across A's attack.
	spanB, err := sideB.Borrow(1, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer spanB.Release()

	// Parser A's malicious write callback: it wants to rewrite the
	// shared input to "HELLO" to make B diverge. The read-only span
	// interface gives it no mutable handle, so the only sanctioned
	// route is an independent copy.
	maliciousWrite := func(sd *Side) {
		buf, err := sd.Copy(1, 5)
		if err != nil {
			t.Error(err)
			return
		}
		for i := range buf {
			buf[i] -= 'a' - 'A'
		}
		// buf is private: nothing here can reach the shared input.
	}
	maliciousWrite(sideA)

	if got := string(spanB.Segments()[0]); got != "hello" {
		t.Fatalf("parser B view corrupted by A's write: got %q", got)
	}
	if !src.VerifyIntegrity() {
		t.Fatal("shared input mutated through the isolation layer")
	}

	// Both sides parse the same logical content: no fake divergence.
	recA, err := parseRecords(sideA)
	if err != nil {
		t.Fatal(err)
	}
	recB, err := parseRecords(sideB)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recA, recB) || !reflect.DeepEqual(recA, []string{"hello", "world"}) {
		t.Fatalf("fake divergence: A=%v B=%v", recA, recB)
	}

	// Evidence from both sides attests to the same initial input.
	evA, evB := sideA.Evidence(), sideB.Evidence()
	if evA.Digest != wantDigest || evB.Digest != wantDigest {
		t.Fatal("evidence digest does not match the initial input")
	}
	if len(evA.Ranges) == 0 || len(evB.Ranges) == 0 {
		t.Fatal("read ranges were not recorded")
	}
}

// TestUnsafeTamperDetected shows that even a parser cheating with
// unsafe to write through a read-only segment cannot fabricate a
// divergence silently: the source digest check catches it.
func TestUnsafeTamperDetected(t *testing.T) {
	initial := []byte("attacked-input")
	src := NewSource(append([]byte(nil), initial...))

	sd := src.Side("cheater")
	sp, err := sd.Borrow(0, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Release()

	seg := sp.Segments()[0]
	hack := unsafe.Slice(unsafe.SliceData(seg), len(seg))
	hack[0] = 'X'

	if src.VerifyIntegrity() {
		t.Fatal("tampering through unsafe went undetected")
	}
}

// TestCrossBlockBorrowAndCancel checks that spans crossing block
// boundaries read the right bytes, and that cancelling one side never
// reclaims blocks still borrowed by the other side.
func TestCrossBlockBorrowAndCancel(t *testing.T) {
	b0 := []byte("aaa")
	b1 := []byte("BBB")
	b2 := []byte("ccc")
	src := NewSource(b0, b1, b2)
	wantDigest := sha256.Sum256(concat(b0, b1, b2))

	sideA := src.Side("a")
	sideB := src.Side("b")

	// A borrows across blocks 0-1, B borrows across blocks 1-2.
	spanA, err := sideA.Borrow(2, 3) // "aBB"
	if err != nil {
		t.Fatal(err)
	}
	spanB, err := sideB.Borrow(4, 4) // "BBcc"
	if err != nil {
		t.Fatal(err)
	}

	readSpan := func(sp *Span) string {
		buf := make([]byte, sp.Len())
		for i := range buf {
			buf[i] = sp.At(int64(i))
		}
		return string(buf)
	}
	if got := readSpan(spanA); got != "aBB" {
		t.Fatalf("cross-block span A = %q", got)
	}
	if got := readSpan(spanB); got != "BBcc" {
		t.Fatalf("cross-block span B = %q", got)
	}

	// Cancel A: block 0 is only borrowed by A and is reclaimed; block 1
	// is still borrowed by B and must survive; block 2 is B's too.
	sideA.Cancel()
	if !src.BlockReclaimed(0) {
		t.Fatal("block 0 should be reclaimed after A's cancel")
	}
	if src.BlockReclaimed(1) {
		t.Fatal("block 1 reclaimed while B still borrows it")
	}
	if src.BlockReclaimed(2) {
		t.Fatal("block 2 reclaimed while B still borrows it")
	}

	// B's cross-block data is still fully readable after A's cancel.
	if got := readSpan(spanB); got != "BBcc" {
		t.Fatalf("B's span broken by A's cancel: %q", got)
	}
	if !src.VerifyIntegrity() {
		t.Fatal("input corrupted by cancel")
	}

	// A cancelled side cannot borrow again.
	if _, err := sideA.Borrow(0, 1); !errors.Is(err, ErrCancelled) {
		t.Fatalf("borrow after cancel: %v", err)
	}

	// Once B releases, the remaining blocks are reclaimed.
	spanB.Release()
	if !src.BlockReclaimed(1) || !src.BlockReclaimed(2) {
		t.Fatal("blocks not reclaimed after last borrower released")
	}

	// Evidence still ties both sides to the same initial input.
	if sideA.Evidence().Digest != wantDigest || sideB.Evidence().Digest != wantDigest {
		t.Fatal("evidence digest mismatch with initial input")
	}
}

// TestZeroCopyNormalParsingEvidence runs a normal parser over the
// read-only interface and proves the run was zero-copy and that both
// sides' evidence comes from the same initial input.
func TestZeroCopyNormalParsingEvidence(t *testing.T) {
	blk0 := []byte("\x03foo")
	blk1 := []byte("\x03bar\x00")
	initial := concat(blk0, blk1)
	src := NewSource(blk0, blk1)
	wantDigest := sha256.Sum256(initial)

	sideA := src.Side("normal-a")
	sideB := src.Side("normal-b")

	recA, err := parseRecords(sideA)
	if err != nil {
		t.Fatal(err)
	}
	recB, err := parseRecords(sideB)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"foo", "bar", ""}
	if !reflect.DeepEqual(recA, want) || !reflect.DeepEqual(recB, want) {
		t.Fatalf("parse results: A=%v B=%v", recA, recB)
	}

	// Zero-copy: no bytes were ever handed out as mutable copies.
	if got := src.CopiedBytes(); got != 0 {
		t.Fatalf("expected zero-copy parsing, copied %d bytes", got)
	}

	// Evidence: identical initial-input digest on both sides, and the
	// recorded read ranges match what the parser actually touched.
	evA, evB := sideA.Evidence(), sideB.Evidence()

	// A single-block borrow must alias the block, not copy it.
	sp, err := sideA.Borrow(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	segs := sp.Segments()
	if len(segs) != 1 || &segs[0][0] != &blk0[1] {
		t.Fatal("single-block span is not a zero-copy alias")
	}
	sp.Release()

	if evA.Digest != wantDigest || evB.Digest != wantDigest {
		t.Fatal("evidence not anchored to the initial input digest")
	}
	if evA.Digest != evB.Digest {
		t.Fatal("sides disagree on the input they parsed")
	}
	wantRanges := []Range{
		{Off: 0, Len: 1}, {Off: 1, Len: 3},
		{Off: 4, Len: 1}, {Off: 5, Len: 3},
		{Off: 8, Len: 1}, {Off: 9, Len: 0},
	}
	if !reflect.DeepEqual(evA.Ranges, wantRanges) {
		t.Fatalf("recorded read ranges = %+v", evA.Ranges)
	}
	if !reflect.DeepEqual(evA.Ranges, evB.Ranges) {
		t.Fatal("sides read different ranges from the same input")
	}
	if !src.VerifyIntegrity() {
		t.Fatal("input mutated during zero-copy parsing")
	}
}
