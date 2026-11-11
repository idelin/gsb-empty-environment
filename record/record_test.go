package record

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"testing"
)

var (
	testAESKey = bytes.Repeat([]byte{0x11}, 16)
	testMACKey = bytes.Repeat([]byte{0x22}, 32)
)

type countingCounter struct{ n int }

func (c *countingCounter) AuthFailure() { c.n++ }

func newPair(t *testing.T) (*Writer, *Reader, *countingCounter) {
	t.Helper()
	w, err := NewWriter(testAESKey, testMACKey)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	counter := &countingCounter{}
	r, err := NewReader(testAESKey, testMACKey, counter)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return w, r, counter
}

func recomputeTag(r *Reader, seq uint64, iv, ciphertext []byte) []byte {
	mac := r.mac()
	var header [16]byte
	binary.BigEndian.PutUint64(header[:8], seq)
	binary.BigEndian.PutUint64(header[8:], uint64(len(ciphertext)))
	mac.Write(header[:])
	mac.Write(iv)
	mac.Write(ciphertext)
	return mac.Sum(nil)
}

func TestRoundTrip(t *testing.T) {
	w, r, counter := newPair(t)

	sizes := []int{0, 1, 15, 16, 17, 31, 32, 33, 1024, 4095, 4096}
	for i, size := range sizes {
		plaintext := make([]byte, size)
		for j := range plaintext {
			plaintext[j] = byte(j*7 + i)
		}

		record, err := w.Seal(plaintext)
		if err != nil {
			t.Fatalf("Seal(%d): %v", size, err)
		}
		if got, want := r.Sequence(), uint64(i); got != want {
			t.Fatalf("sequence before Open %d: got %d want %d", i, got, want)
		}

		got, err := r.Open(record)
		if err != nil {
			t.Fatalf("Open(%d): %v", size, err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Fatalf("plaintext mismatch for size %d", size)
		}
		if got, want := r.Sequence(), uint64(i+1); got != want {
			t.Fatalf("sequence after Open %d: got %d want %d", i, got, want)
		}
		if counter.n != 0 || r.Failures() != 0 {
			t.Fatalf("successful Open recorded failures: counter=%d failures=%d", counter.n, r.Failures())
		}
	}
}

func TestOpenReturnsFreshBuffer(t *testing.T) {
	w, r, _ := newPair(t)
	record, err := w.Seal([]byte("confidential payload"))
	if err != nil {
		t.Fatal(err)
	}

	plaintext, err := r.Open(record)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("confidential payload")

	for i := range record {
		record[i] ^= 0xFF
	}
	if !bytes.Equal(plaintext, want) {
		t.Fatalf("plaintext aliases input record: got %q want %q", plaintext, want)
	}
}

// TestAllTruncationPositions cuts a valid record at every possible
// position. Every prefix must be rejected with ErrAuth, expose no
// plaintext, leave the sequence number untouched, and count exactly one
// failure.
func TestAllTruncationPositions(t *testing.T) {
	w, r, counter := newPair(t)
	record, err := w.Seal(bytes.Repeat([]byte{0xAB}, 67))
	if err != nil {
		t.Fatal(err)
	}

	beforeSeq := r.Sequence()
	for cut := 0; cut < len(record); cut++ {
		truncated := record[:cut]
		failuresBefore := r.Failures()

		plaintext, err := r.Open(truncated)
		if !errors.Is(err, ErrAuth) {
			t.Fatalf("truncation at %d: err = %v, want ErrAuth", cut, err)
		}
		if plaintext != nil {
			t.Fatalf("truncation at %d: plaintext leaked: %x", cut, plaintext)
		}
		if r.Sequence() != beforeSeq {
			t.Fatalf("truncation at %d advanced sequence to %d", cut, r.Sequence())
		}
		if r.Failures() != failuresBefore+1 {
			t.Fatalf("truncation at %d: failure not counted", cut)
		}
		if got := counter.n; got != int(r.Failures()) {
			t.Fatalf("truncation at %d: external counter %d != Failures %d", cut, got, r.Failures())
		}
	}
}

// TestLastBlockByteByByteTampering changes every byte of the final
// ciphertext block to every alternative value. All 16*255 mutations must
// fail identically with ErrAuth and no plaintext.
func TestLastBlockByteByByteTampering(t *testing.T) {
	w, r, counter := newPair(t)
	record, err := w.Seal(bytes.Repeat([]byte{0x3C}, 80))
	if err != nil {
		t.Fatal(err)
	}

	lastBlockStart := len(record) - TagSize - aes.BlockSize
	beforeSeq := r.Sequence()
	mutations := 0

	for offset := lastBlockStart; offset < lastBlockStart+aes.BlockSize; offset++ {
		original := record[offset]
		for replacement := 0; replacement < 256; replacement++ {
			if byte(replacement) == original {
				continue
			}
			mutated := append([]byte(nil), record...)
			mutated[offset] = byte(replacement)

			plaintext, err := r.Open(mutated)
			if !errors.Is(err, ErrAuth) {
				t.Fatalf("offset %d -> 0x%02x: err = %v, want ErrAuth", offset, replacement, err)
			}
			if plaintext != nil {
				t.Fatalf("offset %d -> 0x%02x: plaintext leaked", offset, replacement)
			}
			if r.Sequence() != beforeSeq {
				t.Fatalf("offset %d -> 0x%02x advanced sequence", offset, replacement)
			}
			mutations++
		}
	}

	if mutations != aes.BlockSize*255 {
		t.Fatalf("ran %d mutations, want %d", mutations, aes.BlockSize*255)
	}
	if r.Failures() != uint64(mutations) {
		t.Fatalf("Failures = %d, want %d", r.Failures(), mutations)
	}
	if counter.n != mutations {
		t.Fatalf("external counter = %d, want %d", counter.n, mutations)
	}
}

// TestTamperEverywhere flips one bit at every position of the full record.
func TestTamperEverywhere(t *testing.T) {
	w, r, _ := newPair(t)
	record, err := w.Seal([]byte("flip every single bit of this record"))
	if err != nil {
		t.Fatal(err)
	}

	for pos := range record {
		for bit := uint(0); bit < 8; bit++ {
			mutated := append([]byte(nil), record...)
			mutated[pos] ^= 1 << bit

			plaintext, err := r.Open(mutated)
			if !errors.Is(err, ErrAuth) {
				t.Fatalf("pos %d bit %d: err = %v, want ErrAuth", pos, bit, err)
			}
			if plaintext != nil {
				t.Fatalf("pos %d bit %d: plaintext leaked", pos, bit)
			}
		}
	}
}

// TestNonBlockMultipleLength covers lengths that pass the minimum-size
// check but have a ciphertext region that is not block aligned.
func TestNonBlockMultipleLength(t *testing.T) {
	w, r, counter := newPair(t)
	record, err := w.Seal([]byte("padding boundary probe"))
	if err != nil {
		t.Fatal(err)
	}

	for _, extra := range []int{1, 2, 7, 9, 15} {
		bad := append(append([]byte(nil), record...), make([]byte, extra)...)
		plaintext, err := r.Open(bad)
		if !errors.Is(err, ErrAuth) {
			t.Fatalf("extra %d bytes: err = %v, want ErrAuth", extra, err)
		}
		if plaintext != nil {
			t.Fatalf("extra %d bytes: plaintext leaked", extra)
		}
	}
	if r.Sequence() != 0 {
		t.Fatalf("sequence = %d, want 0", r.Sequence())
	}
	if int(r.Failures()) != counter.n || counter.n == 0 {
		t.Fatalf("counter=%d failures=%d", counter.n, r.Failures())
	}
}

// TestSequenceAdvanceAndReplay verifies the sequence advances only after a
// successful record, that replaying an old record fails, that skipping a
// record fails, and that the stream recovers at the expected sequence.
func TestSequenceAdvanceAndReplay(t *testing.T) {
	w, r, counter := newPair(t)

	first, err := w.Seal([]byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.Seal([]byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	third, err := w.Seal([]byte("third"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.Open(first); err != nil {
		t.Fatalf("first: %v", err)
	}
	if r.Sequence() != 1 {
		t.Fatalf("sequence after first = %d, want 1", r.Sequence())
	}

	if plaintext, err := r.Open(first); !errors.Is(err, ErrAuth) || plaintext != nil {
		t.Fatalf("replay: plaintext=%v err=%v", plaintext, err)
	}
	if r.Sequence() != 1 {
		t.Fatalf("replay changed sequence to %d", r.Sequence())
	}

	if plaintext, err := r.Open(third); !errors.Is(err, ErrAuth) || plaintext != nil {
		t.Fatalf("skip: plaintext=%v err=%v", plaintext, err)
	}
	if r.Sequence() != 1 {
		t.Fatalf("skip changed sequence to %d", r.Sequence())
	}

	if _, err := r.Open(second); err != nil {
		t.Fatalf("second after failures: %v", err)
	}
	if r.Sequence() != 2 {
		t.Fatalf("sequence after second = %d, want 2", r.Sequence())
	}
	if _, err := r.Open(third); err != nil {
		t.Fatalf("third: %v", err)
	}
	if r.Sequence() != 3 {
		t.Fatalf("sequence after third = %d, want 3", r.Sequence())
	}

	if r.Failures() != 2 || counter.n != 2 {
		t.Fatalf("Failures=%d counter=%d, want 2/2", r.Failures(), counter.n)
	}
}

// TestWrongKeys confirms key mismatches surface as ErrAuth.
func TestWrongKeys(t *testing.T) {
	w, _, _ := newPair(t)
	record, err := w.Seal([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	otherAES, err := NewReader(bytes.Repeat([]byte{0x99}, 16), testMACKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext, err := otherAES.Open(record); !errors.Is(err, ErrAuth) || plaintext != nil {
		t.Fatalf("wrong AES key: plaintext=%v err=%v", plaintext, err)
	}

	otherMAC, err := NewReader(testAESKey, bytes.Repeat([]byte{0x88}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext, err := otherMAC.Open(record); !errors.Is(err, ErrAuth) || plaintext != nil {
		t.Fatalf("wrong MAC key: plaintext=%v err=%v", plaintext, err)
	}
}

// TestAuthenticatedBadPadding builds a record whose MAC is valid but whose
// plaintext has structurally invalid PKCS#7 padding. It must be reported
// as the same ErrAuth sentinel, deterministically, with no plaintext.
func TestAuthenticatedBadPadding(t *testing.T) {
	w, r, _ := newPair(t)

	record, err := w.Seal([]byte("padding oracle probe"))
	if err != nil {
		t.Fatal(err)
	}
	iv := record[:aes.BlockSize]
	ciphertext := record[aes.BlockSize : len(record)-TagSize]

	// Decrypt legitimately, force an illegal zero padding byte, then
	// re-encrypt under the same IV so the corruption is deterministic.
	decrypted := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(r.block, iv).CryptBlocks(decrypted, ciphertext)
	decrypted[len(decrypted)-1] = 0x00
	cipher.NewCBCEncrypter(r.block, iv).CryptBlocks(ciphertext, decrypted)

	// Recompute the MAC so authentication passes and the failure happens
	// specifically at the padding check.
	tag := recomputeTag(r, r.seq, iv, ciphertext)
	copy(record[len(record)-TagSize:], tag)

	got, err := r.Open(record)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("bad padding: err = %v, want ErrAuth", err)
	}
	if got != nil {
		t.Fatalf("bad padding leaked plaintext: %x", got)
	}
	if r.Sequence() != 0 {
		t.Fatalf("bad padding advanced sequence to %d", r.Sequence())
	}
	if r.Failures() != 1 {
		t.Fatalf("Failures = %d, want 1", r.Failures())
	}
}

// TestCounterFuncAndNilCounter exercises the functional Counter adapter
// and a nil counter (failures must still be counted internally).
func TestCounterFuncAndNilCounter(t *testing.T) {
	w, err := NewWriter(testAESKey, testMACKey)
	if err != nil {
		t.Fatal(err)
	}
	record, err := w.Seal([]byte("counter wiring"))
	if err != nil {
		t.Fatal(err)
	}

	var external int
	r, err := NewReader(testAESKey, testMACKey, CounterFunc(func() { external++ }))
	if err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if plaintext, err := r.Open(record[:10]); !errors.Is(err, ErrAuth) || plaintext != nil {
			t.Fatalf("expected ErrAuth, got plaintext=%v err=%v", plaintext, err)
		}
	}
	if external != 3 || r.Failures() != 3 {
		t.Fatalf("external=%d Failures=%d, want 3/3", external, r.Failures())
	}

	nilCounter, err := NewReader(testAESKey, testMACKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext, err := nilCounter.Open(nil); !errors.Is(err, ErrAuth) || plaintext != nil {
		t.Fatalf("nil counter: plaintext=%v err=%v", plaintext, err)
	}
	if nilCounter.Failures() != 1 {
		t.Fatalf("nil-counter Failures = %d, want 1", nilCounter.Failures())
	}
}

// flakyCounter panics on its second invocation to verify that a
// misbehaving counter cannot make a failure path distinguishable from
// ErrAuth or corrupt the internal tally.
type flakyCounter struct{ calls int }

func (c *flakyCounter) AuthFailure() {
	c.calls++
	if c.calls == 2 {
		panic("boom")
	}
}

func TestPanickingCounterStillReturnsErrAuth(t *testing.T) {
	counter := &flakyCounter{}
	r, err := NewReader(testAESKey, testMACKey, counter)
	if err != nil {
		t.Fatal(err)
	}

	if plaintext, err := r.Open(nil); !errors.Is(err, ErrAuth) || plaintext != nil {
		t.Fatalf("first failure: plaintext=%v err=%v", plaintext, err)
	}

	// The counter panics on this second failure. Recovery must still
	// surface ErrAuth and must not double-count.
	plaintext, err := r.Open(nil)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if plaintext != nil {
		t.Fatalf("plaintext leaked: %x", plaintext)
	}
	if r.Failures() != 2 {
		t.Fatalf("Failures = %d, want 2", r.Failures())
	}
}
