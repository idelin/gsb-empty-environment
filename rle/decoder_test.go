package rle

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"math/rand"
	"testing"
)

// modelDecode is the deliberately small, unrolled reference decoder:
// no streaming, no incremental state, just a plain loop over the whole
// input applying the same format rules and limits. The streaming
// Decoder must agree with it byte for byte.
func modelDecode(data []byte, maxTotal int64) ([]byte, error) {
	var out []byte
	i := 0
	for {
		if i >= len(data) {
			return out, io.EOF
		}
		var count uint64
		var shift uint
		for {
			if i >= len(data) {
				return out, io.ErrUnexpectedEOF
			}
			b := data[i]
			i++
			if shift == 63 && b > 1 {
				return out, ErrCountOverflow
			}
			count |= uint64(b&0x7f) << shift
			if b&0x80 == 0 {
				break
			}
			shift += 7
		}
		if i >= len(data) {
			return out, io.ErrUnexpectedEOF
		}
		v := data[i]
		i++
		for k := uint64(0); k < count; k++ {
			if maxTotal > 0 && int64(len(out)) >= maxTotal {
				return out, ErrOutputLimit
			}
			out = append(out, v)
		}
	}
}

func varint(v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append([]byte(nil), buf[:n]...)
}

// nonMinimal encodes v with extra zero continuation groups appended,
// e.g. 5 -> 0x85 0x00 for extra=1.
func nonMinimal(v uint64, extra int) []byte {
	b := varint(v)
	out := make([]byte, 0, len(b)+extra)
	out = append(out, b[:len(b)-1]...)
	out = append(out, b[len(b)-1]|0x80)
	for i := 1; i < extra; i++ {
		out = append(out, 0x80)
	}
	return append(out, 0x00)
}

type pair struct {
	count uint64
	val   byte
}

func encode(ps ...pair) []byte {
	var out []byte
	for _, p := range ps {
		out = append(out, varint(p.count)...)
		out = append(out, p.val)
	}
	return out
}

// chunkReader feeds data in fixed-size chunks so counts are forced to
// span read boundaries when size is small.
type chunkReader struct {
	data []byte
	size int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := c.size
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

// drain reads until an error, accumulating all output.
func drain(t *testing.T, d *Decoder, bufSize int) ([]byte, error) {
	t.Helper()
	var out []byte
	buf := make([]byte, bufSize)
	for {
		n, err := d.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			return out, err
		}
		if n == 0 {
			t.Fatal("Read returned (0, nil)")
		}
	}
}

