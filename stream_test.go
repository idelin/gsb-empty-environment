package qstr

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestStreamDecoderCutPoints feeds the same stream split at every byte
// offset (and byte-by-byte) and requires identical records, error ranges
// and consumed-byte checkpoints regardless of where chunks are cut.
func TestStreamDecoderCutPoints(t *testing.T) {
	const sep = '\n'
	var stream strings.Builder
	var wantRecords []string
	type wantErr struct {
		start, end int
		msg        string
	}
	var wantErrs []wantErr
	add := func(s string) int {
		off := stream.Len()
		stream.WriteString(s)
		return off
	}

	add(Encode("hello\nworld"))
	add("\n")
	wantRecords = append(wantRecords, "hello\nworld")

	// Surrogate pair escape and \u escapes inside a record.
	add(`"\uD83D\uDE00 pair \u0041"`)
	add("\n")
	wantRecords = append(wantRecords, "😀 pair A")

	add(Encode(`literal \u0041 and "q" and 中😀`))
	add("\n")
	wantRecords = append(wantRecords, `literal \u0041 and "q" and 中😀`)

	// Bad record: invalid hex digit -> error, then resync at separator.
	off := add(`"bad\u00g0"`)
	add("\n")
	wantErrs = append(wantErrs, wantErr{off + 8, off + 9, "invalid hex digit"})

	// Garbage without an opening quote -> error, then resync.
	off = add("junk")
	add("\n")
	wantErrs = append(wantErrs, wantErr{off, off + 1, "expected opening quote"})

	// Truncated record at EOF -> unterminated error at Close.
	off = add(`"tail\u00`)
	wantErrs = append(wantErrs, wantErr{off + 5, off + 9, `truncated \u escape`})

	full := stream.String()

	check := func(t *testing.T, d *StreamDecoder) {
		t.Helper()
		if !reflect.DeepEqual(d.Records(), wantRecords) {
			t.Fatalf("records %q, want %q", d.Records(), wantRecords)
		}
		if len(d.Errors()) != len(wantErrs) {
			t.Fatalf("errors %v, want %d", d.Errors(), len(wantErrs))
		}
		for i, we := range wantErrs {
			got := d.Errors()[i]
			if got.Start != we.start || got.End != we.end || got.Msg != we.msg {
				t.Fatalf("error %d = [%d,%d) %q, want [%d,%d) %q",
					i, got.Start, got.End, got.Msg, we.start, we.end, we.msg)
			}
		}
		if d.Checkpoint() != len(full) {
			t.Fatalf("checkpoint %d, want %d (whole stream consumed)", d.Checkpoint(), len(full))
		}
	}

	// One shot.
	d := NewStreamDecoder(sep)
	if err := d.Write([]byte(full)); err != nil {
		t.Fatal(err)
	}
	d.Close()
	check(t, d)

	// Every two-chunk cut point, including cuts inside escapes and
	// surrogate pairs.
	for cut := 0; cut <= len(full); cut++ {
		d := NewStreamDecoder(sep)
		if err := d.Write([]byte(full[:cut])); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if err := d.Write([]byte(full[cut:])); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		d.Close()
		check(t, d)
	}

	// Byte at a time.
	d = NewStreamDecoder(sep)
	for i := 0; i < len(full); i++ {
		if err := d.Write([]byte(full[i : i+1])); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()
	check(t, d)
}

// TestStreamDecoderCheckpoint verifies the checkpoint advances exactly past
// consumed input and stalls on incomplete records.
func TestStreamDecoderCheckpoint(t *testing.T) {
	d := NewStreamDecoder('\n')
	d.Write([]byte(`"ab"`))
	if got := d.Checkpoint(); got != 4 {
		t.Fatalf("checkpoint %d, want 4", got)
	}
	d.Write([]byte("\n"))
	if got := d.Checkpoint(); got != 5 {
		t.Fatalf("checkpoint %d, want 5", got)
	}
	d.Write([]byte(`"cd\u00`)) // incomplete escape: nothing more consumed
	if got := d.Checkpoint(); got != 5 {
		t.Fatalf("checkpoint %d, want 5", got)
	}
	d.Write([]byte(`41"`)) // completes \u0041 -> "cdA"
	if got := d.Checkpoint(); got != 15 {
		t.Fatalf("checkpoint %d, want 15", got)
	}
	d.Close()
	if !reflect.DeepEqual(d.Records(), []string{"ab", "cdA"}) {
		t.Fatalf("records %q", d.Records())
	}
	if len(d.Errors()) != 0 {
		t.Fatalf("errors %v", d.Errors())
	}
}

// TestStreamDecoderSeparatorInsideEscape verifies that during error
// recovery a separator occurring inside a legal escape sequence is not
// treated as a record boundary.
func TestStreamDecoderSeparatorInsideEscape(t *testing.T) {
	const sep = 'A'
	// After the bad record, resync must skip the 'A' inside \u0041 and
	// stop only at the standalone separator.
	stream := `"one"` + "A" + `garbage\u0041more` + "A" + `"two"`
	d := NewStreamDecoder(sep)
	d.Write([]byte(stream))
	d.Close()
	if !reflect.DeepEqual(d.Records(), []string{"one", "two"}) {
		t.Fatalf("records %q, want [one two]", d.Records())
	}
	errs := d.Errors()
	if len(errs) != 1 {
		t.Fatalf("errors %v, want exactly 1", errs)
	}
	if errs[0].Start != 6 || errs[0].End != 7 || errs[0].Msg != "expected opening quote" {
		t.Fatalf("error = %+v", errs[0])
	}
	if d.Checkpoint() != len(stream) {
		t.Fatalf("checkpoint %d, want %d", d.Checkpoint(), len(stream))
	}

	// Same property with the separator inside a surrogate pair escape.
	stream2 := `"x"` + "A" + `junk\uD83D\uDE00junk` + "A" + `"y"`
	d2 := NewStreamDecoder(sep)
	d2.Write([]byte(stream2))
	d2.Close()
	if !reflect.DeepEqual(d2.Records(), []string{"x", "y"}) {
		t.Fatalf("records %q, want [x y]", d2.Records())
	}
	if len(d2.Errors()) != 1 {
		t.Fatalf("errors %v, want exactly 1", d2.Errors())
	}
}

// TestStreamEncoderCutPoints encodes the same input split at every byte
// offset (including cuts inside multi-byte UTF-8 characters) and requires
// output identical to the one-shot Encode.
func TestStreamEncoderCutPoints(t *testing.T) {
	s := "he\"llo\\\n\t\x00\x7f中😀é\\u0041 tail"
	want := Encode(s)
	for cut := 0; cut <= len(s); cut++ {
		enc := NewStreamEncoder(context.Background(), 4096)
		if _, err := enc.Write([]byte(s[:cut])); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if _, err := enc.Write([]byte(s[cut:])); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if err := enc.Close(); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		got, err := io.ReadAll(enc)
		if err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if string(got) != want {
			t.Fatalf("cut %d: got %q, want %q", cut, got, want)
		}
	}
	// Byte at a time.
	enc := NewStreamEncoder(context.Background(), 4096)
	for i := 0; i < len(s); i++ {
		if _, err := enc.Write([]byte(s[i : i+1])); err != nil {
			t.Fatal(err)
		}
	}
	enc.Close()
	got, _ := io.ReadAll(enc)
	if string(got) != want {
		t.Fatalf("bytewise: got %q, want %q", got, want)
	}
}

// TestStreamEncoderBackpressure verifies Write blocks while the bounded
// buffer is full and completes once the reader drains it, with content
// identical to one-shot Encode.
func TestStreamEncoderBackpressure(t *testing.T) {
	input := strings.Repeat("ab\"\\\n中😀", 40)
	enc := NewStreamEncoder(context.Background(), 16)

	var wrote int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		n, err := enc.Write([]byte(input))
		if err != nil {
			t.Errorf("Write: %v", err)
		}
		if n != len(input) {
			t.Errorf("Write consumed %d of %d", n, len(input))
		}
		atomic.StoreInt32(&wrote, 1)
	}()

	// With no reader the small buffer fills and Write must block.
	select {
	case <-done:
		t.Fatal("Write returned despite full buffer (no backpressure)")
	case <-time.After(50 * time.Millisecond):
	}
	if atomic.LoadInt32(&wrote) != 0 {
		t.Fatal("Write completed without a reader")
	}

	// Drain concurrently; the writer unblocks and finishes.
	type result struct {
		data []byte
		err  error
	}
	readCh := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(enc)
		readCh <- result{data, err}
	}()
	<-done
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	r := <-readCh
	if r.err != nil {
		t.Fatal(r.err)
	}
	if string(r.data) != Encode(input) {
		t.Fatalf("got %q, want %q", r.data, Encode(input))
	}
}

