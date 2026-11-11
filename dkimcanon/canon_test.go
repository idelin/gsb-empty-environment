package dkimcanon

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// refSimple is a direct, non-streaming transcription of RFC 6376 section 3.4.5
// used as the oracle for the streaming Canonicalizer.
func refSimple(body []byte) []byte {
	if len(body) == 0 {
		return []byte("\r\n")
	}
	lines := strings.Split(string(body), "\r\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return []byte(strings.Join(lines, "\r\n") + "\r\n")
}

// refRelaxed transcribes RFC 6376 section 3.4.6.
func refRelaxed(body []byte) []byte {
	lines := strings.Split(string(body), "\r\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	out := make([]byte, 0, len(body))
	nonEmpty := false
	for _, line := range lines {
		var b strings.Builder
		pendingSpace := false
		seenContent := false
		for _, r := range line {
			switch r {
			case ' ', '\t':
				if seenContent {
					pendingSpace = true
				}
			default:
				if pendingSpace {
					b.WriteByte(' ')
					pendingSpace = false
				}
				seenContent = true
				b.WriteRune(r)
			}
		}
		canonLine := b.String()
		if canonLine != "" {
			nonEmpty = true
		}
		out = append(out, canonLine...)
		out = append(out, '\r', '\n')
	}
	if !nonEmpty {
		return []byte{}
	}
	return out
}

func hasInvalidNewline(body []byte) bool {
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\r':
			if i+1 >= len(body) || body[i+1] != '\n' {
				return true
			}
			i++
		case '\n':
			return true
		}
	}
	return false
}

var chunkSizes = []int{1, 2, 3, 5, 7, 16, 64, 4096}

type chunkWriter interface {
	Write([]byte) (int, error)
}

func writeChunks(w chunkWriter, body []byte, size int) error {
	for len(body) > 0 {
		n := size
		if n > len(body) {
			n = len(body)
		}
		got, err := w.Write(body[:n])
		if err != nil {
			return err
		}
		if got != n {
			return fmt.Errorf("short write: got %d want %d", got, n)
		}
		body = body[n:]
	}
	return nil
}

// streamCanon runs the body through the streaming canonicalizer at every
// chunk size and asserts every boundary pattern yields the same bytes.
func streamCanon(t *testing.T, algo Algorithm, body []byte, opts ...Option) []byte {
	t.Helper()
	var reference []byte
	for _, size := range chunkSizes {
		var buf bytes.Buffer
		c := NewCanonicalizer(&buf, algo, opts...)
		if err := writeChunks(c, body, size); err != nil {
			t.Fatalf("size=%d body=%q: %v", size, body, err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("size=%d body=%q: close: %v", size, body, err)
		}
		if reference == nil {
			reference = buf.Bytes()
		} else if !bytes.Equal(buf.Bytes(), reference) {
			t.Fatalf("chunk size %d produced inconsistent output\n got: %q\nwant: %q",
				size, buf.Bytes(), reference)
		}
	}
	return reference
}

func TestCanonicalizationMatchesReference(t *testing.T) {
	bodies := [][]byte{
		nil,
		[]byte(""),
		[]byte("\r\n"),
		[]byte("\r\n\r\n\r\n"),
		[]byte("a"),
		[]byte("a\r\n"),
		[]byte("a\r\nb\r\n"),
		[]byte("a\r\n\r\nb\r\n"),
		[]byte("a\r\n\r\n\r\n"),
		[]byte("Hello: Foo\r\n\r\n"),
		[]byte("line one\r\nline two\r\n\r\n\r\n"),
		[]byte("no final newline"),
		[]byte("  leading and trailing  \r\n\t tabby \t\r\n"),
		[]byte("a \t b\t\tc\r\n   \r\n d \r\n"),
		[]byte("\t \r\n\t\r\n"),
		[]byte("café \r\n"),
		append(bytes.Repeat([]byte("x"), 5000), []byte("\r\n\r\n")...),
	}

	cases := []struct {
		name string
		algo Algorithm
		ref  func([]byte) []byte
	}{
		{"simple", Simple, refSimple},
		{"relaxed", Relaxed, refRelaxed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, body := range bodies {
				if hasInvalidNewline(body) {
					continue
				}
				got := streamCanon(t, tc.algo, body)
				want := tc.ref(body)
				if !bytes.Equal(got, want) {
					t.Fatalf("body %q:\n got %q\nwant %q", body, got, want)
				}
			}
		})
	}
}

