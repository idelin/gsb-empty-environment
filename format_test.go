package prefixcode

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
)

func TestFormatRoundTrip(t *testing.T) {
	msg := []byte("the quick brown fox jumps over the lazy dog. pack my box! 12345")
	data, err := Compress(msg, 12)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decompress(bytes.NewReader(data), -1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, msg) {
		t.Fatalf("got %q want %q", out, msg)
	}
}

func TestFormatEmptyMessage(t *testing.T) {
	data, err := Compress(nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decompress(bytes.NewReader(data), -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("want empty output, got %d bytes", len(out))
	}
}

func TestFormatSingleSymbolMessage(t *testing.T) {
	msg := bytes.Repeat([]byte{'x'}, 1000)
	data, err := Compress(msg, 8)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decompress(bytes.NewReader(data), int64(len(msg)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, msg) {
		t.Fatal("single-symbol round trip failed")
	}
}

func TestFormatVersionRejected(t *testing.T) {
	data, err := Compress([]byte("hello"), 8)
	if err != nil {
		t.Fatal(err)
	}
	data[3] = 99
	if _, err := Decompress(bytes.NewReader(data), -1); err == nil {
		t.Fatal("unknown version must be rejected")
	}
}

func TestTamperedCodeTableRejected(t *testing.T) {
	msg := []byte("some reasonably long message to compress with a real table")
	data, err := Compress(msg, 12)
	if err != nil {
		t.Fatal(err)
	}
	// Flip bits in every code-length byte, one at a time. Each tampered
	// stream must be rejected (validation or checksum), never misdecoded.
	for off := 8; off < 8+256; off++ {
		bad := append([]byte(nil), data...)
		bad[off] ^= 0x01
		if out, err := Decompress(bytes.NewReader(bad), -1); err == nil {
			t.Fatalf("tampered length byte %d accepted, decoded %q", off, out)
		}
	}
}

func TestTamperedPayloadRejected(t *testing.T) {
	msg := []byte("another message with enough symbols to be interesting")
	data, err := Compress(msg, 12)
	if err != nil {
		t.Fatal(err)
	}
	for off := headerSize; off < len(data); off++ {
		bad := append([]byte(nil), data...)
		bad[off] ^= 0x10
		if _, err := Decompress(bytes.NewReader(bad), -1); err == nil {
			t.Fatalf("tampered payload byte %d accepted", off)
		}
	}
}

func TestTruncatedFileRejected(t *testing.T) {
	msg := []byte("truncate me at every interesting boundary please")
	data, err := Compress(msg, 12)
	if err != nil {
		t.Fatal(err)
	}
	points := []int{0, 1, 3, 4, 100, headerSize - 1, headerSize, headerSize + 1, len(data) - 1}
	for _, n := range points {
		if n >= len(data) {
			continue
		}
		if out, err := Decompress(bytes.NewReader(data[:n]), -1); err == nil {
			t.Fatalf("truncation to %d bytes accepted, decoded %q", n, out)
		}
	}
}

func TestTrailingGarbageNotConsumed(t *testing.T) {
	msg := []byte("boundary check")
	data, err := Compress(msg, 8)
	if err != nil {
		t.Fatal(err)
	}
	garbage := []byte("GARBAGE")
	r := bytes.NewReader(append(append([]byte(nil), data...), garbage...))
	out, err := Decompress(r, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, msg) {
		t.Fatal("round trip failed")
	}
	if r.Len() != len(garbage) {
		t.Fatalf("decoder over-read: %d of %d garbage bytes left", r.Len(), len(garbage))
	}
}

func TestConcatenatedStreams(t *testing.T) {
	m1 := []byte("first message, first table")
	m2 := []byte("second message with a different distribution!!!")
	d1, err := Compress(m1, 10)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := Compress(m2, 10)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(append(d1, d2...))
	o1, err := Decompress(r, -1)
	if err != nil {
		t.Fatal(err)
	}
	o2, err := Decompress(r, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(o1, m1) || !bytes.Equal(o2, m2) {
		t.Fatal("concatenated streams decoded incorrectly")
	}
}

func TestFormatQuotaBounded(t *testing.T) {
	msg := []byte("this message has quite a few symbols in it")
	data, err := Compress(msg, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decompress(bytes.NewReader(data), int64(len(msg)-1)); err != ErrQuota {
		t.Fatalf("want ErrQuota, got %v", err)
	}
	if _, err := Decompress(bytes.NewReader(data), int64(len(msg))); err != nil {
		t.Fatalf("exact quota should succeed: %v", err)
	}
}

func TestChunkedReader(t *testing.T) {
	rng := rand.New(rand.NewSource(21))
	msg := make([]byte, 5000)
	for i := range msg {
		msg[i] = byte(rng.Intn(40))
	}
	data, err := Compress(msg, 12)
	if err != nil {
		t.Fatal(err)
	}
	// A reader that yields 1 byte per Read call.
	out, err := Decompress(&slowReader{data: data}, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, msg) {
		t.Fatal("chunked-reader round trip failed")
	}
}

type slowReader struct {
	data []byte
	off  int
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	p[0] = r.data[r.off]
	r.off++
	return 1, nil
}
