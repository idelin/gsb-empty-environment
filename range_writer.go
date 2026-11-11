package netsec

import (
	"bytes"
	"errors"
	"fmt"
)

// RangeKind classifies a restore range.
type RangeKind uint8

const (
	// Hole marks a sparse range whose logical bytes must read as zero.
	Hole RangeKind = iota
	// Data marks an authenticated range whose payload is restored verbatim.
	Data
)

// Range is one half-open interval [Offset, Offset+Length) of the logical
// image. For Data ranges Payload must hold exactly Length authenticated
// bytes. For Hole ranges Payload must be nil.
type Range struct {
	Offset  int64
	Length  int64
	Kind    RangeKind
	Payload []byte
}

// BlockDevice is the minimal target a restore is written to. Writes are
// full blocks at block-aligned offsets, so an implementation never has to
// preserve unwritten portions of a block on the writer's behalf.
type BlockDevice interface {
	// BlockSize returns the number of bytes in one block; it must be
	// positive.
	BlockSize() int64
	// Blocks returns the capacity of the device in blocks.
	Blocks() int64
	// ReadAt reads len(dst) bytes starting at offset.
	ReadAt(dst []byte, offset int64) (int, error)
	// WriteAt writes len(src) bytes at a block-aligned offset. Both offset
	// and len(src) must be multiples of BlockSize.
	WriteAt(src []byte, offset int64) (int, error)
}

var (
	// ErrInvalidPlan means the ranges do not form an exact, non-overlapping
	// partition of the logical length.
	ErrInvalidPlan = errors.New("netsec: invalid restore plan")
	// ErrIncomplete means the coverage ledger does not cover the whole
	// logical image at publish time.
	ErrIncomplete = errors.New("netsec: restore coverage incomplete")
	// ErrStaleData means a hole segment did not read back as zero.
	ErrStaleData = errors.New("netsec: stale data observed in hole")
)

// Segment is one contiguous covered portion of the logical image.
type Segment struct {
	Offset int64
	Length int64
	Kind   RangeKind
}

// RestoreWriter writes a partitioned set of data and hole ranges onto a
// block device while retaining a coverage ledger for pre-publish proof.
type RestoreWriter struct {
	dev      BlockDevice
	size     int64
	blockSz  int64
	segments []Segment
	covered  int64
}

// NewRestoreWriter validates that logicalSize fits on dev and returns a
// writer targeting it.
func NewRestoreWriter(dev BlockDevice, logicalSize int64) (*RestoreWriter, error) {
	if dev == nil {
		return nil, fmt.Errorf("%w: nil block device", ErrInvalidPlan)
	}
	blockSz := dev.BlockSize()
	if blockSz <= 0 {
		return nil, fmt.Errorf("%w: non-positive block size %d", ErrInvalidPlan, blockSz)
	}
	if logicalSize < 0 {
		return nil, fmt.Errorf("%w: negative logical size %d", ErrInvalidPlan, logicalSize)
	}
	capBytes, ok := mul64(blockSz, dev.Blocks())
	if !ok || logicalSize > capBytes {
		return nil, fmt.Errorf("%w: logical size %d exceeds device capacity %d",
			ErrInvalidPlan, logicalSize, capBytes)
	}
	return &RestoreWriter{
		dev:     dev,
		size:    logicalSize,
		blockSz: blockSz,
	}, nil
}

// Restore writes every range onto the device. Ranges are copied first, so
// the caller's slice is never modified. On success the ledger covers the
// full logical image; on error the caller may fix the device and call
// Restore again.
func (w *RestoreWriter) Restore(ranges []Range) error {
	if err := validateRanges(ranges, w.size); err != nil {
		return err
	}

	// Every restore attempt covers the full image from scratch, so the
	// ledger must not retain segments from an earlier failed attempt.
	w.segments = nil
	w.covered = 0

	plan := make([]Range, len(ranges))
	copy(plan, ranges)

	blockSz := w.blockSz
	cursor := 0
	blockBuf := make([]byte, blockSz)

	for blockStart := int64(0); blockStart < w.size; blockStart += blockSz {
		blockEnd := blockStart + blockSz
		if blockEnd > w.size {
			blockEnd = w.size
		}

		// Fresh, zero-initialized buffer per block: bytes that are not
		// overwritten by an authenticated data range stay zero. Stale
		// contents of any recycled target block are never read or merged.
		clear(blockBuf)

		// Local index of the first range intersecting this block. Ranges
		// fully consumed by earlier blocks advance cursor once; a range
		// spanning several blocks stays at cursor and is revisited per block.
		i := cursor
		for i < len(plan) && plan[i].Offset < blockEnd {
			r := plan[i]
			rEnd := r.Offset + r.Length
			if rEnd <= blockStart {
				cursor++
				i = cursor
				continue
			}
			// Intersection of the range with this block, expressed both
			// as positions inside blockBuf and as payload positions.
			lo := r.Offset - blockStart
			if lo < 0 {
				lo = 0
			}
			hi := rEnd - blockStart
			if hi > blockEnd-blockStart {
				hi = blockEnd - blockStart
			}
			payloadLo := blockStart + lo - r.Offset

			switch r.Kind {
			case Data:
				copy(blockBuf[lo:hi], r.Payload[payloadLo:payloadLo+(hi-lo)])
			case Hole:
				// Explicit zeroing: hole bytes must never depend on the
				// allocation state of the target block.
				clear(blockBuf[lo:hi])
			}

			if rEnd <= blockEnd {
				cursor++
			}
			i++
		}

		if _, err := w.dev.WriteAt(blockBuf, blockStart); err != nil {
			return fmt.Errorf("netsec: write block at offset %d failed: %w", blockStart, err)
		}
		w.record(plan, blockStart, blockEnd)
	}

	return nil
}

