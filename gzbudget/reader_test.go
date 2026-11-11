package gzbudget

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
	"testing"
)

// countReader counts bytes and calls pulled from a source.
type countReader struct {
	r     io.Reader
	n     int64
	reads int
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	c.reads++
	return n, err
}

// chunkReader delivers the source in fixed-size blocks to simulate member
// headers and trailers spanning read blocks.
type chunkReader struct {
	data []byte
	step int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := c.step
	if n > len(c.data) {
		n = len(c.data)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

// countWriter counts bytes accepted by a destination.
type countWriter struct{ n int64 }

func (w *countWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

func gzipMember(t *testing.T, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildStream(t *testing.T, payloads []string) (stream []byte, members [][]byte) {
	t.Helper()
	for _, p := range payloads {
		m := gzipMember(t, p)
		members = append(members, m)
		stream = append(stream, m...)
	}
	return stream, members
}

func TestMultiMemberAccounting(t *testing.T) {
	payloads := []string{"hello", "world!", strings.Repeat("a", 1000)}
	stream, members := buildStream(t, payloads)

	src := &countReader{r: bytes.NewReader(stream)}
	r := NewReader(src, Limits{})
	dst := &countWriter{}
	if _, err := io.Copy(dst, r); err != nil {
		t.Fatalf("copy: %v", err)
	}

	stats := r.Members()
	if len(stats) != len(payloads) {
		t.Fatalf("got %d members, want %d", len(stats), len(payloads))
	}
	var wantOut int64
	for i, st := range stats {
		if st.Index != i {
			t.Errorf("member %d: index = %d", i, st.Index)
		}
		if st.CompressedBytes != int64(len(members[i])) {
			t.Errorf("member %d: compressed = %d, want %d", i, st.CompressedBytes, len(members[i]))
		}
		if st.DecompressedBytes != int64(len(payloads[i])) {
			t.Errorf("member %d: decompressed = %d, want %d", i, st.DecompressedBytes, len(payloads[i]))
		}
		if i > 0 && st.CompressedOffset != stats[i-1].CompressedOffset+stats[i-1].CompressedBytes {
			t.Errorf("member %d: offset %d not contiguous", i, st.CompressedOffset)
		}
		wantOut += int64(len(payloads[i]))
	}
	if dst.n != wantOut || r.TotalOutput() != wantOut {
		t.Errorf("output = %d/%d, want %d", dst.n, r.TotalOutput(), wantOut)
	}
	if r.CompressedConsumed() != int64(len(stream)) {
		t.Errorf("consumed = %d, want %d", r.CompressedConsumed(), len(stream))
	}
	if src.n != int64(len(stream)) {
		t.Errorf("source pulled = %d, want %d", src.n, len(stream))
	}
}

func TestSpanningBlocks(t *testing.T) {
	payloads := []string{"header-splits", strings.Repeat("b", 500), "trailer-splits"}
	stream, members := buildStream(t, payloads)

	// 3-byte blocks guarantee headers/trailers span read boundaries.
	src := &countReader{r: &chunkReader{data: stream, step: 3}}
	r := NewReader(src, Limits{})
	dst := &countWriter{}
	if _, err := io.Copy(dst, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	stats := r.Members()
	for i, st := range stats {
		if st.CompressedBytes != int64(len(members[i])) {
			t.Errorf("member %d: compressed = %d, want %d", i, st.CompressedBytes, len(members[i]))
		}
		if st.DecompressedBytes != int64(len(payloads[i])) {
			t.Errorf("member %d: decompressed = %d, want %d", i, st.DecompressedBytes, len(payloads[i]))
		}
	}
}

func TestBudgetExceededNoExtraExpansion(t *testing.T) {
	payloads := []string{strings.Repeat("x", 100), strings.Repeat("y", 100), strings.Repeat("z", 100)}
	stream, _ := buildStream(t, payloads)

	src := &countReader{r: bytes.NewReader(stream)}
	r := NewReader(src, Limits{MaxTotalOutput: 150})
	dst := &countWriter{}
	_, err := io.Copy(dst, r)

	var gerr *Error
	if !errors.As(err, &gerr) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if gerr.Kind != KindBudget || !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("kind = %v, want budget", gerr.Kind)
	}
	if gerr.Member != 1 {
		t.Fatalf("member = %d, want 1", gerr.Member)
	}
	if dst.n != 150 {
		t.Fatalf("output = %d, want exactly the 150 byte budget", dst.n)
	}

	// After refusal: repeated reads must return the same sticky error and
	// neither expand nor pull another byte.
	srcBytes, srcReads := src.n, src.reads
	consumed := r.CompressedConsumed()
	buf := make([]byte, 64)
	for i := 0; i < 3; i++ {
		n, err := r.Read(buf)
		if n != 0 || err != gerr {
			t.Fatalf("read %d after refusal: n=%d err=%v", i, n, err)
		}
	}
	if src.n != srcBytes || src.reads != srcReads {
		t.Errorf("source touched after refusal: bytes %d to %d, reads %d to %d",
			srcBytes, src.n, srcReads, src.reads)
	}
	if r.CompressedConsumed() != consumed {
		t.Errorf("consumed after refusal: %d to %d", consumed, r.CompressedConsumed())
	}
	if r.TotalOutput() != 150 {
		t.Errorf("output after refusal = %d, want 150", r.TotalOutput())
	}
}

func TestBudgetExactFitIsCleanEOF(t *testing.T) {
	payloads := []string{"abc", "defgh"}
	stream, _ := buildStream(t, payloads)

	r := NewReader(bytes.NewReader(stream), Limits{MaxTotalOutput: 8})
	dst := &countWriter{}
	if _, err := io.Copy(dst, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if dst.n != 8 {
		t.Fatalf("output = %d, want 8", dst.n)
	}
}

func TestDepthLimit(t *testing.T) {
	payloads := []string{"one", "two", "three"}
	stream, members := buildStream(t, payloads)

	r := NewReader(bytes.NewReader(stream), Limits{MaxMembers: 2})
	dst := &countWriter{}
	_, err := io.Copy(dst, r)

	var gerr *Error
	if !errors.As(err, &gerr) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if gerr.Kind != KindDepth || !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("kind = %v, want depth", gerr.Kind)
	}
	if gerr.Member != 2 {
		t.Fatalf("member = %d, want 2", gerr.Member)
	}
	wantOff := int64(len(members[0]) + len(members[1]))
	if gerr.Offset != wantOff {
		t.Fatalf("offset = %d, want %d", gerr.Offset, wantOff)
	}
	if dst.n != int64(len("one")+len("two")) {
		t.Fatalf("output = %d, want only the first two members", dst.n)
	}
}

func TestCorruptChecksumLocated(t *testing.T) {
	payloads := []string{strings.Repeat("q", 200), "ok"}
	stream, _ := buildStream(t, payloads)
	// Flip a CRC32 byte in the first member's trailer.
	first := gzipMember(t, payloads[0])
	stream[len(first)-8] ^= 0xff

	src := &countReader{r: bytes.NewReader(stream)}
	r := NewReader(src, Limits{})
	dst := &countWriter{}
	_, err := io.Copy(dst, r)

	var gerr *Error
	if !errors.As(err, &gerr) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if gerr.Kind != KindCorrupt {
		t.Fatalf("kind = %v, want corrupt", gerr.Kind)
	}
	if gerr.Member != 0 || gerr.Offset != 0 {
		t.Fatalf("member=%d offset=%d, want 0/0", gerr.Member, gerr.Offset)
	}
	if !errors.Is(err, gzip.ErrChecksum) {
		t.Fatalf("cause = %v, want gzip.ErrChecksum", err)
	}
	// Partial accounting of the corrupt member is still available.
	stats := r.Members()
	if len(stats) != 1 || stats[0].CompressedBytes == 0 {
		t.Fatalf("stats = %+v, want one partially accounted member", stats)
	}

	// No further expansion after the failure.
	consumed := r.CompressedConsumed()
	if n, err := r.Read(make([]byte, 16)); n != 0 || err != gerr {
		t.Fatalf("read after failure: n=%d err=%v", n, err)
	}
	if r.CompressedConsumed() != consumed {
		t.Errorf("consumed changed after failure")
	}
}

func TestCorruptHeaderLocated(t *testing.T) {
	payloads := []string{"good", "bad"}
	stream, members := buildStream(t, payloads)
	// Corrupt the magic bytes of the second member's header.
	stream[len(members[0])] = 0x00

	r := NewReader(bytes.NewReader(stream), Limits{})
	dst := &countWriter{}
	_, err := io.Copy(dst, r)

	var gerr *Error
	if !errors.As(err, &gerr) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if gerr.Kind != KindCorrupt || gerr.Member != 1 {
		t.Fatalf("kind=%v member=%d, want corrupt/1", gerr.Kind, gerr.Member)
	}
	if gerr.Offset != int64(len(members[0])) {
		t.Fatalf("offset = %d, want %d", gerr.Offset, len(members[0]))
	}
	if dst.n != int64(len("good")) {
		t.Fatalf("output = %d, want only the intact member", dst.n)
	}
}

func TestTruncatedStreamLocated(t *testing.T) {
	payloads := []string{"first", strings.Repeat("w", 300)}
	stream, members := buildStream(t, payloads)
	// Cut the second member mid-body.
	stream = stream[:len(members[0])+len(members[1])/2]

	r := NewReader(&chunkReader{data: stream, step: 7}, Limits{})
	_, err := io.Copy(&countWriter{}, r)

	var gerr *Error
	if !errors.As(err, &gerr) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if gerr.Kind != KindCorrupt || gerr.Member != 1 {
		t.Fatalf("kind=%v member=%d, want corrupt/1", gerr.Kind, gerr.Member)
	}
	if gerr.Offset != int64(len(members[0])) {
		t.Fatalf("offset = %d, want %d", gerr.Offset, len(members[0]))
	}
}

func TestStrictLimitTinyReads(t *testing.T) {
	payloads := []string{strings.Repeat("s", 1000)}
	stream, _ := buildStream(t, payloads)

	r := NewReader(bytes.NewReader(stream), Limits{MaxTotalOutput: 10})
	var total int64
	buf := make([]byte, 1) // force one-byte expansions
	for {
		n, err := r.Read(buf)
		total += int64(n)
		if err != nil {
			var gerr *Error
			if !errors.As(err, &gerr) || gerr.Kind != KindBudget {
				t.Fatalf("err = %v, want budget error", err)
			}
			break
		}
	}
	if total != 10 {
		t.Fatalf("expanded %d bytes, want exactly 10", total)
	}
}

func TestEmptyStream(t *testing.T) {
	r := NewReader(bytes.NewReader(nil), Limits{})
	if n, err := r.Read(make([]byte, 8)); n != 0 || err != io.EOF {
		t.Fatalf("n=%d err=%v, want 0/EOF", n, err)
	}
	if len(r.Members()) != 0 {
		t.Fatalf("members = %v, want none", r.Members())
	}
}