func TestIllegalNewlineRejected(t *testing.T) {
	illegal := [][]byte{
		[]byte("a\nb\r\n"),
		[]byte("a\rb\r\n"),
		[]byte("good\r\nbad\nb\r\n"),
		[]byte("dangling\r"),
		[]byte("bare\n"),
	}

	for _, algo := range []Algorithm{Simple, Relaxed} {
		for _, body := range illegal {
			var buf bytes.Buffer
			c := NewCanonicalizer(&buf, algo)
			_, werr := c.Write(body)
			cerr := c.Close()
			if !errors.Is(werr, ErrInvalidNewline) && !errors.Is(cerr, ErrInvalidNewline) {
				t.Fatalf("algo %v body %q: want ErrInvalidNewline, got write=%v close=%v",
					algo, body, werr, cerr)
			}
			if !errors.Is(c.Err(), ErrInvalidNewline) {
				t.Fatalf("sticky error not retained for %q", body)
			}
		}
	}

	t.Run("split CRLF across writes", func(t *testing.T) {
		var good bytes.Buffer
		c := NewCanonicalizer(&good, Simple)
		if _, err := c.Write([]byte("a\r")); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Write([]byte("\nb\r\n")); err != nil {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if got, want := good.String(), "a\r\nb\r\n"; got != want {
			t.Fatalf("got %q want %q", got, want)
		}

		for _, second := range [][]byte{[]byte("x"), []byte("\r"), nil} {
			var bad bytes.Buffer
			c2 := NewCanonicalizer(&bad, Simple)
			if _, err := c2.Write([]byte("a\r")); err != nil {
				t.Fatal(err)
			}
			if _, err := c2.Write(second); err != nil && !errors.Is(err, ErrInvalidNewline) {
				t.Fatalf("want ErrInvalidNewline, got %v", err)
			}
			if err := c2.Close(); !errors.Is(err, ErrInvalidNewline) {
				t.Fatalf("second=%q: want ErrInvalidNewline from Close, got %v", second, err)
			}
		}
	})
}

func TestBodyLengthLimit(t *testing.T) {
	body := []byte("alpha\r\nbeta\r\ngamma\r\n")

	t.Run("output is a prefix of the unlimited stream", func(t *testing.T) {
		full, err := Canonicalize(Simple, body)
		if err != nil {
			t.Fatal(err)
		}
		for _, limit := range []int64{0, 1, 2, 5, 6, 7, 8, 13, int64(len(full)) - 1, int64(len(full))} {
			want := full
			if int64(len(want)) > limit {
				want = want[:limit]
			}
			oneShot, err := Canonicalize(Simple, body, WithBodyLength(limit))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(oneShot, want) {
				t.Fatalf("limit=%d one-shot:\n got %q\nwant %q", limit, oneShot, want)
			}
			streamed := streamCanon(t, Simple, body, WithBodyLength(limit))
			if !bytes.Equal(streamed, want) {
				t.Fatalf("limit=%d streamed:\n got %q\nwant %q", limit, streamed, want)
			}
		}
	})

	t.Run("truncation flag and emitted count", func(t *testing.T) {
		full, _ := Canonicalize(Relaxed, body)
		const limit int64 = 4
		var buf bytes.Buffer
		c := NewRelaxed(&buf, WithBodyLength(limit))
		if err := writeChunks(c, body, 3); err != nil {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if !c.Truncated() {
			t.Fatal("expected Truncated()=true")
		}
		if c.Emitted() != limit {
			t.Fatalf("Emitted=%d want %d", c.Emitted(), limit)
		}
		if int64(buf.Len()) != limit || !bytes.HasPrefix(full, buf.Bytes()) {
			t.Fatalf("truncated output %q is not a %d-byte prefix of %q", buf.Bytes(), limit, full)
		}

		big, err := Canonicalize(Relaxed, body, WithBodyLength(int64(len(full))+10))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(big, full) {
			t.Fatalf("limit above full size altered output: %q vs %q", big, full)
		}
	})

	t.Run("limit zero emits nothing", func(t *testing.T) {
		got, err := Canonicalize(Simple, body, WithBodyLength(0))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("got %q, want empty", got)
		}
	})
}

type discardSink struct{}

func (discardSink) Write(p []byte) (int, error) { return len(p), nil }

func TestTrailingEmptyLinesAreBoundedMemory(t *testing.T) {
	// The pending-line counter replaces buffering: feeding 100k trailing
	// CRLFs one byte at a time must perform zero heap allocations while they
	// remain the undelivered tail. The single allocation measured here is
	// the one-time Canonicalizer construction; the write/close loop adds none.
	cr := []byte{'\r'}
	lf := []byte{'\n'}
	allocs := testing.AllocsPerRun(20, func() {
		c := NewSimple(discardSink{})
		for i := 0; i < 100000; i++ {
			if _, err := c.Write(cr); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Write(lf); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 1 {
		t.Fatalf("trailing CRLF path allocated %v times per run; want 1 (constructor only)", allocs)
	}

	// Any number of trailing blank lines reduces to the same single CRLF.
	for _, n := range []int{0, 1, 2, 5, 1000} {
		body := append([]byte("x\r\n"), bytes.Repeat([]byte("\r\n"), n)...)
		got, err := Canonicalize(Simple, body)
		if err != nil {
			t.Fatal(err)
		}
		if want := "x\r\n"; string(got) != want {
			t.Fatalf("n=%d got %q want %q", n, got, want)
		}
	}

	// Interior blank lines must be preserved even in large numbers.
	interior := append(append([]byte("a\r\n"), bytes.Repeat([]byte("\r\n"), 1000)...), 'b')
	got, err := Canonicalize(Simple, interior)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte("a\r\n"), bytes.Repeat([]byte("\r\n"), 1000)...), []byte("b\r\n")...)
	if !bytes.Equal(got, want) {
		t.Fatalf("interior blank lines were lost: len got=%d want=%d", len(got), len(want))
	}
}

func TestFuzzCanonicalizationMatchesReference(t *testing.T) {
	alphabet := []byte("ab \t\r\nx")
	rng := func(seed, n int) []byte {
		out := make([]byte, n)
		for i := range out {
			seed = seed*1103515245 + 12345
			out[i] = alphabet[int(uint32(seed)>>16)%len(alphabet)]
		}
		return out
	}

	for seed := 0; seed < 300; seed++ {
		body := rng(seed+1, 37+seed%40)
		if hasInvalidNewline(body) {
			continue
		}
		if got := streamCanon(t, Simple, body); !bytes.Equal(got, refSimple(body)) {
			t.Fatalf("simple seed=%d body=%q:\n got %q\nwant %q", seed, body, got, refSimple(body))
		}
		if got := streamCanon(t, Relaxed, body); !bytes.Equal(got, refRelaxed(body)) {
			t.Fatalf("relaxed seed=%d body=%q:\n got %q\nwant %q", seed, body, got, refRelaxed(body))
		}
	}
}