func TestBasicDecode(t *testing.T) {
	in := encode(pair{3, 'a'}, pair{2, 'b'}, pair{1, 'c'})
	d := NewDecoder(bytes.NewReader(in), Options{})
	out, err := drain(t, d, 64)
	if err != io.EOF {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if string(out) != "aaabbc" {
		t.Fatalf("out = %q, want %q", out, "aaabbc")
	}
}

func TestZeroCount(t *testing.T) {
	// Zero counts emit nothing but still consume their value byte.
	in := encode(pair{0, 'x'}, pair{0, 'y'}, pair{1, 'z'}, pair{0, 'w'})
	d := NewDecoder(&chunkReader{data: in, size: 1}, Options{})
	out, err := drain(t, d, 16)
	if err != io.EOF {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if string(out) != "z" {
		t.Fatalf("out = %q, want %q", out, "z")
	}
}

func TestNonMinimalCount(t *testing.T) {
	var in []byte
	in = append(in, nonMinimal(5, 1)...) // 0x85 0x00
	in = append(in, 'a')
	in = append(in, nonMinimal(0, 2)...) // zero count, padded
	in = append(in, 'x')
	in = append(in, nonMinimal(300, 2)...)
	in = append(in, 'b')
	d := NewDecoder(&chunkReader{data: in, size: 1}, Options{})
	out, err := drain(t, d, 1024)
	if err != io.EOF {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	want := append(bytes.Repeat([]byte{'a'}, 5), bytes.Repeat([]byte{'b'}, 300)...)
	if !bytes.Equal(out, want) {
		t.Fatalf("out length %d, want %d", len(out), len(want))
	}
}

func TestTruncation(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want []byte
		err  error
	}{
		{"empty", nil, nil, io.EOF},
		{"count without value", []byte{0x05}, nil, io.ErrUnexpectedEOF},
		{"unterminated varint", []byte{0x85}, nil, io.ErrUnexpectedEOF},
		{"unterminated long varint", []byte{0x80, 0x80, 0x80}, nil, io.ErrUnexpectedEOF},
		{"pair then dangling count", append(encode(pair{3, 'a'}), 0x02), []byte("aaa"), io.ErrUnexpectedEOF},
		{"clean boundary", encode(pair{2, 'q'}), []byte("qq"), io.EOF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(&chunkReader{data: tc.in, size: 1}, Options{})
			out, err := drain(t, d, 8)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if !bytes.Equal(out, tc.want) {
				t.Fatalf("out = %q, want %q", out, tc.want)
			}
		})
	}
}

func TestTruncationErrorOffset(t *testing.T) {
	in := append(encode(pair{3, 'a'}), 0x02) // 3 bytes; dangling count at end
	d := NewDecoder(bytes.NewReader(in), Options{})
	_, err := drain(t, d, 8)
	var perr *Error
	if !errors.As(err, &perr) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if perr.Offset != 3 {
		t.Fatalf("offset = %d, want 3", perr.Offset)
	}
}

func TestCountOverflow(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"tenth byte too large", append(bytes.Repeat([]byte{0xff}, 9), 0x02)},
		{"eleventh group", append(bytes.Repeat([]byte{0x80}, 9), 0x80, 0x01)},
		{"max then continuation", append(varint(math.MaxUint64)[:9], 0xff)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(&chunkReader{data: tc.in, size: 3}, Options{})
			_, err := drain(t, d, 8)
			if !errors.Is(err, ErrCountOverflow) {
				t.Fatalf("err = %v, want ErrCountOverflow", err)
			}
			var perr *Error
			if !errors.As(err, &perr) {
				t.Fatalf("err = %v, want *Error", err)
			}
			if perr.Offset != 10 {
				t.Fatalf("offset = %d, want 10", perr.Offset)
			}
		})
	}
}

func TestMaxUint64CountAccepted(t *testing.T) {
	// The largest legal count must not overflow; with an output limit
	// it decodes up to the limit instead.
	in := append(varint(math.MaxUint64), 'x')
	d := NewDecoder(bytes.NewReader(in), Options{MaxTotalBytes: 7})
	out, err := drain(t, d, 4)
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("err = %v, want ErrOutputLimit", err)
	}
	if !bytes.Equal(out, bytes.Repeat([]byte{'x'}, 7)) {
		t.Fatalf("out = %q", out)
	}
}

func TestOutputLimitNoHugeAllocation(t *testing.T) {
	// An astronomical count must stream out up to the limit and then
	// fail, without ever allocating count-sized memory.
	in := append(varint(math.MaxUint64), 'z')
	d := NewDecoder(bytes.NewReader(in), Options{
		MaxTotalBytes:  100,
		MaxBufferBytes: 16,
	})
	var out []byte
	buf := make([]byte, 64) // deliberately larger than MaxBufferBytes
	var err error
	for err == nil {
		var n int
		n, err = d.Read(buf)
		if n > 16 {
			t.Fatalf("Read produced %d bytes, over MaxBufferBytes 16", n)
		}
		out = append(out, buf[:n]...)
	}
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("err = %v, want ErrOutputLimit", err)
	}
	if len(out) != 100 || !bytes.Equal(out, bytes.Repeat([]byte{'z'}, 100)) {
		t.Fatalf("out length = %d, want 100 z's", len(out))
	}
	var perr *Error
	if !errors.As(err, &perr) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if want := int64(len(varint(math.MaxUint64)) + 1); perr.Offset != want {
		t.Fatalf("offset = %d, want %d", perr.Offset, want)
	}
	// Failure released the input window; only the fixed 512-byte
	// buffer ever existed, never anything count-sized.
	if d.in != nil {
		t.Fatal("decoder retained input buffer after limit failure")
	}
	if d.TotalEmitted() != 100 {
		t.Fatalf("TotalEmitted = %d, want 100", d.TotalEmitted())
	}
}

