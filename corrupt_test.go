package streamcol

import (
	"context"
	"errors"
	"math"
	"testing"
)

// blockErrors drains a decoder and collects the per-block errors in order.
func blockErrors(errs []error) []error {
	out := make([]error, 0, len(errs))
	for _, e := range errs {
		var be *BlockError
		if errors.As(e, &be) {
			out = append(out, be.Err)
		}
	}
	return out
}

func TestCorruptBaselineBreaksChainUntilIndependent(t *testing.T) {
	// 6 blocks of 4; blocks 0 and 3 are independent.
	vals := make([]int64, 24)
	for i := range vals {
		vals[i] = int64(1000 + i*7)
	}
	data, idx := encode(t, vals, &EncoderConfig{BlockSize: 4, Encoding: EncDelta, IndependentEvery: 3})

	// Corrupt the baseline byte of block 0 (offset+4: magic, verType, flags).
	// 0x00 -> 0x02 keeps it a well-formed one-byte varint, so the header
	// parses and the checksum is what catches the tampering.
	bad := append([]byte(nil), data...)
	bad[idx[0].Offset+4] ^= 0x02

	got, _, errs := DecodeAll(context.Background(), bad, nil)
	be := blockErrors(errs)
	if len(be) != 3 {
		t.Fatalf("want 3 block errors, got %v", errs)
	}
	if !errors.Is(be[0], ErrChecksum) {
		t.Fatalf("block 0: want ErrChecksum, got %v", be[0])
	}
	// Blocks 1 and 2 depend on block 0's last value and cannot be decoded.
	for i := 1; i <= 2; i++ {
		if !errors.Is(be[i], ErrBrokenChain) {
			t.Fatalf("block %d: want ErrBrokenChain, got %v", i, be[i])
		}
	}
	// Block 3 is independent: decoding resumes there.
	want := vals[12:]
	if !equalVals(got, want) {
		t.Fatalf("recovered %d values, want %d (blocks 3..5)", len(got), len(want))
	}
}

func TestCorruptPayloadSkippedPrecisely(t *testing.T) {
	vals := testSeries()["random-walk"]
	data, idx := encode(t, vals, &EncoderConfig{BlockSize: 16, Encoding: EncAuto, IndependentEvery: 2})

	// Flip a payload byte in block 1 (a dependent block).
	bad := append([]byte(nil), data...)
	bad[idx[1].Offset+12] ^= 0x01

	got, gotIdx, errs := DecodeAll(context.Background(), bad, nil)
	be := blockErrors(errs)
	if len(be) == 0 || !errors.Is(be[0], ErrChecksum) {
		t.Fatalf("want ErrChecksum, got %v", errs)
	}
	// Block 2 is independent (IndependentEvery=2), so decoding resumes and
	// every value from block 2 onward is intact.
	want := append(append([]int64{}, vals[:16]...), vals[32:]...)
	if !equalVals(got, want) {
		t.Fatalf("recovered %d values, want %d", len(got), len(want))
	}
	// Index entries keep true stream positions despite the gap.
	for _, e := range gotIdx {
		if e.FirstIndex >= 32 {
			v, err := DecodeAt(bad, gotIdx, e.FirstIndex)
			if err != nil || v != vals[e.FirstIndex] {
				t.Fatalf("DecodeAt(%d) = %d, %v after corruption", e.FirstIndex, v, err)
			}
		}
	}
	// Elements of the lost block are not addressable.
	if _, err := DecodeAt(bad, gotIdx, 20); !errors.Is(err, ErrIndexOutOfRange) {
		t.Fatalf("DecodeAt into lost block: want ErrIndexOutOfRange, got %v", err)
	}
}