// record appends one ledger segment per range intersected by the block
// just written. It is called only after the block write has succeeded, so
// every ledger entry corresponds to durable device content.
func (w *RestoreWriter) record(plan []Range, blockStart, blockEnd int64) {
	for i := range plan {
		r := plan[i]
		rEnd := r.Offset + r.Length
		if rEnd <= blockStart || r.Offset >= blockEnd {
			continue
		}
		segStart := r.Offset
		if segStart < blockStart {
			segStart = blockStart
		}
		segEnd := rEnd
		if segEnd > blockEnd {
			segEnd = blockEnd
		}
		w.segments = append(w.segments, Segment{
			Offset: segStart,
			Length: segEnd - segStart,
			Kind:   r.Kind,
		})
		w.covered += segEnd - segStart
	}
}

// Verify walks the ledger segment by segment and proves it covers exactly
// [0, size) with no overlaps or gaps.
func (w *RestoreWriter) Verify() error {
	var want int64
	for i, seg := range w.segments {
		if seg.Length <= 0 {
			return fmt.Errorf("%w: zero-length segment %d", ErrIncomplete, i)
		}
		if seg.Offset != want {
			if seg.Offset < want {
				return fmt.Errorf("%w: overlapping ledger segment at offset %d",
					ErrIncomplete, seg.Offset)
			}
			return fmt.Errorf("%w: gap of %d bytes before offset %d",
				ErrIncomplete, seg.Offset-want, seg.Offset)
		}
		next, ok := add64(seg.Offset, seg.Length)
		if !ok {
			return fmt.Errorf("%w: segment offset overflow at %d", ErrIncomplete, seg.Offset)
		}
		want = next
	}
	if want != w.size {
		return fmt.Errorf("%w: covered %d of %d bytes", ErrIncomplete, want, w.size)
	}
	return nil
}

// Covered returns the ledger and the total number of bytes covered so far.
func (w *RestoreWriter) Covered() ([]Segment, int64) {
	out := make([]Segment, len(w.segments))
	copy(out, w.segments)
	return out, w.covered
}

// Publish performs the pre-release proof: the ledger must cover the whole
// logical image, and every hole segment must read back from the device as
// zero. On success the restore is safe to expose.
func (w *RestoreWriter) Publish() error {
	if err := w.Verify(); err != nil {
		return err
	}

	zero := make([]byte, w.blockSz)
	readBuf := make([]byte, w.blockSz)
	for _, seg := range w.segments {
		if seg.Kind != Hole {
			continue
		}
		pos := seg.Offset
		remaining := seg.Length
		for remaining > 0 {
			n := remaining
			if n > w.blockSz {
				n = w.blockSz
			}
			if _, err := w.dev.ReadAt(readBuf[:n], pos); err != nil {
				return fmt.Errorf("netsec: hole proof read at offset %d failed: %w", pos, err)
			}
			if !bytes.Equal(readBuf[:n], zero[:n]) {
				return fmt.Errorf("%w: hole segment at offset %d length %d",
					ErrStaleData, seg.Offset, seg.Length)
			}
			pos += n
			remaining -= n
		}
	}
	return nil
}

// validateRanges proves the ranges form an exact partition of [0, size):
// sorted, non-overlapping, gap-free, in bounds, with payloads matching
// their kind and length.
func validateRanges(ranges []Range, size int64) error {
	var cursor int64
	for i, r := range ranges {
		if i > 0 && r.Offset < ranges[i-1].Offset {
			return fmt.Errorf("%w: range %d not sorted", ErrInvalidPlan, i)
		}
		if r.Offset < 0 || r.Length <= 0 {
			return fmt.Errorf("%w: range %d has invalid offset/length", ErrInvalidPlan, i)
		}
		if r.Offset != cursor {
			if r.Offset < cursor {
				return fmt.Errorf("%w: range %d overlaps previous range at offset %d",
					ErrInvalidPlan, i, r.Offset)
			}
			return fmt.Errorf("%w: gap of %d bytes before range %d",
				ErrInvalidPlan, r.Offset-cursor, i)
		}
		end, ok := add64(r.Offset, r.Length)
		if !ok || end > size {
			return fmt.Errorf("%w: range %d exceeds logical size %d", ErrInvalidPlan, i, size)
		}
		switch r.Kind {
		case Data:
			if int64(len(r.Payload)) != r.Length {
				return fmt.Errorf("%w: data range %d payload length %d != range length %d",
					ErrInvalidPlan, i, len(r.Payload), r.Length)
			}
		case Hole:
			if r.Payload != nil {
				return fmt.Errorf("%w: hole range %d must carry no payload", ErrInvalidPlan, i)
			}
		default:
			return fmt.Errorf("%w: range %d has unknown kind %d", ErrInvalidPlan, i, r.Kind)
		}
		cursor = end
	}
	if cursor != size {
		return fmt.Errorf("%w: ranges cover %d of %d bytes", ErrInvalidPlan, cursor, size)
	}
	return nil
}

func add64(a, b int64) (int64, bool) {
	s := a + b
	if s < a {
		return 0, false
	}
	return s, true
}

func mul64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	p := a * b
	if p/a != b {
		return 0, false
	}
	return p, true
}
