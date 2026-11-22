package colarchive

import (
	"errors"
	"fmt"
	"sort"
)

// ErrRowOutOfRange is returned when a query names a row outside the archive.
var ErrRowOutOfRange = errors.New("colarchive: row out of range")

// RowSpan is an arithmetic progression of global logical row numbers:
//
//	Start, Start+Step, ..., Start+(Count-1)*Step
//
// Step must be at least one.  Count zero denotes an empty span.
type RowSpan struct {
	Start int64
	Step  int64
	Count int64
}

// Last returns the last row in the span.  The result is only meaningful when
// Count > 0.
func (s RowSpan) Last() int64 { return s.Start + (s.Count-1)*s.Step }

// Validate checks the span.
func (s RowSpan) Validate() error {
	if s.Start < 0 {
		return fmt.Errorf("colarchive: negative row start %d", s.Start)
	}
	if s.Count < 0 {
		return fmt.Errorf("colarchive: negative row count %d", s.Count)
	}
	if s.Step < 1 {
		return fmt.Errorf("colarchive: row step must be positive, got %d", s.Step)
	}
	if s.Count > 1 {
		delta, ok := mulInt64(s.Count-1, s.Step)
		if !ok {
			return fmt.Errorf("colarchive: row span overflows")
		}
		if _, ok := addInt64(s.Start, delta); !ok {
			return fmt.Errorf("colarchive: row span ends out of range")
		}
	}
	return nil
}

// Query asks for selected logical bits of a union of row progressions.  Bits
// are sorted and deduplicated before use.  All queried rows must belong to
// blocks whose width contains every selected bit.
type Query struct {
	Rows []RowSpan
	Bits []int
}

// MapPiece is one compact piece of the reverse (physical-to-logical) mapping.
// For a non-null piece, physical bit `PhysicalStart + k*PhysicalStep`
// corresponds to logical bit LogicalBit of row `RowStart + k*RowStep`.
//
// A null piece has Null set and no physical location.  Null status is a
// property of the row, not of a particular bit.
type MapPiece struct {
	Block         int
	LogicalBit    int
	RowStart      int64
	RowStep       int64
	RowCount      int64
	PhysicalStart int64
	PhysicalStep  int64
	Null          bool
}

// RowAt returns the logical row for index k of the piece.
func (p MapPiece) RowAt(k int64) int64 { return p.RowStart + k*p.RowStep }

// PhysicalBitAt returns the physical bit for index k of a non-null piece.
func (p MapPiece) PhysicalBitAt(k int64) int64 { return p.PhysicalStart + k*p.PhysicalStep }

// NullSpan is a compact record of queried rows that are null.  The null
// status applies to every requested bit of those rows.
type NullSpan struct {
	Block    int
	RowStart int64
	RowStep  int64
	RowCount int64
}

// RowAt returns the logical row for index k.
func (s NullSpan) RowAt(k int64) int64 { return s.RowStart + k*s.RowStep }

// BitRef identifies one logical bit of one row.
type BitRef struct {
	Block      int
	Row        int64
	LogicalBit int
}

type mappingResult struct {
	pieces []MapPiece
	nulls  []NullSpan
}

func sortedBits(bits []int) ([]int, error) {
	out := append([]int(nil), bits...)
	sort.Ints(out)
	dst := out[:0]
	for _, b := range out {
		if b < 0 {
			return nil, fmt.Errorf("colarchive: negative logical bit %d", b)
		}
		if len(dst) == 0 || dst[len(dst)-1] != b {
			dst = append(dst, b)
		}
	}
	return dst, nil
}