func TestDeltaOverflowRejected(t *testing.T) {
	// Independent delta block: baseline MaxInt64, first delta +1 wraps.
	payload := appendZigzag(nil, 1)
	blk := appendBlock(nil, EncDelta, true, math.MaxInt64, 1, payload)
	_, _, errs := DecodeAll(context.Background(), blk, nil)
	if len(errs) != 1 || !errors.Is(errs[0], ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", errs)
	}

	// Delta-of-delta: baseline MinInt64, delta chain underflows.
	payload = appendZigzag(nil, -1) // d0 = -1
	payload = appendZigzag(payload, math.MinInt64)
	payload = appendZigzag(payload, -1) // dd = -1 -> d = MinInt64-1 wraps
	blk = appendBlock(nil, EncDeltaOfDelta, true, math.MinInt64, 3, payload)
	_, _, errs = DecodeAll(context.Background(), blk, nil)
	if len(errs) != 1 || !errors.Is(errs[0], ErrOverflow) {
		t.Fatalf("dod: want ErrOverflow, got %v", errs)
	}
}

func TestNonShortestVarintRejected(t *testing.T) {
	// In the payload: raw block whose single element is 0x80 0x00.
	blk := appendBlock(nil, EncRaw, true, 0, 1, []byte{0x80, 0x00})
	_, _, errs := DecodeAll(context.Background(), blk, nil)
	if len(errs) != 1 || !errors.Is(errs[0], ErrVarintNotShortest) {
		t.Fatalf("payload: want ErrVarintNotShortest, got %v", errs)
	}

	// In the header: count encoded as 0x80 0x00.
	hdr := []byte{magic0, magic1, byte(formatVersion<<4) | byte(EncRaw), flagIndependent}
	hdr = appendZigzag(hdr, 0)
	hdr = append(hdr, 0x80, 0x00) // count, non-shortest
	hdr = appendUvarint(hdr, 0)   // payloadLen
	// A valid independent block right after the garbage must be found again.
	good := appendBlock(nil, EncRaw, true, 7, 1, appendZigzag(nil, 7))
	stream := append(hdr, good...)
	got, _, errs := DecodeAll(context.Background(), stream, nil)
	if len(errs) != 1 || !errors.Is(errs[0], ErrVarintNotShortest) {
		t.Fatalf("header: want ErrVarintNotShortest, got %v", errs)
	}
	if !equalVals(got, []int64{7}) {
		t.Fatalf("resync after bad header decoded %v, want [7]", got)
	}
}

func TestTenByteVarintInPayload(t *testing.T) {
	// Valid: 10-byte encoding of zigzag(MinInt64) decodes to MinInt64.
	payload := appendZigzag(nil, math.MinInt64)
	if len(payload) != 10 {
		t.Fatalf("zigzag(MinInt64) is %d bytes, want 10", len(payload))
	}
	blk := appendBlock(nil, EncRaw, true, 0, 1, payload)
	got, _, errs := DecodeAll(context.Background(), blk, nil)
	if len(errs) != 0 || !equalVals(got, []int64{math.MinInt64}) {
		t.Fatalf("valid 10-byte varint: got %v errs %v", got, errs)
	}

	// Invalid: 10th byte 0x02 overflows 64 bits.
	bad := append(append([]byte{}, payload[:9]...), 0x02)
	blk = appendBlock(nil, EncRaw, true, 0, 1, bad)
	_, _, errs = DecodeAll(context.Background(), blk, nil)
	if len(errs) != 1 || !errors.Is(errs[0], ErrVarintOverflow) {
		t.Fatalf("10th byte 0x02: want ErrVarintOverflow, got %v", errs)
	}
}

