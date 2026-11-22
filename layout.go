// Package colarchive plans and describes reads from a bit-packed, bit-plane
// transposed column archive.
//
// The on-disk model implemented by this package is intentionally small and
// explicit:
//
//   - an archive is a sequence of groups placed back to back;
//   - a group contains one or more blocks, concatenated at bit granularity;
//   - a block has Rows logical rows and a fixed Width.  Its data occupies
//     Width bit planes.  Plane p stores logical bit Planes[p] for every
//     non-null row, in row order;
//   - null rows do not contribute a bit to any plane;
//   - a group is zero padded to the next absolute 8-byte boundary.
//
// Bits are numbered from zero at the most significant bit of a byte:
// bit t lives in byte t/8 at mask 1<<(7-(t%8)).  Consecutive bits therefore
// run from the high bit to the low bit of a byte.
//
// Logical rows are numbered globally by concatenating blocks in group order
// and then block order.
package colarchive

import (
	"errors"
	"fmt"
	"sort"
)

// ErrInvalidArchive is returned when an archive description is inconsistent.
var ErrInvalidArchive = errors.New("colarchive: invalid archive")

// NullRun is one run of the null bitmap of a block.  Runs are concatenated
// and must cover exactly Block.Rows rows.  A null run contributes no data
// bits.  Adjacent runs with the same Null value are permitted but redundant.
type NullRun struct {
	Rows int64
	Null bool
}

// Block describes one independently parameterised block.
//
// Planes is a permutation of 0..Width-1.  Planes[p] is the logical bit
// number stored in physical plane p.  Thus a logical value bit b is found in
// plane p where Planes[p] == b.
type Block struct {
	Rows   int64
	Width  int
	Planes []int
	Nulls  []NullRun
}

// Group is a concatenation of blocks followed by alignment padding.  Groups
// do not carry independent offsets: they are laid out back to back from
// Archive.BaseOffset.  Each group's end is aligned to an absolute 8-byte
// boundary.
type Group struct {
	Blocks []Block
}

// Archive is a complete archive description.
type Archive struct {
	// BaseOffset is the absolute byte offset of the first group.  It may be
	// non-zero when the archive sits after a header.  The first group starts
	// exactly at BaseOffset.
	BaseOffset int64
	Groups     []Group
}

// BlockLayout is the resolved physical location of a block.
type BlockLayout struct {
	Index     int   // global block index
	Group     int   // group index
	Block     int   // block index inside the group
	RowBase   int64 // first global logical row
	StartBit  int64 // absolute bit offset of physical plane 0, row 0
	EndBit    int64 // absolute bit offset one past the block data
	NonNull   int64
	PlaneBase []int64 // PlaneBase[b] = absolute bit of logical bit b at q=0

	runStart   []int64 // first row of each null run
	runNonNull []int64 // non-null rows before each run
	runIsNull  []bool
}

// GroupLayout is the resolved byte range and blocks of a group.
type GroupLayout struct {
	Index     int
	StartByte int64 // group start
	EndByte   int64 // padded end, an absolute multiple of 8
	DataBits  int64
	Blocks    []BlockLayout
}

// Layout is the result of resolving an Archive.
type Layout struct {
	Archive    *Archive
	BaseOffset int64
	Groups     []GroupLayout
	TotalRows  int64
}

