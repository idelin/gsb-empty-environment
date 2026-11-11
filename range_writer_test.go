package netsec

import (
	"errors"
	"fmt"
	"testing"
)

const dirtyByte = 0xDB

var errFault = errors.New("netsec/test: injected device fault")

// memBlockDevice is an in-memory block device whose storage starts out
// full of non-zero "dirty" pages, simulating recycled blocks with stale
// data. Only full, block-aligned writes are accepted.
type memBlockDevice struct {
	blockSize   int64
	storage     []byte
	failAtWrite int // 1-based write that must fail; 0 disables faults
	writes      int
}

func newDirtyDevice(blocks, blockSize int64) *memBlockDevice {
	d := &memBlockDevice{
		blockSize: blockSize,
		storage:   make([]byte, blocks*blockSize),
	}
	for i := range d.storage {
		d.storage[i] = dirtyByte
	}
	return d
}

func (d *memBlockDevice) BlockSize() int64 { return d.blockSize }

func (d *memBlockDevice) Blocks() int64 {
	return int64(len(d.storage)) / d.blockSize
}

func (d *memBlockDevice) ReadAt(dst []byte, offset int64) (int, error) {
	if offset < 0 || offset+int64(len(dst)) > int64(len(d.storage)) {
		return 0, fmt.Errorf("read out of bounds: offset %d len %d", offset, len(dst))
	}
	return copy(dst, d.storage[offset:offset+int64(len(dst))]), nil
}

func (d *memBlockDevice) WriteAt(src []byte, offset int64) (int, error) {
	if offset%d.blockSize != 0 {
		return 0, fmt.Errorf("unaligned write offset %d", offset)
	}
	if int64(len(src))%d.blockSize != 0 {
		return 0, fmt.Errorf("partial block write of %d bytes", len(src))
	}
	if offset < 0 || offset+int64(len(src)) > int64(len(d.storage)) {
		return 0, fmt.Errorf("write out of bounds: offset %d len %d", offset, len(src))
	}
	d.writes++
	if d.failAtWrite > 0 && d.writes == d.failAtWrite {
		return 0, errFault
	}
	return copy(d.storage[offset:offset+int64(len(src))], src), nil
}

// payload builds deterministic, non-zero authenticated data. Zero bytes
// never appear, so any hole leak is distinguishable from valid data.
func payload(off, n int64) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((off + int64(i) + 1) & 0x7f)
	}
	return b
}

// expectImage reconstructs the exact expected logical content.
func expectImage(ranges []Range, size int64) []byte {
	want := make([]byte, size)
	for _, r := range ranges {
		if r.Kind == Data {
			copy(want[r.Offset:r.Offset+r.Length], r.Payload)
		}
	}
	return want
}

func assertImage(t *testing.T, d *memBlockDevice, want []byte) {
	t.Helper()
	got := make([]byte, len(want))
	if _, err := d.ReadAt(got, 0); err != nil {
		t.Fatalf("read back image: %v", err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d mismatch: got %#x want %#x", i, got[i], want[i])
		}
	}
}

func assertNoDirtyLeak(t *testing.T, d *memBlockDevice, size int64) {
	t.Helper()
	for i := int64(0); i < size; i++ {
		if d.storage[i] == dirtyByte {
			t.Fatalf("stale dirty byte survived at offset %d", i)
		}
	}
}