func TestLengthLimitsCheckedBeforeAllocation(t *testing.T) {
	// Absurd declared lengths must be rejected from the header alone,
	// without waiting for the (nonexistent) payload bytes.
	hdr := []byte{magic0, magic1, byte(formatVersion<<4) | byte(EncRaw), flagIndependent}
	hdr = appendZigzag(hdr, 0)
	hdr = appendUvarint(hdr, 1<<40) // count
	hdr = appendUvarint(hdr, 1<<40) // payloadLen
	d := NewDecoder(nil)
	d.Feed(hdr)
	_, err := d.NextBlock(context.Background())
	var be *BlockError
	if !errors.As(err, &be) || !errors.Is(be.Err, ErrLengthLimit) {
		t.Fatalf("huge lengths: want ErrLengthLimit, got %v", err)
	}

	// count > payloadLen is structurally impossible.
	hdr2 := []byte{magic0, magic1, byte(formatVersion<<4) | byte(EncRaw), flagIndependent}
	hdr2 = appendZigzag(hdr2, 0)
	hdr2 = appendUvarint(hdr2, 100)
	hdr2 = appendUvarint(hdr2, 3)
	d = NewDecoder(nil)
	d.Feed(hdr2)
	if _, err := d.NextBlock(context.Background()); !errors.Is(err, ErrCorruptHeader) {
		t.Fatalf("count>payloadLen: want ErrCorruptHeader, got %v", err)
	}

	// A valid stream exceeding configured quotas is rejected per block.
	vals := make([]int64, 64)
	data, _ := encode(t, vals, &EncoderConfig{BlockSize: 16, Encoding: EncRaw})
	_, _, errs := DecodeAll(context.Background(), data, &DecoderConfig{MaxBlockElements: 8})
	if len(errs) == 0 || !errors.Is(errs[0], ErrLengthLimit) {
		t.Fatalf("MaxBlockElements: want ErrLengthLimit, got %v", errs)
	}
	_, _, errs = DecodeAll(context.Background(), data, &DecoderConfig{MaxTotalElements: 20})
	if len(errs) == 0 || !errors.Is(errs[0], ErrLengthLimit) {
		t.Fatalf("MaxTotalElements: want ErrLengthLimit, got %v", errs)
	}
}

func TestFirstBlockMustBeIndependent(t *testing.T) {
	blk := appendBlock(nil, EncDelta, false, 0, 1, appendZigzag(nil, 5))
	_, _, errs := DecodeAll(context.Background(), blk, nil)
	if len(errs) != 1 || !errors.Is(errs[0], ErrBrokenChain) {
		t.Fatalf("want ErrBrokenChain, got %v", errs)
	}
}

func TestDependentBlockWithNonzeroBaselineDelta(t *testing.T) {
	// Block 0: independent raw [10, 20]. Block 1: dependent delta whose
	// stored baseline delta is +5, so its anchor is 20+5 = 25.
	b0 := appendBlock(nil, EncRaw, true, 0, 2, appendZigzag(appendZigzag(nil, 10), 20))
	b1 := appendBlock(nil, EncDelta, false, 5, 1, appendZigzag(nil, 1))
	got, _, errs := DecodeAll(context.Background(), append(b0, b1...), nil)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if !equalVals(got, []int64{10, 20, 26}) {
		t.Fatalf("got %v, want [10 20 26]", got)
	}
}

func TestCorruptHeaderResync(t *testing.T) {
	vals := []int64{1, 2, 3, 4}
	good, _ := encode(t, vals, &EncoderConfig{BlockSize: 4, Encoding: EncRaw})
	// Prepend garbage, including a fake magic with an unparseable header.
	garbage := []byte{0x00, 0x13, 0x37, magic0, magic1, 0xFF, 0xFF}
	stream := append(garbage, good...)
	got, _, errs := DecodeAll(context.Background(), stream, nil)
	if len(errs) == 0 {
		t.Fatalf("want block errors for garbage, got none")
	}
	if !equalVals(got, vals) {
		t.Fatalf("after resync got %v, want %v", got, vals)
	}
}

func TestEmptyAndSingleValueStreams(t *testing.T) {
	got, _, errs := DecodeAll(context.Background(), nil, nil)
	if len(errs) != 0 || len(got) != 0 {
		t.Fatalf("empty stream: got %v errs %v", got, errs)
	}
	data, _ := encode(t, []int64{math.MaxInt64}, &EncoderConfig{BlockSize: 1})
	got, _, errs = DecodeAll(context.Background(), data, nil)
	if len(errs) != 0 || !equalVals(got, []int64{math.MaxInt64}) {
		t.Fatalf("single value: got %v errs %v", got, errs)
	}
}