// Validate checks the archive description without resolving offsets.
func (a *Archive) Validate() error {
	if a == nil {
		return fmt.Errorf("%w: nil archive", ErrInvalidArchive)
	}
	if a.BaseOffset < 0 {
		return fmt.Errorf("%w: negative base offset", ErrInvalidArchive)
	}
	for gi := range a.Groups {
		for bi := range a.Groups[gi].Blocks {
			if err := a.Groups[gi].Blocks[bi].validate(gi, bi); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Block) validate(gi, bi int) error {
	where := fmt.Sprintf("group %d block %d", gi, bi)
	if b.Rows < 0 {
		return fmt.Errorf("%w: %s: negative row count", ErrInvalidArchive, where)
	}
	if b.Width < 1 || b.Width > 256 {
		return fmt.Errorf("%w: %s: width %d out of range", ErrInvalidArchive, where, b.Width)
	}
	if len(b.Planes) != b.Width {
		return fmt.Errorf("%w: %s: plane permutation has length %d, want %d", ErrInvalidArchive, where, len(b.Planes), b.Width)
	}
	seen := make([]bool, b.Width)
	for _, p := range b.Planes {
		if p < 0 || p >= b.Width {
			return fmt.Errorf("%w: %s: plane index %d out of range", ErrInvalidArchive, where, p)
		}
		if seen[p] {
			return fmt.Errorf("%w: %s: duplicate plane index %d", ErrInvalidArchive, where, p)
		}
		seen[p] = true
	}
	var rows int64
	for ri, r := range b.Nulls {
		if r.Rows < 0 {
			return fmt.Errorf("%w: %s: null run %d has negative length", ErrInvalidArchive, where, ri)
		}
		if r.Rows == 0 {
			return fmt.Errorf("%w: %s: null run %d is empty", ErrInvalidArchive, where, ri)
		}
		var ok bool
		rows, ok = addInt64(rows, r.Rows)
		if !ok {
			return fmt.Errorf("%w: %s: row count overflow", ErrInvalidArchive, where)
		}
	}
	if rows != b.Rows {
		return fmt.Errorf("%w: %s: null runs cover %d rows, block has %d", ErrInvalidArchive, where, rows, b.Rows)
	}
	return nil
}

// NonNull returns the number of non-null rows in the block.
func (b *Block) NonNull() (int64, bool) {
	var n int64
	for _, r := range b.Nulls {
		if !r.Null {
			var ok bool
			n, ok = addInt64(n, r.Rows)
			if !ok {
				return 0, false
			}
		}
	}
	return n, true
}

// Layout resolves all block and group offsets, validating the description.
func (a *Archive) Layout() (*Layout, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	l := &Layout{Archive: a, BaseOffset: a.BaseOffset}
	groupStart := a.BaseOffset
	if groupStart < 0 {
		return nil, fmt.Errorf("%w: negative base offset", ErrInvalidArchive)
	}
	var globalBlock int
	for gi := range a.Groups {
		g := &a.Groups[gi]
		gl := GroupLayout{Index: gi, StartByte: groupStart}
		groupStartBit, ok := mulInt64(groupStart, 8)
		if !ok {
			return nil, fmt.Errorf("%w: group %d start offset overflows", ErrInvalidArchive, gi)
		}
		bitCursor := groupStartBit
		var dataBits int64
		for bi := range g.Blocks {
			b := &g.Blocks[bi]
			nn, ok := b.NonNull()
			if !ok {
				return nil, fmt.Errorf("%w: group %d block %d: row count overflow", ErrInvalidArchive, gi, bi)
			}
			planeBits, ok := mulInt64(int64(b.Width), nn)
			if !ok {
				return nil, fmt.Errorf("%w: group %d block %d: data bit count overflow", ErrInvalidArchive, gi, bi)
			}
			bl := BlockLayout{
				Index:      globalBlock,
				Group:      gi,
				Block:      bi,
				RowBase:    l.TotalRows,
				StartBit:   bitCursor,
				NonNull:    nn,
				PlaneBase:  make([]int64, b.Width),
				runStart:   make([]int64, len(b.Nulls)),
				runNonNull: make([]int64, len(b.Nulls)),
				runIsNull:  make([]bool, len(b.Nulls)),
			}
			var rowCursor, nonNullCursor int64
			for ri, r := range b.Nulls {
				bl.runStart[ri] = rowCursor
				bl.runNonNull[ri] = nonNullCursor
				bl.runIsNull[ri] = r.Null
				rowCursor += r.Rows
				if !r.Null {
					nonNullCursor += r.Rows
				}
			}
			for p, logical := range b.Planes {
				off, ok := addInt64(bitCursor, int64(p)*nn)
				if !ok {
					return nil, fmt.Errorf("%w: group %d block %d: plane offset overflow", ErrInvalidArchive, gi, bi)
				}
				bl.PlaneBase[logical] = off
			}
			endBit, ok := addInt64(bitCursor, planeBits)
			if !ok {
				return nil, fmt.Errorf("%w: group %d block %d: end bit overflow", ErrInvalidArchive, gi, bi)
			}
			bl.EndBit = endBit
			gl.Blocks = append(gl.Blocks, bl)
			dataBits, ok = addInt64(dataBits, planeBits)
			if !ok {
				return nil, fmt.Errorf("%w: group %d: data bit count overflow", ErrInvalidArchive, gi)
			}
			bitCursor = endBit
			var okRows bool
			l.TotalRows, okRows = addInt64(l.TotalRows, b.Rows)
			if !okRows {
				return nil, fmt.Errorf("%w: total row count overflow", ErrInvalidArchive)
			}
			globalBlock++
		}
		endByte, ok := addInt64(groupStart, ceilDiv64(dataBits, 8))
		if !ok {
			return nil, fmt.Errorf("%w: group %d: byte count overflow", ErrInvalidArchive, gi)
		}
		endByte = alignUp8(endByte)
		if endByte < groupStart {
			return nil, fmt.Errorf("%w: group %d: byte count overflow", ErrInvalidArchive, gi)
		}
		gl.EndByte = endByte
		gl.DataBits = dataBits
		l.Groups = append(l.Groups, gl)
		groupStart = endByte
	}
	return l, nil
}

// BlockForRow returns the block containing a global logical row.
func (l *Layout) BlockForRow(row int64) (*BlockLayout, error) {
	if row < 0 || row >= l.TotalRows {
		return nil, fmt.Errorf("colarchive: row %d out of range [0,%d)", row, l.TotalRows)
	}
	blocks := l.blockList()
	i := sort.Search(len(blocks), func(i int) bool {
		b := blocks[i]
		return b.RowBase+blockRows(l, b) > row
	})
	if i == len(blocks) || row < blocks[i].RowBase {
		return nil, fmt.Errorf("colarchive: internal row lookup failure for %d", row)
	}
	return blocks[i], nil
}

// blockAt returns the resolved block with the given global index.
func (l *Layout) blockAt(index int) (*BlockLayout, *Block, error) {
	for gi := range l.Groups {
		for bi := range l.Groups[gi].Blocks {
			b := &l.Groups[gi].Blocks[bi]
			if b.Index == index {
				return b, &l.Archive.Groups[gi].Blocks[bi], nil
			}
		}
	}
	return nil, nil, fmt.Errorf("colarchive: block index %d out of range", index)
}

func (l *Layout) blockList() []*BlockLayout {
	n := 0
	for gi := range l.Groups {
		n += len(l.Groups[gi].Blocks)
	}
	out := make([]*BlockLayout, 0, n)
	for gi := range l.Groups {
		for bi := range l.Groups[gi].Blocks {
			out = append(out, &l.Groups[gi].Blocks[bi])
		}
	}
	return out
}

func addInt64(a, b int64) (int64, bool) {
	c := a + b
	if (b > 0 && c < a) || (b < 0 && c > a) {
		return 0, false
	}
	return c, true
}

func mulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	c := a * b
	if c/b != a || (a == -1 && b == -1<<63) || (b == -1 && a == -1<<63) {
		return 0, false
	}
	return c, true
}

func ceilDiv64(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func alignUp8(x int64) int64 {
	return (x + 7) &^ 7
}

// gcd64 returns the positive greatest common divisor.
func gcd64(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
