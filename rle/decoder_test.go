package rle

import (
	"bytes"
	"context"
	"io"
	"testing"
)

// chunkReader delivers data in fixed-size blocks so multi-byte varints
// and records span underlying read boundaries.
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

func drain(t *testing.T, d *Decoder) ([]byte, error) {
	t.Helper()
	var out []byte
	buf := make([]byte, 256)
	for {
		n, err := d.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			return out, err
		}
	}
}

func TestZeroCountEmitsNothing(t *testing.T) {
	// {count=0, 'a'} is skipped; {count=3, 'b'} expands.
	src := &chunkReader{data: []byte{0x00, 'a', 0x03, 'b'}, step: 1}
	d := NewDecoder(context.Background(), src, Options{MaxBuffer: 2})
	out, err := drain(t, d)
	if err != io.EOF {
		t.Fatalf("err = %v, want EOF", err)
	}
	if string(out) != "bbb" {
		t.Fatalf("out = %q, want %q", out, "bbb")
	}
}

func TestNonShortestVarintAccepted(t *testing.T) {
	// count=2 as 0x82 0x00 (non-shortest), and count=0 as 0x80 0x00.
	src := &chunkReader{data: []byte{0x82, 0x00, 'x', 0x80, 0x00, 'q', 0x01, 'z'}, step: 1}
	d := NewDecoder(context.Background(), src, Options{MaxBuffer: 1})
	out, err := drain(t, d)
	if err != io.EOF {
		t.Fatalf("err = %v, want EOF", err)
	}
	if string(out) != "xxz" {
		t.Fatalf("out = %q, want %q", out, "xxz")
	}
}

func TestTruncatedVarint(t *testing.T) {
	src := &chunkReader{data: []byte{0x80}, step: 1}
	d := NewDecoder(context.Background(), src, Options{})
	if _, err := drain(t, d); err != io.ErrUnexpectedEOF {
		t.Fatalf("err = %v, want ErrUnexpectedEOF", err)
	}
}

func TestTruncatedValue(t *testing.T) {
	// Complete count but the value byte never arrives.
	src := &chunkReader{data: []byte{0x05}, step: 1}
	d := NewDecoder(context.Background(), src, Options{})
	if _, err := drain(t, d); err != io.ErrUnexpectedEOF {
		t.Fatalf("err = %v, want ErrUnexpectedEOF", err)
	}
}

func TestCountOverflow(t *testing.T) {
	// Ten-byte varint whose final byte exceeds the 64th bit.
	data := append(bytes.Repeat([]byte{0xff}, 9), 0x7f, 'v')
	d := NewDecoder(context.Background(), &chunkReader{data: data, step: 3}, Options{})
	if _, err := drain(t, d); err != ErrCountOverflow {
		t.Fatalf("err = %v, want ErrCountOverflow", err)
	}
}

func TestOutputLimitWithoutHugeAllocation(t *testing.T) {
	// count = 1<<40 would be a terabyte if buffered eagerly; the decoder
	// must emit incrementally and stop at the cumulative limit.
	var enc []byte
	for v := uint64(1) << 40; ; v >>= 7 {
		if v < 0x80 {
			enc = append(enc, byte(v))
			break
		}
		enc = append(enc, byte(v)|0x80)
	}
	enc = append(enc, 'a')
	d := NewDecoder(context.Background(), &chunkReader{data: enc, step: 2},
		Options{MaxOutput: 5, MaxBuffer: 3})
	out, err := drain(t, d)
	if err != ErrOutputLimit {
		t.Fatalf("err = %v, want ErrOutputLimit", err)
	}
	if string(out) != "aaaaa" {
		t.Fatalf("out = %q, want %q", out, "aaaaa")
	}
	// Sticky: further reads keep reporting the limit.
	if n, err := d.Read(make([]byte, 4)); n != 0 || err != ErrOutputLimit {
		t.Fatalf("sticky read = %d, %v; want 0, ErrOutputLimit", n, err)
	}
}

func TestMaxBufferCapsSingleRead(t *testing.T) {
	src := &chunkReader{data: []byte{0x0a, 'k'}, step: 8}
	d := NewDecoder(context.Background(), src, Options{MaxBuffer: 3})
	n, err := d.Read(make([]byte, 10))
	if err != nil || n != 3 {
		t.Fatalf("read = %d, %v; want 3, nil", n, err)
	}
}

func TestCountSpansBlocks(t *testing.T) {
	// count=300 encodes as 0xAC 0x02; deliver one byte per block.
	src := &chunkReader{data: []byte{0xac, 0x02, 'k'}, step: 1}
	d := NewDecoder(context.Background(), src, Options{MaxBuffer: 7})
	out, err := drain(t, d)
	if err != io.EOF {
		t.Fatalf("err = %v, want EOF", err)
	}
	if len(out) != 300 || string(out[:3]) != "kkk" {
		t.Fatalf("len(out) = %d, want 300 k's", len(out))
	}
}

func TestCancelReleasesState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := &chunkReader{data: []byte{0x0a, 'k', 0x0a, 'k'}, step: 1}
	d := NewDecoder(ctx, src, Options{MaxBuffer: 2})
	if _, err := d.Read(make([]byte, 2)); err != nil {
		t.Fatalf("first read: %v", err)
	}
	cancel()
	if _, err := d.Read(make([]byte, 2)); err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d.src != nil || d.haveRun || d.pending != 0 || d.shift != 0 {
		t.Fatalf("state not released after cancel: %+v", d)
	}
}