func TestExactLimitIsClean(t *testing.T) {
	// Output landing exactly on the limit is not an error.
	in := encode(pair{10, 'a'})
	d := NewDecoder(bytes.NewReader(in), Options{MaxTotalBytes: 10})
	out, err := drain(t, d, 3)
	if err != io.EOF {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if len(out) != 10 {
		t.Fatalf("out length = %d, want 10", len(out))
	}
}

func TestCancel(t *testing.T) {
	in := encode(pair{100, 'a'})
	d := NewDecoder(bytes.NewReader(in), Options{})
	buf := make([]byte, 10)
	n, err := d.Read(buf)
	if n != 10 || err != nil {
		t.Fatalf("first Read = %d, %v", n, err)
	}
	d.Cancel()
	if d.in != nil {
		t.Fatal("Cancel did not release the input buffer")
	}
	if n, err := d.Read(buf); n != 0 || err != ErrCanceled {
		t.Fatalf("Read after Cancel = %d, %v, want 0, ErrCanceled", n, err)
	}
}

func TestUnderlyingReaderError(t *testing.T) {
	boom := errors.New("boom")
	d := NewDecoder(&failReader{err: boom}, Options{})
	_, err := drain(t, d, 8)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	var perr *Error
	if !errors.As(err, &perr) {
		t.Fatalf("err = %v, want *Error", err)
	}
}

type failReader struct{ err error }

func (f *failReader) Read([]byte) (int, error) { return 0, f.err }

// TestModel cross-checks the streaming decoder against the unrolled
// reference model on randomized inputs, chunkings, buffer sizes and
// limits, covering zero counts, non-minimal encodings, truncation and
// overflow in combination.
func TestModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 5000; iter++ {
		data := genInput(rng)
		maxTotal := int64(1 + rng.Intn(2000))
		wantOut, wantErr := modelDecode(data, maxTotal)

		d := NewDecoder(
			&chunkReader{data: data, size: 1 + rng.Intn(7)},
			Options{
				MaxTotalBytes:  maxTotal,
				MaxBufferBytes: 1 + rng.Intn(64),
			},
		)
		var gotOut []byte
		buf := make([]byte, 1+rng.Intn(64))
		var gotErr error
		for gotErr == nil {
			var n int
			n, gotErr = d.Read(buf)
			gotOut = append(gotOut, buf[:n]...)
		}
		if !bytes.Equal(gotOut, wantOut) {
			t.Fatalf("iter %d: out mismatch: got %d bytes, want %d\ninput: % x",
				iter, len(gotOut), len(wantOut), data)
		}
		if !errors.Is(gotErr, wantErr) {
			t.Fatalf("iter %d: err = %v, want %v\ninput: % x",
				iter, gotErr, wantErr, data)
		}
	}
}

// genInput builds mostly-valid streams, then sometimes truncates them
// or appends garbage, so the model comparison exercises every failure
// path as well as clean decoding.
func genInput(rng *rand.Rand) []byte {
	var out []byte
	np := rng.Intn(6)
	for i := 0; i < np; i++ {
		var c uint64
		switch rng.Intn(10) {
		case 0:
			c = 0
		case 1:
			c = uint64(rng.Intn(3))
		case 2:
			c = uint64(rng.Intn(3000)) // likely to hit the output limit
		default:
			c = uint64(rng.Intn(50))
		}
		enc := varint(c)
		if e := rng.Intn(3); e > 0 && len(enc)+e <= 9 {
			enc = nonMinimal(c, e)
		}
		out = append(out, enc...)
		out = append(out, byte(rng.Intn(256)))
	}
	switch rng.Intn(8) {
	case 0: // truncate somewhere, possibly mid-varint
		if len(out) > 0 {
			out = out[:rng.Intn(len(out))]
		}
	case 1: // overflowing varint
		out = append(out, bytes.Repeat([]byte{0xff}, 10)...)
	case 2: // dangling continuation byte
		out = append(out, 0x80)
	}
	return out
}