// TestStreamEncoderCancel verifies a Write blocked on backpressure is
// released by context cancellation.
func TestStreamEncoderCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	enc := NewStreamEncoder(ctx, 8)

	errCh := make(chan error, 1)
	go func() {
		_, err := enc.Write([]byte(strings.Repeat("data ", 100)))
		errCh <- err
	}()
	time.Sleep(50 * time.Millisecond) // let Write block on the full buffer
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Write err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Write not unblocked by cancellation")
	}

	// Buffered output is still drained, then Read reports cancellation.
	for {
		_, err := enc.Read(make([]byte, 64))
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Read err = %v, want context.Canceled", err)
			}
			break
		}
	}
}

// TestStreamRoundTrip pipes each encoded record into a StreamDecoder.
func TestStreamRoundTrip(t *testing.T) {
	inputs := []string{"", "plain", "esc\"aped\\\n\x00", "中😀é", `\u0041 literal`}
	var want []string
	dec := NewStreamDecoder('\n')
	for _, s := range inputs {
		want = append(want, s)
		enc := NewStreamEncoder(context.Background(), 16)
		go func(s string) {
			enc.Write([]byte(s))
			enc.Close()
		}(s)
		got, err := io.ReadAll(enc)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != Encode(s) {
			t.Fatalf("stream encode %q: got %q", s, got)
		}
		dec.Write(got)
		dec.Write([]byte("\n"))
	}
	dec.Close()
	if !reflect.DeepEqual(dec.Records(), want) {
		t.Fatalf("records %q, want %q", dec.Records(), want)
	}
	if len(dec.Errors()) != 0 {
		t.Fatalf("errors %v", dec.Errors())
	}
}