// buildMapping turns a query into compact logical-to-physical (and null)
// pieces.  It performs no cost optimisation and does not expand row spans.
func buildMapping(l *Layout, q Query) (*mappingResult, error) {
	bits, err := sortedBits(q.Bits)
	if err != nil {
		return nil, err
	}
	for _, s := range q.Rows {
		if err := s.Validate(); err != nil {
			return nil, err
		}
		if s.Count > 0 && s.Last() >= l.TotalRows {
			return nil, fmt.Errorf("%w: row %d, archive has %d rows", ErrRowOutOfRange, s.Last(), l.TotalRows)
		}
	}
	res := &mappingResult{}
	if len(bits) == 0 {
		return res, nil
	}
	blocks := l.blockList()
	if len(blocks) == 0 {
		return res, nil
	}
	for _, s := range q.Rows {
		if s.Count == 0 {
			continue
		}
		start, step, count := s.Start, s.Step, s.Count
		// Clip to the archive's global row range.
		start, count, ok := intersectAP(start, step, count, 0, l.TotalRows)
		if !ok {
			continue
		}
		// Find the first block that may intersect.
		bi := sort.Search(len(blocks), func(i int) bool {
			return blocks[i].RowBase+blockRows(l, blocks[i]) > start
		})
		for ; bi < len(blocks) && count > 0; bi++ {
			bl := blocks[bi]
			rows := blockRows(l, bl)
			blockStart := bl.RowBase
			blockEnd := blockStart + rows
			segStart, segCount, ok := intersectAP(start, step, count, blockStart, blockEnd)
			if !ok {
				if start >= blockEnd {
					continue
				}
				break
			}
			if err := appendBlockSegment(l, bl, bits, segStart-blockStart, step, segCount, res); err != nil {
				return nil, err
			}
			// Advance the progression past this block.
			consumed, ok := mulInt64(segCount, step)
			if !ok {
				break
			}
			start, ok = addInt64(segStart, consumed)
			if !ok {
				break
			}
			count -= segCount
			if count <= 0 {
				break
			}
		}
	}
	return res, nil
}

func blockRows(l *Layout, bl *BlockLayout) int64 {
	return l.Archive.Groups[bl.Group].Blocks[bl.Block].Rows
}

func appendBlockSegment(l *Layout, bl *BlockLayout, bits []int, localStart, step, count int64, res *mappingResult) error {
	b := &l.Archive.Groups[bl.Group].Blocks[bl.Block]
	for _, bit := range bits {
		if bit >= b.Width {
			return fmt.Errorf("colarchive: logical bit %d out of range for block %d (width %d)", bit, bl.Index, b.Width)
		}
	}
	if count == 0 {
		return nil
	}
	end := localStart + (count-1)*step // exclusive local row bound is end+1
	// Locate the first null run containing localStart.
	ri := sort.Search(len(bl.runStart), func(i int) bool { return bl.runStart[i] > localStart }) - 1
	if ri < 0 {
		ri = 0
	}
	for ri < len(bl.runStart) {
		runStart := bl.runStart[ri]
		runEnd := b.Rows
		if ri+1 < len(bl.runStart) {
			runEnd = bl.runStart[ri+1]
		}
		if runEnd <= localStart {
			ri++
			continue
		}
		if runStart > end {
			break
		}
		segStart, segCount, ok := intersectAP(localStart, step, count, runStart, runEnd)
		if !ok {
			if localStart >= runEnd {
				ri++
				continue
			}
			break
		}
		globalRow := bl.RowBase + segStart
		if bl.runIsNull[ri] {
			res.nulls = append(res.nulls, NullSpan{
				Block: bl.Index, RowStart: globalRow, RowStep: step, RowCount: segCount,
			})
		} else {
			q0 := bl.runNonNull[ri] + (segStart - runStart)
			for _, bit := range bits {
				res.pieces = append(res.pieces, MapPiece{
					Block:         bl.Index,
					LogicalBit:    bit,
					RowStart:      globalRow,
					RowStep:       step,
					RowCount:      segCount,
					PhysicalStart: bl.PlaneBase[bit] + q0,
					PhysicalStep:  step,
				})
			}
		}
		// Advance local progression past this run.
		consumed, ok := mulInt64(segCount, step)
		if !ok {
			break
		}
		localStart, ok = addInt64(segStart, consumed)
		if !ok {
			break
		}
		count -= segCount
		if count <= 0 {
			break
		}
		ri++
	}
	return nil
}

func intersectAP(start, step, count, lo, hi int64) (int64, int64, bool) {
	if count <= 0 || step <= 0 || hi <= lo {
		return 0, 0, false
	}
	if start < lo {
		delta := lo - start
		k := (delta + step - 1) / step
		if k >= count {
			return 0, 0, false
		}
		start += k * step
		count -= k
	}
	if start >= hi {
		return 0, 0, false
	}
	maxCount := (hi-1-start)/step + 1
	if count > maxCount {
		count = maxCount
	}
	if count <= 0 {
		return 0, 0, false
	}
	return start, count, true
}
