package streamcol

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func encode(t *testing.T, vals []int64, cfg *EncoderConfig) ([]byte, Index) {
	t.Helper()
	var buf bytes.Buffer
	e := NewEncoder(&buf, cfg)
	for _, v := range vals {
		if err := e.Add(v); err != nil {
			t.Fatalf("Add(%d): %v", v, err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes(), e.Index()
}

func mustDecodeAll(t *testing.T, data []byte) ([]int64, Index) {
	t.Helper()
	vals, idx, errs := DecodeAll(context.Background(), data, nil)
	if len(errs) != 0 {
		t.Fatalf("DecodeAll errors: %v", errs)
	}
	return vals, idx
}

func equalVals(a, b []int64) bool {
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

func testSeries() map[string][]int64 {
	r := rand.New(rand.NewSource(7))
	full := make([]int64, 500)
	for i := range full {
		full[i] = int64(r.Uint64())
	}
	walk := make([]int64, 500)
	cur := int64(0)
	for i := range walk {
		cur += int64(r.Intn(201) - 100)
		walk[i] = cur
	}
	ts := make([]int64, 500)
	cur = 1_700_000_000_000_000_000
	for i := range ts {
		cur += 1_000_000_000 + int64(r.Intn(41)-20)
		ts[i] = cur
	}
	constant := make([]int64, 500)
	for i := range constant {
		constant[i] = 424242
	}
	edge := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, math.MaxInt64, math.MinInt64, 0, 127, -128, 300}
	return map[string][]int64{
		"full-range":  full,
		"random-walk": walk,
		"timestamps":  ts,
		"constant":    constant,
		"edge":        edge,
		"single":      {math.MinInt64},
		"empty":       {},
	}
}

func TestRoundTrip(t *testing.T) {
	encs := []Encoding{EncRaw, EncDelta, EncDeltaOfDelta, EncAuto}
	for name, vals := range testSeries() {
		for _, enc := range encs {
			for _, bs := range []int{1, 3, 128} {
				// Forced delta encodings cannot represent full-range jumps.
				if name == "full-range" && enc != EncRaw && enc != EncAuto {
					continue
				}
				if name == "edge" && enc != EncRaw && enc != EncAuto {
					continue
				}
				data, encIdx := encode(t, vals, &EncoderConfig{BlockSize: bs, Encoding: enc, IndependentEvery: 4})
				got, decIdx := mustDecodeAll(t, data)
				if !equalVals(got, vals) {
					t.Fatalf("%s/%s/bs=%d: decoded %d values, want %d", name, enc, bs, len(got), len(vals))
				}
				if !reflect.DeepEqual(encIdx, decIdx) {
					t.Fatalf("%s/%s/bs=%d: encoder and decoder indexes differ", name, enc, bs)
				}
			}
		}
	}
}

func TestForcedDeltaOverflowFailsLoudly(t *testing.T) {
	vals := []int64{math.MinInt64, math.MaxInt64}
	var buf bytes.Buffer
	e := NewEncoder(&buf, &EncoderConfig{BlockSize: 2, Encoding: EncDelta})
	e.Add(vals[0])
	err := e.Add(vals[1])
	if err == nil {
		err = e.Close()
	}
	if !errors.Is(err, ErrIncompatibleEncoder) {
		t.Fatalf("want ErrIncompatibleEncoder, got %v", err)
	}
}

func TestChunkedEqualsWhole(t *testing.T) {
	vals := testSeries()["timestamps"]
	data, _ := encode(t, vals, &EncoderConfig{BlockSize: 32, Encoding: EncAuto, IndependentEvery: 3})
	want, wantIdx := mustDecodeAll(t, data)

	for _, step := range []int{1, 2, 7, 64} {
		d := NewDecoder(nil)
		var got []int64
		for i := 0; i < len(data); i += step {
			end := i + step
			if end > len(data) {
				end = len(data)
			}
			d.Feed(data[i:end])
			for {
				vs, err := d.NextBlock(context.Background())
				if errors.Is(err, ErrNeedMoreData) {
					break
				}
				if err != nil {
					t.Fatalf("step=%d: %v", step, err)
				}
				got = append(got, vs...)
			}
		}
		if !equalVals(got, want) {
			t.Fatalf("step=%d: chunked decode differs from whole-buffer decode", step)
		}
		if !reflect.DeepEqual(d.Index(), wantIdx) {
			t.Fatalf("step=%d: chunked index differs", step)
		}
	}
}

func TestRandomAccessMatchesSequential(t *testing.T) {
	vals := testSeries()["timestamps"]
	data, _ := encode(t, vals, &EncoderConfig{BlockSize: 16, Encoding: EncAuto, IndependentEvery: 3})
	_, idx := mustDecodeAll(t, data)
	for k := uint64(0); k < uint64(len(vals)); k++ {
		v, err := DecodeAt(data, idx, k)
		if err != nil {
			t.Fatalf("DecodeAt(%d): %v", k, err)
		}
		if v != vals[k] {
			t.Fatalf("DecodeAt(%d) = %d, want %d", k, v, vals[k])
		}
	}
	if _, err := DecodeAt(data, idx, uint64(len(vals))); !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("out-of-range DecodeAt: want ErrIndexOutOfRange, got %v", err)
	}
}

func TestCheckpointResume(t *testing.T) {
	vals := testSeries()["random-walk"]
	data, encIdx := encode(t, vals, &EncoderConfig{BlockSize: 8, Encoding: EncAuto, IndependentEvery: 3})
	want, _ := mustDecodeAll(t, data)

	d1 := NewDecoder(nil)
	d1.Feed(data)
	var head []int64
	for i := 0; i < 5; i++ {
		vs, err := d1.NextBlock(context.Background())
		if err != nil {
			t.Fatalf("NextBlock: %v", err)
		}
		head = append(head, vs...)
	}
	cp := d1.Checkpoint()
	if cp.Offset != encIdx[5].Offset {
		t.Fatalf("checkpoint offset %d, want block 5 offset %d", cp.Offset, encIdx[5].Offset)
	}

	d2 := ResumeDecoder(cp, nil)
	d2.Feed(data[cp.Offset:])
	tail, idx2, errs := Drain(context.Background(), d2)
	if len(errs) != 0 {
		t.Fatalf("resumed drain errors: %v", errs)
	}
	got := append(head, tail...)
	if !equalVals(got, want) {
		t.Fatalf("checkpoint/resume decode differs from whole decode")
	}
	if len(idx2) == 0 || idx2[0].FirstIndex != uint64(len(head)) {
		t.Fatalf("resumed index starts at %v, want FirstIndex %d", idx2, len(head))
	}
}

func TestCancellation(t *testing.T) {
	vals := testSeries()["random-walk"]
	data, _ := encode(t, vals, &EncoderConfig{BlockSize: 8})
	d := NewDecoder(nil)
	d.Feed(data)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := d.NextBlock(ctx); err != nil {
		t.Fatalf("first block: %v", err)
	}
	cancel()
	if _, err := d.NextBlock(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if _, _, errs := DecodeAll(ctx, data, nil); len(errs) == 0 || !errors.Is(errs[0], context.Canceled) {
		t.Fatalf("DecodeAll with cancelled ctx: %v", errs)
	}
}

func TestTruncatedStreamReportsNeedMoreData(t *testing.T) {
	vals := testSeries()["random-walk"]
	data, _ := encode(t, vals, &EncoderConfig{BlockSize: 8})
	full, _ := mustDecodeAll(t, data)

	for _, cut := range []int{1, 2, 3, 5, len(data) / 2, len(data) - 1} {
		d := NewDecoder(nil)
		d.Feed(data[:cut])
		var got []int64
		for {
			vs, err := d.NextBlock(context.Background())
			if errors.Is(err, ErrNeedMoreData) {
				break
			}
			if err != nil {
				t.Fatalf("cut=%d: clean truncation must not yield corruption: %v", cut, err)
			}
			got = append(got, vs...)
		}
		if !equalVals(got, full[:len(got)]) {
			t.Fatalf("cut=%d: prefix values mismatch", cut)
		}
		_, _, errs := DecodeAll(context.Background(), data[:cut], nil)
		if len(errs) != 1 || !errors.Is(errs[0], io.ErrUnexpectedEOF) {
			t.Fatalf("cut=%d: want io.ErrUnexpectedEOF, got %v", cut, errs)
		}
	}
}

func TestZigzagAndVarint(t *testing.T) {
	cases := []struct {
		v int64
		u uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := zigzagEncode(c.v); got != c.u {
			t.Fatalf("zigzagEncode(%d) = %d, want %d", c.v, got, c.u)
		}
		if got := zigzagDecode(c.u); got != c.v {
			t.Fatalf("zigzagDecode(%d) = %d, want %d", c.u, got, c.v)
		}
		buf := appendZigzag(nil, c.v)
		v, n, err := decodeZigzag(buf)
		if err != nil || v != c.v || n != len(buf) {
			t.Fatalf("round trip %d: v=%d n=%d err=%v", c.v, v, n, err)
		}
	}
	// The maximum varint is exactly 10 bytes ending in 0x01.
	maxBuf := appendUvarint(nil, math.MaxUint64)
	if len(maxBuf) != 10 || maxBuf[9] != 0x01 {
		t.Fatalf("MaxUint64 varint = % x", maxBuf)
	}
}

func TestVarintStrictness(t *testing.T) {
	// Prefixes that must report "need more data", never corruption.
	for _, p := range [][]byte{{}, {0x80}, {0xFF, 0xFF, 0xFF}} {
		if _, _, err := decodeUvarint(p); !errors.Is(err, ErrNeedMoreData) {
			t.Fatalf("decodeUvarint(% x): want ErrNeedMoreData, got %v", p, err)
		}
	}
	// Non-shortest encodings.
	for _, p := range [][]byte{{0x80, 0x00}, {0x81, 0x00}, {0xFF, 0x80, 0x00}} {
		if _, _, err := decodeUvarint(p); !errors.Is(err, ErrVarintNotShortest) {
			t.Fatalf("decodeUvarint(% x): want ErrVarintNotShortest, got %v", p, err)
		}
	}
	// Tenth byte must be exactly 0x01.
	ten := func(last byte) []byte {
		b := bytes.Repeat([]byte{0xFF}, 9)
		return append(b, last)
	}
	if _, _, err := decodeUvarint(ten(0x00)); !errors.Is(err, ErrVarintNotShortest) {
		t.Fatalf("10th byte 0x00: want ErrVarintNotShortest, got %v", err)
	}
	if _, _, err := decodeUvarint(ten(0x02)); !errors.Is(err, ErrVarintOverflow) {
		t.Fatalf("10th byte 0x02: want ErrVarintOverflow, got %v", err)
	}
	if _, _, err := decodeUvarint(ten(0x81)); !errors.Is(err, ErrVarintOverflow) {
		t.Fatalf("10th byte 0x81: want ErrVarintOverflow, got %v", err)
	}
	if v, n, err := decodeUvarint(ten(0x01)); err != nil || v != math.MaxUint64 || n != 10 {
		t.Fatalf("10th byte 0x01: v=%d n=%d err=%v", v, n, err)
	}
	// Eleven continuation bytes overflow at the tenth.
	if _, _, err := decodeUvarint(bytes.Repeat([]byte{0x80}, 11)); !errors.Is(err, ErrVarintOverflow) {
		t.Fatalf("11 bytes: want ErrVarintOverflow, got %v", err)
	}
}

func TestCheckedArithmetic(t *testing.T) {
	if _, err := checkedAdd(math.MaxInt64, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MaxInt64+1: %v", err)
	}
	if _, err := checkedAdd(math.MinInt64, -1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MinInt64-1: %v", err)
	}
	if _, err := checkedSub(math.MinInt64, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MinInt64-1: %v", err)
	}
	if _, err := checkedSub(math.MaxInt64, -1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("MaxInt64+1 via sub: %v", err)
	}
	if v, err := checkedAdd(math.MaxInt64, 0); err != nil || v != math.MaxInt64 {
		t.Fatalf("MaxInt64+0: %d %v", v, err)
	}
	if v, err := checkedSub(math.MinInt64, math.MinInt64); err != nil || v != 0 {
		t.Fatalf("MinInt64-MinInt64: %d %v", v, err)
	}
}