// TestPlanValidation covers every partition-integrity failure mode.
func TestPlanValidation(t *testing.T) {
	const size = 16
	good := []Range{
		{Offset: 0, Length: 4, Kind: Data, Payload: payload(0, 4)},
		{Offset: 4, Length: 4, Kind: Hole},
		{Offset: 8, Length: 8, Kind: Data, Payload: payload(8, 8)},
	}
	if err := validateRanges(good, size); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}

	bad := []struct {
		name   string
		ranges []Range
		size   int64
	}{
		{
			name: "initial gap",
			size: size,
			ranges: []Range{
				{Offset: 1, Length: 15, Kind: Hole},
			},
		},
		{
			name: "gap between ranges",
			size: size,
			ranges: []Range{
				{Offset: 0, Length: 4, Kind: Hole},
				{Offset: 5, Length: 11, Kind: Hole},
			},
		},
		{
			name: "overlap",
			size: size,
			ranges: []Range{
				{Offset: 0, Length: 8, Kind: Hole},
				{Offset: 7, Length: 9, Kind: Hole},
			},
		},
		{
			name: "undershoot",
			size: size,
			ranges: []Range{
				{Offset: 0, Length: 12, Kind: Hole},
			},
		},
		{
			name: "overshoot",
			size: size,
			ranges: []Range{
				{Offset: 0, Length: 8, Kind: Hole},
				{Offset: 8, Length: 9, Kind: Hole},
			},
		},
		{
			name: "unsorted",
			size: size,
			ranges: []Range{
				{Offset: 8, Length: 8, Kind: Hole},
				{Offset: 0, Length: 8, Kind: Hole},
			},
		},
		{
			name: "zero length range",
			size: size,
			ranges: []Range{
				{Offset: 0, Length: 0, Kind: Hole},
			},
		},
		{
			name: "negative offset",
			size: size,
			ranges: []Range{
				{Offset: -1, Length: 17, Kind: Hole},
			},
		},
		{
			name: "data payload too short",
			size: size,
			ranges: []Range{
				{Offset: 0, Length: 4, Kind: Data, Payload: payload(0, 3)},
				{Offset: 4, Length: 12, Kind: Hole},
			},
		},
		{
			name: "hole with payload",
			size: size,
			ranges: []Range{
				{Offset: 0, Length: 4, Kind: Hole, Payload: payload(0, 4)},
				{Offset: 4, Length: 12, Kind: Hole},
			},
		},
		{
			name: "unknown kind",
			size: size,
			ranges: []Range{
				{Offset: 0, Length: 16, Kind: 99},
			},
		},
		{
			name:   "empty plan for nonempty image",
			size:   size,
			ranges: nil,
		},
		{
			name: "offset overflow",
			size: size,
			ranges: []Range{
				{Offset: 0, Length: 1, Kind: Hole},
				{Offset: 1, Length: 1 << 62, Kind: Hole},
			},
		},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateRanges(tc.ranges, tc.size); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("want ErrInvalidPlan, got %v", err)
			}
		})
	}

	t.Run("empty image accepts empty plan", func(t *testing.T) {
		if err := validateRanges(nil, 0); err != nil {
			t.Fatalf("empty image plan rejected: %v", err)
		}
	})
}

// TestAlignedBoundaries places every range transition exactly on a block
// boundary.
func TestAlignedBoundaries(t *testing.T) {
	const blockSize = 4
	const blocks = 4
	const size = int64(blocks * blockSize)
	ranges := []Range{
		{Offset: 0, Length: 4, Kind: Data, Payload: payload(0, 4)},
		{Offset: 4, Length: 4, Kind: Hole},
		{Offset: 8, Length: 4, Kind: Hole},
		{Offset: 12, Length: 4, Kind: Data, Payload: payload(12, 4)},
	}

	dev := newDirtyDevice(blocks, blockSize)
	w, err := NewRestoreWriter(dev, size)
	if err != nil {
		t.Fatalf("NewRestoreWriter: %v", err)
	}
	if err := w.Restore(ranges); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := w.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	segs, covered := w.Covered()
	if covered != size || len(segs) != len(ranges) {
		t.Fatalf("ledger = %d segments covering %d bytes, want %d segments / %d bytes",
			len(segs), covered, len(ranges), size)
	}
	assertImage(t, dev, expectImage(ranges, size))
	assertNoDirtyLeak(t, dev, size)
}

// TestUnalignedTransitions splits ranges mid-block with a small block size,
// exercising the zero-assembled partial block path.
func TestUnalignedTransitions(t *testing.T) {
	const blockSize = 4
	const blocks = 3
	const size = int64(blocks * blockSize)
	ranges := []Range{
		{Offset: 0, Length: 1, Kind: Hole},
		{Offset: 1, Length: 3, Kind: Data, Payload: payload(1, 3)},
		{Offset: 4, Length: 2, Kind: Hole},
		{Offset: 6, Length: 4, Kind: Data, Payload: payload(6, 4)},
		{Offset: 10, Length: 2, Kind: Hole},
	}

	dev := newDirtyDevice(blocks, blockSize)
	w, err := NewRestoreWriter(dev, size)
	if err != nil {
		t.Fatalf("NewRestoreWriter: %v", err)
	}
	if err := w.Restore(ranges); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := w.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	segs, covered := w.Covered()
	if covered != size {
		t.Fatalf("ledger covers %d of %d bytes", covered, size)
	}
	// Segments are recorded per block; they must still stitch together
	// into one gap-free partition of the image.
	var stitched int64
	for _, seg := range segs {
		if seg.Offset != stitched {
			t.Fatalf("ledger gap/overlap at %d, expected %d", seg.Offset, stitched)
		}
		stitched += seg.Length
	}
	if stitched != size {
		t.Fatalf("ledger stitches to %d, want %d", stitched, size)
	}
	assertImage(t, dev, expectImage(ranges, size))
	assertNoDirtyLeak(t, dev, size)
}

