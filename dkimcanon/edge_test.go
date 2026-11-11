package dkimcanon

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"hash"
	"testing"
)

func newSHA256() hash.Hash { return sha256.New() }

// failingSink returns errSink after its first failAt writes.
type failingSink struct {
	buf    bytes.Buffer
	failAt int
	calls  int
}

var errSink = errors.New("sink failure")

func (f *failingSink) Write(p []byte) (int, error) {
	f.calls++
	if f.calls >= f.failAt {
		return 0, errSink
	}
	return f.buf.Write(p)
}

func TestSinkErrorIsSticky(t *testing.T) {
	for _, algo := range []Algorithm{Simple, Relaxed} {
		sink := &failingSink{failAt: 2}
		c := NewCanonicalizer(sink, algo)
		var sawErr error
		for i := 1; i <= 40; i++ {
			if _, err := c.Write([]byte("abc\r\n")); err != nil {
				sawErr = err
				break
			}
		}
		if !errors.Is(sawErr, errSink) {
			t.Fatalf("algo=%v: want sink error, got %v", algo, sawErr)
		}
		if !errors.Is(c.Err(), errSink) {
			t.Fatalf("algo=%v: sink error not retained", algo)
		}
		if _, err := c.Write([]byte("x")); !errors.Is(err, errSink) {
			t.Fatalf("algo=%v: write after sink error returned %v", algo, err)
		}
		if err := c.Close(); !errors.Is(err, errSink) {
			t.Fatalf("algo=%v: close returned %v", algo, err)
		}
	}
}

func TestRelaxedWhitespaceAcrossChunks(t *testing.T) {
	// WSP runs and the CRLF can all straddle chunk boundaries; the canonical
	// output must be identical for every split.
	body := []byte("a   \t b\t\r\n\t\t c  \r\n")
	want := []byte("a b\r\nc\r\n")

	for _, size := range chunkSizes {
		var buf bytes.Buffer
		c := NewRelaxed(&buf)
		if err := writeChunks(c, body, size); err != nil {
			t.Fatalf("size=%d: %v", size, err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("size=%d: %v", size, err)
		}
		if !bytes.Equal(buf.Bytes(), want) {
			t.Fatalf("size=%d:\n got %q\nwant %q", size, buf.Bytes(), want)
		}
	}
}

func TestOneByteLimitBoundary(t *testing.T) {
	body := []byte("ab\r\ncd\r\n")

	// limit=1 on the simple stream: only the very first canonical octet.
	got, err := Canonicalize(Simple, body, WithBodyLength(1))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a" {
		t.Fatalf("limit=1 got %q want \"a\"", got)
	}

	// Same result must hold for every chunking.
	for _, size := range chunkSizes {
		var buf bytes.Buffer
		c := NewSimple(&buf, WithBodyLength(1))
		if err := writeChunks(c, body, size); err != nil {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if buf.String() != "a" {
			t.Fatalf("size=%d got %q want \"a\"", size, buf.Bytes())
		}
		if !c.Truncated() || c.Emitted() != 1 {
			t.Fatalf("size=%d: Truncated=%v Emitted=%d", size, c.Truncated(), c.Emitted())
		}
	}
}

func TestEmptyBodyCanonicalization(t *testing.T) {
	simple, err := Canonicalize(Simple, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(simple) != "\r\n" {
		t.Fatalf("simple empty body got %q want CRLF", simple)
	}

	relaxed, err := Canonicalize(Relaxed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(relaxed) != 0 {
		t.Fatalf("relaxed empty body got %q want empty", relaxed)
	}

	// A body made solely of blank lines: simple keeps one final CRLF,
	// relaxed has no non-empty line so it canonicalizes to empty.
	blank := []byte("\r\n\r\n")
	if got, _ := Canonicalize(Simple, blank); string(got) != "\r\n" {
		t.Fatalf("simple blank-only body got %q", got)
	}
	if got, _ := Canonicalize(Relaxed, blank); len(got) != 0 {
		t.Fatalf("relaxed blank-only body got %q, want empty", got)
	}
}

func TestWriteIsIdempotentAcrossArbitraryBoundaries(t *testing.T) {
	// Cross-CRLF and cross-whitespace boundaries: digest of the whole body
	// written in one call equals the digest built from singleton-byte writes.
	body := []byte("header-ish \t value\r\n\r\nsecond line \r\n")

	for _, algo := range []Algorithm{Simple, Relaxed} {
		oneShot, err := BodyHash(algo, newSHA256, body)
		if err != nil {
			t.Fatal(err)
		}
		h := newSHA256()
		d := NewDigester(algo, h)
		if err := writeChunks(d, body, 1); err != nil {
			t.Fatal(err)
		}
		streamed, err := d.Sum()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(oneShot, streamed) {
			t.Fatalf("algo=%v: singleton-byte digest differs from one-shot", algo)
		}
	}
}
