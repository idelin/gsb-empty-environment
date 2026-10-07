package stresc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func runDecoder(t *testing.T, sep byte, stream []byte, chunks ...[]byte) ([]Record, []Error) {
	t.Helper()
	d := NewDecoder(sep)
	var recs []Record
	var errs []Error
	for _, c := range chunks {
		r, e := d.Feed(c)
		recs = append(recs, r...)
		errs = append(errs, e...)
	}
	r, e := d.Finish()
	recs = append(recs, r...)
	errs = append(errs, e...)
	if d.Processed() != len(stream) {
		t.Fatalf("Processed() = %d, want %d", d.Processed(), len(stream))
	}
	return recs, errs
}

func sameRecords(a, b []Record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameErrors(a, b []Error) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDecoderSplitPoints feeds the stream at every possible chunking
// and requires identical records, error ranges, and consumed counts.
// The records include escapes, \uXXXX sequences, surrogate pairs, and
// literal multi-byte runes so that every construct is split across
// chunks at some point.
func TestDecoderSplitPoints(t *testing.T) {
	streams := [][]byte{
		[]byte(strings.Join([]string{
			`"hello"`,
			`"esc \n\t\\\" \x01\x1f end"`,
			`"unicode é世😀"`,
			`"A😀世"`,
			`"😀literal"`,
			`"x"`,
		}, "\n") + "\n"),
		[]byte(strings.Join([]string{
			`"ok1"`,
			`"bad \q esc"`,
			`"ok2 \uD83D\uDE00"`,
			`"bad2 \xzz"`,
			`"ok3"`,
		}, "\n") + "\n"),
	}
	for si, stream := range streams {
		wantRecs, wantErrs := runDecoder(t, '\n', stream, stream)
		if len(wantErrs) != 0 && si == 0 {
			t.Fatalf("stream %d: unexpected errors %v", si, wantErrs)
		}
		// All 2-chunk splits.
		for i := 0; i <= len(stream); i++ {
			recs, errs := runDecoder(t, '\n', stream, stream[:i], stream[i:])
			if !sameRecords(recs, wantRecs) {
				t.Fatalf("stream %d split %d: records %v, want %v", si, i, recs, wantRecs)
			}
			if !sameErrors(errs, wantErrs) {
				t.Fatalf("stream %d split %d: errors %v, want %v", si, i, errs, wantErrs)
			}
		}
		// All 3-chunk splits.
		for i := 0; i <= len(stream); i++ {
			for j := i; j <= len(stream); j++ {
				recs, errs := runDecoder(t, '\n', stream, stream[:i], stream[i:j], stream[j:])
				if !sameRecords(recs, wantRecs) {
					t.Fatalf("stream %d splits %d,%d: records %v, want %v", si, i, j, recs, wantRecs)
				}
				if !sameErrors(errs, wantErrs) {
					t.Fatalf("stream %d splits %d,%d: errors %v, want %v", si, i, j, errs, wantErrs)
				}
			}
		}
	}
}

func TestDecoderErrorRecovery(t *testing.T) {
	stream := "\"ok1\"\n\"bad \\q esc\"\n\"ok2\"\n\"bad2 \\xzz\"\n\"ok3\"\n"
	recs, errs := runDecoder(t, '\n', []byte(stream), []byte(stream))

	wantVals := []string{"ok1", "ok2", "ok3"}
	if len(recs) != len(wantVals) {
		t.Fatalf("records %v, want %d values", recs, len(wantVals))
	}
	for i, v := range wantVals {
		if recs[i].Value != v {
			t.Fatalf("record %d = %q, want %q", i, recs[i].Value, v)
		}
	}
	if recs[0].Start != 0 || recs[0].End != 5 {
		t.Fatalf("record 0 range [%d,%d), want [0,5)", recs[0].Start, recs[0].End)
	}
	if len(errs) != 2 {
		t.Fatalf("errors %v, want 2", errs)
	}
	if off := strings.Index(stream, "q esc"); errs[0].Offset != off {
		t.Fatalf("error 0 offset %d, want %d", errs[0].Offset, off)
	}
	if off := strings.Index(stream, "zz"); errs[1].Offset != off {
		t.Fatalf("error 1 offset %d, want %d", errs[1].Offset, off)
	}
}

// TestDecoderRecoverySepInsideEscape verifies that a separator byte
// occurring inside a legal escape sequence is not a recovery boundary.
func TestDecoderRecoverySepInsideEscape(t *testing.T) {
	// The separator '1' appears inside the legal escape \x41 of the
	// bad record; recovery must skip the whole escape.
	stream := `"ok"1"bad \q A\x41B tail"1"next"1`
	recs, errs := runDecoder(t, '1', []byte(stream), []byte(stream))
	if len(errs) != 1 {
		t.Fatalf("errors %v, want exactly 1", errs)
	}
	if off := strings.Index(stream, "q A"); errs[0].Offset != off {
		t.Fatalf("error offset %d, want %d", errs[0].Offset, off)
	}
	if len(recs) != 2 || recs[0].Value != "ok" || recs[1].Value != "next" {
		t.Fatalf("records %v, want ok,next", recs)
	}

	// The separator 'n' appears inside the \n escape of the bad
	// record and inside a good record's text.
	stream2 := `"a\nb"n"bad \q x\n y"n"next"n`
	recs2, errs2 := runDecoder(t, 'n', []byte(stream2), []byte(stream2))
	if len(errs2) != 1 {
		t.Fatalf("errors %v, want exactly 1", errs2)
	}
	if len(recs2) != 2 || recs2[0].Value != "a\nb" || recs2[1].Value != "next" {
		t.Fatalf("records %v, want a\\nb,next", recs2)
	}
}

func TestDecoderTruncated(t *testing.T) {
	d := NewDecoder('\n')
	recs, errs := d.Feed([]byte("\"good\"\n\"bad \\x1"))
	if len(recs) != 1 || recs[0].Value != "good" {
		t.Fatalf("records %v, want [good]", recs)
	}
	if len(errs) != 0 {
		t.Fatalf("errors %v, want none before Finish", errs)
	}
	recs, errs = d.Finish()
	if len(recs) != 0 {
		t.Fatalf("records %v, want none", recs)
	}
	if len(errs) != 1 || errs[0].Offset != 7 || !strings.Contains(errs[0].Msg, "truncated") {
		t.Fatalf("errors %v, want truncated at 7", errs)
	}
}

func TestDecoderMissingSeparator(t *testing.T) {
	recs, errs := runDecoder(t, '\n', []byte("\"a\"x\n\"b\"\n"), []byte("\"a\"x\n\"b\"\n"))
	if len(recs) != 2 || recs[0].Value != "a" || recs[1].Value != "b" {
		t.Fatalf("records %v, want a,b", recs)
	}
	if len(errs) != 1 || errs[0].Offset != 3 || !strings.Contains(errs[0].Msg, "separator") {
		t.Fatalf("errors %v, want separator error at 3", errs)
	}
}

// TestEncoderSplitPoints writes the input at every possible split and
// requires identical output and consumed counts. Splits inside
// multi-byte runes exercise the partial-rune tail.
func TestEncoderSplitPoints(t *testing.T) {
	inputs := []string{
		"plain text",
		"escapes \" \\ \n \x00 \x7f \t",
		"unicode é世😀\U0001F600",
		"mixed é\n😀\\end\x01",
	}
	for _, in := range inputs {
		var oneShot bytes.Buffer
		e0 := NewEncoder(nil, &oneShot)
		n, err := e0.Write([]byte(in))
		if err != nil || n != len(in) {
			t.Fatalf("one-shot Write: n=%d err=%v", n, err)
		}
		if err := e0.Close(); err != nil {
			t.Fatalf("one-shot Close: %v", err)
		}
		want := oneShot.Bytes()
		if string(want) != string(Encode(nil, in)) {
			t.Fatalf("stream encoding %q != Encode %q", want, Encode(nil, in))
		}
		for i := 0; i <= len(in); i++ {
			var buf bytes.Buffer
			e := NewEncoder(nil, &buf)
			n1, err1 := e.Write([]byte(in[:i]))
			n2, err2 := e.Write([]byte(in[i:]))
			err3 := e.Close()
			if err1 != nil || err2 != nil || err3 != nil {
				t.Fatalf("split %d of %q: errs %v %v %v", i, in, err1, err2, err3)
			}
			if n1+n2 != len(in) || e.Consumed() != len(in) {
				t.Fatalf("split %d of %q: consumed %d+%d, want %d", i, in, n1, n2, len(in))
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Fatalf("split %d of %q: %q != %q", i, in, buf.Bytes(), want)
			}
		}
	}
}

// shortWriter accepts at most max bytes per Write call.
type shortWriter struct {
	buf bytes.Buffer
	max int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		p = p[:w.max]
	}
	return w.buf.Write(p)
}

