package huffcodec

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

func roundTrip(t *testing.T, data []byte) []byte {
	t.Helper()
	enc, err := Encode(data, 16)
	if err != nil {
		t.Fatalf("encode %d bytes: %v", len(data), err)
	}
	dec, err := DecodeAll(enc, 1<<20)
	if err != nil {
		t.Fatalf("decode %d bytes: %v", len(data), err)
	}
	if !bytes.Equal(dec, data) {
		t.Fatalf("round trip mismatch on %d bytes", len(data))
	}
	return enc
}

func TestFormatRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	roundTrip(t, []byte{})
	roundTrip(t, []byte("a"))
	roundTrip(t, []byte("aaaaaaaaaaaaaaaaaaaa"))
	roundTrip(t, []byte("hello, canonical world!"))
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	roundTrip(t, all)
	for _, n := range []int{1, 2, 3, 17, 100, 4096} {
		data := make([]byte, n)
		rng.Read(data)
		roundTrip(t, data)
	}
}

func TestFormatDeterministic(t *testing.T) {
	data := []byte("deterministic output for identical input")
	a, err1 := Encode(data, 16)
	b, err2 := Encode(data, 16)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("same input encoded differently")
	}
}

func TestTruncatedTailNeverSucceeds(t *testing.T) {
	data := []byte("truncate me at any point and decoding must fail")
	enc := roundTrip(t, data)
	for cut := 0; cut < len(enc); cut++ {
		if _, err := DecodeAll(enc[:cut], 1<<20); err == nil {
			t.Fatalf("truncated to %d/%d bytes decoded without error", cut, len(enc))
		}
	}
}

func TestTamperedTableRejected(t *testing.T) {
	data := []byte("a message with several distinct symbols")
	enc := roundTrip(t, data)
	nSym := int(enc[3]) // small alphabet: uvarint fits in one byte
	pairsOff := 4
	for i := 0; i < nSym; i++ {
		bad := bytes.Clone(enc)
		bad[pairsOff+2*i+1] ^= 0x01 // perturb one code length
		if _, err := DecodeAll(bad, 1<<20); err == nil {
			t.Fatalf("tampered length for table entry %d accepted", i)
		}
	}
	// Splice: valid header from this message, payload from another.
	other := roundTrip(t, []byte("zzzzzzzzzzzzzzzzzzzzzzzzzzzz"))
	hdrLen := pairsOff + 2*nSym
	for hdrLen < len(enc) && enc[hdrLen-1]&0x80 != 0 {
		hdrLen++
	}
	// find payload starts by re-parsing both headers
	payA := payloadOffset(t, enc)
	payB := payloadOffset(t, other)
	spliced := append(bytes.Clone(enc[:payA]), other[payB:]...)
	if _, err := DecodeAll(spliced, 1<<20); err == nil {
		t.Fatal("spliced stream decoded without error")
	}
}

func payloadOffset(t *testing.T, enc []byte) int {
	t.Helper()
	i := 3
	n, m := uvarintAt(enc, i)
	i += m + int(n)*2
	_, m = uvarintAt(enc, i)
	i += m + 4
	return i
}

func uvarintAt(b []byte, off int) (uint64, int) {
	var v uint64
	for i := off; i < len(b); i++ {
		v |= uint64(b[i]&0x7f) << (7 * uint(i-off))
		if b[i] < 0x80 {
			return v, i - off + 1
		}
	}
	return 0, 0
}

func TestCorruptedPayloadRejected(t *testing.T) {
	data := []byte("payload corruption must be caught by the checksum")
	enc := roundTrip(t, data)
	pay := payloadOffset(t, enc)
	for i := pay; i < len(enc); i++ {
		bad := bytes.Clone(enc)
		bad[i] ^= 0x10
		if _, err := DecodeAll(bad, 1<<20); err == nil {
			t.Fatalf("flipped bit in payload byte %d accepted", i)
		}
	}
}

func TestTrailingGarbageRejected(t *testing.T) {
	enc := roundTrip(t, []byte("no trailing bytes allowed"))
	bad := append(bytes.Clone(enc), 0x00)
	if _, err := DecodeAll(bad, 1<<20); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("want ErrTrailingData, got %v", err)
	}
}

func TestVersionAndMagicChecked(t *testing.T) {
	enc := roundTrip(t, []byte("check the header fields"))
	bad := bytes.Clone(enc)
	bad[2] = 99
	if _, err := DecodeAll(bad, 1<<20); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("want ErrBadVersion, got %v", err)
	}
	bad = bytes.Clone(enc)
	bad[0] = 'X'
	if _, err := DecodeAll(bad, 1<<20); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

func TestDecodeOutputQuotaBounded(t *testing.T) {
	data := []byte("this output is larger than the granted quota")
	enc := roundTrip(t, data)
	if _, err := DecodeAll(enc, int64(len(data)-1)); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
	if _, err := DecodeAll(enc, int64(len(data))); err != nil {
		t.Fatalf("exact quota must succeed: %v", err)
	}
}

func TestSingleSymbolAndEmptyFormat(t *testing.T) {
	enc := roundTrip(t, []byte("mmmmmmmmmmmm"))
	if len(enc) == 0 {
		t.Fatal("empty encoding")
	}
	enc = roundTrip(t, nil)
	dec, err := DecodeAll(enc, 0)
	if err != nil || len(dec) != 0 {
		t.Fatalf("empty stream: dec=%v err=%v", dec, err)
	}
}