// TestPartialLastBlock makes the final block only partially used; its tail
// must be zero rather than stale content.
func TestPartialLastBlock(t *testing.T) {
	const blockSize = 8
	const blocks = 3
	const size = int64(20) // 2 full blocks + 4-byte tail block
	ranges := []Range{
		{Offset: 0, Length: 6, Kind: Data, Payload: payload(0, 6)},
		{Offset: 6, Length: 10, Kind: Hole},
		{Offset: 16, Length: 4, Kind: Data, Payload: payload(16, 4)},
	}

	dev := newDirtyDevice(blocks, blockSize)
	w, err := NewRestoreWriter(dev, size)
	if err != nil {
		t.Fatalf("NewRestoreWriter: %v", err)
	}
	if err := w.Restore(ranges); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := w.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	assertImage(t, dev, expectImage(ranges, size))
	assertNoDirtyLeak(t, dev, size)

	// The unused tail of the last physical block must also be zero, even
	// though it lies outside the logical image.
	for i := size; i < blocks*blockSize; i++ {
		if dev.storage[i] != 0 {
			t.Fatalf("tail byte %d not zero: %#x", i, dev.storage[i])
		}
	}
}

// TestAllHoleImage proves an image with no data ranges zeroes every
// recycled block without trusting allocation state.
func TestAllHoleImage(t *testing.T) {
	const blockSize = 4
	const blocks = 4
	const size = int64(blocks * blockSize)

	dev := newDirtyDevice(blocks, blockSize)
	w, err := NewRestoreWriter(dev, size)
	if err != nil {
		t.Fatalf("NewRestoreWriter: %v", err)
	}
	if err := w.Restore([]Range{{Offset: 0, Length: size, Kind: Hole}}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := w.Publish(); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	assertImage(t, dev, make([]byte, size))
	assertNoDirtyLeak(t, dev, size)
}

// TestMidWriteFailure injects a fault mid-restore, proves the writer does
// not claim coverage it lacks and that Publish refuses, then retries to
// completion and shows no stale bytes survive.
func TestMidWriteFailure(t *testing.T) {
	const blockSize = 4
	const blocks = 3
	const size = int64(blocks * blockSize)
	ranges := []Range{
		{Offset: 0, Length: 5, Kind: Data, Payload: payload(0, 5)},
		{Offset: 5, Length: 4, Kind: Hole},
		{Offset: 9, Length: 3, Kind: Data, Payload: payload(9, 3)},
	}

	dev := newDirtyDevice(blocks, blockSize)
	dev.failAtWrite = 2
	w, err := NewRestoreWriter(dev, size)
	if err != nil {
		t.Fatalf("NewRestoreWriter: %v", err)
	}
	if err := w.Restore(ranges); !errors.Is(err, errFault) {
		t.Fatalf("want injected fault, got %v", err)
	}

	segs, covered := w.Covered()
	if covered != blockSize || len(segs) != 1 {
		t.Fatalf("after fault: covered %d via %d segments, want %d / 1",
			covered, len(segs), blockSize)
	}
	if err := w.Verify(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Verify after fault: want ErrIncomplete, got %v", err)
	}
	if err := w.Publish(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Publish after fault: want ErrIncomplete, got %v", err)
	}

	// The faulted block must remain fully dirty: the failed write must not
	// have partially landed.
	for i := int64(blockSize); i < 2*blockSize; i++ {
		if dev.storage[i] != dirtyByte {
			t.Fatalf("faulted block byte %d unexpectedly modified", i)
		}
	}

	// Clear the fault and retry the whole restore.
	dev.failAtWrite = 0
	if err := w.Restore(ranges); err != nil {
		t.Fatalf("retry Restore: %v", err)
	}
	if err := w.Publish(); err != nil {
		t.Fatalf("retry Publish: %v", err)
	}
	assertImage(t, dev, expectImage(ranges, size))
	assertNoDirtyLeak(t, dev, size)
}

// TestZeroSizeImage covers the empty-image edge case.
func TestZeroSizeImage(t *testing.T) {
	dev := newDirtyDevice(1, 4)
	w, err := NewRestoreWriter(dev, 0)
	if err != nil {
		t.Fatalf("NewRestoreWriter: %v", err)
	}
	if err := w.Restore(nil); err != nil {
		t.Fatalf("Restore empty: %v", err)
	}
	if err := w.Publish(); err != nil {
		t.Fatalf("Publish empty: %v", err)
	}
	segs, covered := w.Covered()
	if covered != 0 || len(segs) != 0 {
		t.Fatalf("empty image ledger = %v / %d", segs, covered)
	}
}

// TestPublishDetectsStaleHole proves the hole readback proof fails when a
// hole's backing bytes are tampered with after writing.
func TestPublishDetectsStaleHole(t *testing.T) {
	const blockSize = 4
	const size = int64(8)
	ranges := []Range{
		{Offset: 0, Length: 4, Kind: Hole},
		{Offset: 4, Length: 4, Kind: Hole},
	}

	dev := newDirtyDevice(2, blockSize)
	w, err := NewRestoreWriter(dev, size)
	if err != nil {
		t.Fatalf("NewRestoreWriter: %v", err)
	}
	if err := w.Restore(ranges); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	dev.storage[6] = dirtyByte
	if err := w.Publish(); !errors.Is(err, ErrStaleData) {
		t.Fatalf("want ErrStaleData for tampered hole, got %v", err)
	}
}

func TestDeviceCapacityAndBlockSize(t *testing.T) {
	if _, err := NewRestoreWriter(newDirtyDevice(1, 4), 5); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("oversized image: want ErrInvalidPlan, got %v", err)
	}
	if _, err := NewRestoreWriter(nil, 0); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("nil device: want ErrInvalidPlan, got %v", err)
	}
}