func TestEncoderBackpressure(t *testing.T) {
	in := strings.Repeat("é世😀\n\"\\", 50)
	var want bytes.Buffer
	e0 := NewEncoder(nil, &want)
	if n, err := e0.Write([]byte(in)); err != nil || n != len(in) {
		t.Fatalf("one-shot: n=%d err=%v", n, err)
	}
	if err := e0.Close(); err != nil {
		t.Fatalf("one-shot Close: %v", err)
	}

	w := &shortWriter{max: 3}
	e := NewEncoder(nil, w)
	e.SetMaxBuffer(8)
	rest := []byte(in)
	for len(rest) > 0 {
		n, err := e.Write(rest)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if n == 0 {
			t.Fatal("Write made no progress under backpressure")
		}
		rest = rest[n:]
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if w.buf.String() != want.String() {
		t.Fatalf("backpressured output differs:\n%q\n%q", w.buf.String(), want.String())
	}
	if e.Consumed() != len(in) {
		t.Fatalf("Consumed() = %d, want %d", e.Consumed(), len(in))
	}
}

func TestEncoderCancel(t *testing.T) {
	in := []byte(strings.Repeat("abc ", 100))

	// Canceled before any write: input is consumed into the buffer,
	// but flushing fails.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := NewEncoder(ctx, &bytes.Buffer{})
	n, err := e.Write(in)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Write err = %v, want context.Canceled", err)
	}
	if n != len(in) || e.Consumed() != len(in) {
		t.Fatalf("consumed %d (%d), want %d", n, e.Consumed(), len(in))
	}
	if err := e.Close(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close err = %v, want context.Canceled", err)
	}

	// Canceled mid-stream: first write succeeds, second fails.
	ctx2, cancel2 := context.WithCancel(context.Background())
	var out bytes.Buffer
	e2 := NewEncoder(ctx2, &out)
	if n, err := e2.Write(in[:50]); err != nil || n != 50 {
		t.Fatalf("first Write: n=%d err=%v", n, err)
	}
	cancel2()
	if _, err := e2.Write(in[50:]); !errors.Is(err, context.Canceled) {
		t.Fatalf("second Write err = %v, want context.Canceled", err)
	}
	if e2.Consumed() != len(in) {
		t.Fatalf("Consumed() = %d, want %d", e2.Consumed(), len(in))
	}
}

func TestEncoderInvalidUTF8(t *testing.T) {
	e := NewEncoder(nil, io.Discard)
	n, err := e.Write([]byte{'a', 0xff, 'b'})
	if n != 1 {
		t.Fatalf("consumed %d, want 1", n)
	}
	var eerr *Error
	if !errors.As(err, &eerr) || eerr.Offset != 1 {
		t.Fatalf("err = %v, want *Error at offset 1", err)
	}

	// A truncated multi-byte sequence at Close is an error too.
	e2 := NewEncoder(nil, io.Discard)
	if n, err := e2.Write([]byte{0xF0, 0x9F}); err != nil || n != 2 {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	if err := e2.Close(); err == nil {
		t.Fatal("Close: expected truncated UTF-8 error")
	}
}