// faultyReadDevice fails reads at one offset to exercise Publish's
// readback error path.
type faultyReadDevice struct {
	*memBlockDevice
	failOffset int64
}

func (d faultyReadDevice) ReadAt(dst []byte, offset int64) (int, error) {
	if offset == d.failOffset {
		return 0, errFault
	}
	return d.memBlockDevice.ReadAt(dst, offset)
}

func TestPublishHoleReadError(t *testing.T) {
	const blockSize = 4
	const size = int64(8)
	ranges := []Range{
		{Offset: 0, Length: 4, Kind: Hole},
		{Offset: 4, Length: 4, Kind: Hole},
	}

	dev := faultyReadDevice{
		memBlockDevice: newDirtyDevice(2, blockSize),
		failOffset:     4,
	}
	w, err := NewRestoreWriter(dev, size)
	if err != nil {
		t.Fatalf("NewRestoreWriter: %v", err)
	}
	if err := w.Restore(ranges); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := w.Publish(); !errors.Is(err, errFault) {
		t.Fatalf("want read fault from Publish, got %v", err)
	}
}

func TestNewRestoreWriterRejectsBadArgs(t *testing.T) {
	zeroBlock := &memBlockDevice{blockSize: 0, storage: make([]byte, 4)}
	if _, err := NewRestoreWriter(zeroBlock, 0); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("zero block size: want ErrInvalidPlan, got %v", err)
	}
	if _, err := NewRestoreWriter(newDirtyDevice(1, 4), -1); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("negative size: want ErrInvalidPlan, got %v", err)
	}
}

func TestOverflowHelpers(t *testing.T) {
	max := int64(^uint64(0) >> 1)
	if _, ok := add64(max, 1); ok {
		t.Fatal("add64 failed to detect overflow")
	}
	if s, ok := add64(1, 2); !ok || s != 3 {
		t.Fatalf("add64 normal case = %d, %v", s, ok)
	}
	if _, ok := mul64(max, 2); ok {
		t.Fatal("mul64 failed to detect overflow")
	}
	if s, ok := mul64(3, 4); !ok || s != 12 {
		t.Fatalf("mul64 normal case = %d, %v", s, ok)
	}
	if s, ok := mul64(0, max); !ok || s != 0 {
		t.Fatalf("mul64 zero case = %d, %v", s, ok)
	}
}

func TestVerifyLedgerDefects(t *testing.T) {
	dev := newDirtyDevice(2, 4)
	w, err := NewRestoreWriter(dev, 8)
	if err != nil {
		t.Fatalf("NewRestoreWriter: %v", err)
	}

	w.segments = []Segment{{Offset: 0, Length: 0}}
	if err := w.Verify(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("zero-length segment: want ErrIncomplete, got %v", err)
	}

	w.segments = []Segment{
		{Offset: 0, Length: 4, Kind: Hole},
		{Offset: 2, Length: 4, Kind: Hole},
	}
	w.covered = 8
	if err := w.Verify(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("overlap: want ErrIncomplete, got %v", err)
	}

	w.segments = []Segment{
		{Offset: 0, Length: 4, Kind: Hole},
		{Offset: 6, Length: 2, Kind: Hole},
	}
	w.covered = 6
	if err := w.Verify(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("gap: want ErrIncomplete, got %v", err)
	}
}
