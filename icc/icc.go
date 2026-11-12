// Package icc parses the metadata of ICC color profiles for image
// pipelines. It reads only the header, the tag table, and the size
// fields of mft2/curv tag payloads. All offset and length arithmetic
// is overflow-safe, and the tag count, curve point count, and CLUT
// byte size are bounded by a caller-supplied budget, so hostile
// profiles cannot trigger large allocations.
package icc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
)

const (
	// HeaderSize is the fixed ICC profile header size in bytes.
	HeaderSize = 128

	tagEntrySize = 12

	mft2HeaderSize = 52 // type sig + reserved + channels + grid + matrix + table entry counts
	curvHeaderSize = 12 // type sig + reserved + point count
)

var (
	// ErrTruncated is returned when the profile data ends before a
	// declared structure.
	ErrTruncated = errors.New("icc: profile truncated")
	// ErrOverflow is returned when size arithmetic overflows.
	ErrOverflow = errors.New("icc: size arithmetic overflow")
	// ErrBudget is returned when a structure exceeds the caller's
	// resource budget.
	ErrBudget = errors.New("icc: resource budget exceeded")
	// ErrMalformed is returned when a tag's declared sizes are
	// inconsistent with its bounds.
	ErrMalformed = errors.New("icc: malformed tag data")
)

// Budget caps the resources a profile may consume. Parsing fails with
// ErrBudget as soon as any limit is exceeded, before allocating
// memory proportional to attacker-controlled values.
type Budget struct {
	// MaxTags is the maximum number of tag table entries.
	MaxTags uint32
	// MaxCurvePoints is the maximum number of points in a curv tag.
	MaxCurvePoints uint32
	// MaxCLUTBytes is the maximum byte size of an mft2 CLUT.
	MaxCLUTBytes uint64
}

// Profile is the parsed metadata of an ICC profile.
type Profile struct {
	// Size is the profile size declared in the header.
	Size uint32
	Tags []Tag
}

// Tag is a single tag table entry plus parsed size metadata for
// known payload types.
type Tag struct {
	Signature string // 4-byte tag signature, e.g. "rTRC"
	Type      string // 4-byte type signature at the tag offset, e.g. "curv"
	Offset    uint32
	Size      uint32

	// Curve is set when Type is "curv".
	Curve *CurveInfo
	// MFT2 is set when Type is "mft2".
	MFT2 *MFT2Info
}

// CurveInfo describes a curv tag payload.
type CurveInfo struct {
	Points uint32
}

// MFT2Info describes an mft2 (Lut16Type) tag payload.
type MFT2Info struct {
	InputChannels  uint8
	OutputChannels uint8
	GridPoints     uint8
	CLUTBytes      uint64
}

// Parse reads ICC profile metadata from r, which must hold exactly
// size bytes of profile data. It never allocates memory proportional
// to values in the profile before checking them against b.
func Parse(r io.ReaderAt, size int64, b Budget) (*Profile, error) {
	if size < HeaderSize+4 {
		return nil, ErrTruncated
	}
	hdr := make([]byte, HeaderSize)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return nil, mapErr(err)
	}
	p := &Profile{Size: binary.BigEndian.Uint32(hdr[0:4])}
	if uint64(p.Size) > uint64(size) {
		return nil, fmt.Errorf("%w: declared size %d exceeds data %d", ErrMalformed, p.Size, size)
	}

	var cntBuf [4]byte
	if _, err := r.ReadAt(cntBuf[:], HeaderSize); err != nil {
		return nil, mapErr(err)
	}
	count := binary.BigEndian.Uint32(cntBuf[:])
	if count > b.MaxTags {
		return nil, fmt.Errorf("%w: tag count %d exceeds limit %d", ErrBudget, count, b.MaxTags)
	}
	// count is bounded by the budget, so the table fits in memory and
	// its extent cannot overflow int64.
	tableEnd := int64(HeaderSize) + 4 + int64(count)*tagEntrySize
	if tableEnd > size {
		return nil, ErrTruncated
	}
	table := make([]byte, int64(count)*tagEntrySize)
	if _, err := r.ReadAt(table, HeaderSize+4); err != nil {
		return nil, mapErr(err)
	}

	p.Tags = make([]Tag, 0, count)
	for i := uint32(0); i < count; i++ {
		e := table[i*tagEntrySize:]
		t := Tag{
			Signature: string(e[0:4]),
			Offset:    binary.BigEndian.Uint32(e[4:8]),
			Size:      binary.BigEndian.Uint32(e[8:12]),
		}
		// 64-bit addition of two uint32 ranges cannot wrap.
		if uint64(t.Offset)+uint64(t.Size) > uint64(size) {
			return nil, fmt.Errorf("%w: tag %q range [%d,%d) exceeds data %d",
				ErrTruncated, t.Signature, t.Offset, uint64(t.Offset)+uint64(t.Size), size)
		}
		if err := t.inspect(r, b); err != nil {
			return nil, err
		}
		p.Tags = append(p.Tags, t)
	}
	return p, nil
}

// inspect reads the size fields of known tag payload types.
func (t *Tag) inspect(r io.ReaderAt, b Budget) error {
	if t.Size < 4 {
		return nil
	}
	var sig [4]byte
	if _, err := r.ReadAt(sig[:], int64(t.Offset)); err != nil {
		return mapErr(err)
	}
	t.Type = string(sig[:])
	switch t.Type {
	case "curv":
		return t.inspectCurv(r, b)
	case "mft2":
		return t.inspectMFT2(r, b)
	}
	return nil
}

func (t *Tag) inspectCurv(r io.ReaderAt, b Budget) error {
	if t.Size < curvHeaderSize {
		return fmt.Errorf("%w: curv tag %q size %d too small", ErrMalformed, t.Signature, t.Size)
	}
	var hdr [curvHeaderSize]byte
	if _, err := r.ReadAt(hdr[:], int64(t.Offset)); err != nil {
		return mapErr(err)
	}
	count := binary.BigEndian.Uint32(hdr[8:12])
	if count > b.MaxCurvePoints {
		return fmt.Errorf("%w: curv tag %q has %d points, limit %d",
			ErrBudget, t.Signature, count, b.MaxCurvePoints)
	}
	if uint64(curvHeaderSize)+uint64(count)*2 > uint64(t.Size) {
		return fmt.Errorf("%w: curv tag %q data exceeds tag size", ErrMalformed, t.Signature)
	}
	t.Curve = &CurveInfo{Points: count}
	return nil
}

func (t *Tag) inspectMFT2(r io.ReaderAt, b Budget) error {
	if t.Size < mft2HeaderSize {
		return fmt.Errorf("%w: mft2 tag %q size %d too small", ErrMalformed, t.Signature, t.Size)
	}
	var hdr [mft2HeaderSize]byte
	if _, err := r.ReadAt(hdr[:], int64(t.Offset)); err != nil {
		return mapErr(err)
	}
	inCh, outCh, grid := hdr[8], hdr[9], hdr[10]
	inEntries := binary.BigEndian.Uint16(hdr[48:50])
	outEntries := binary.BigEndian.Uint16(hdr[50:52])

	clut, ok := clutBytes(grid, inCh, outCh)
	if !ok {
		return fmt.Errorf("%w: mft2 tag %q CLUT grid %d^%d overflows",
			ErrOverflow, t.Signature, grid, inCh)
	}
	if clut > b.MaxCLUTBytes {
		return fmt.Errorf("%w: mft2 tag %q CLUT is %d bytes, limit %d",
			ErrBudget, t.Signature, clut, b.MaxCLUTBytes)
	}
	total := uint64(mft2HeaderSize) +
		uint64(inCh)*uint64(inEntries)*2 +
		clut +
		uint64(outCh)*uint64(outEntries)*2
	if total > uint64(t.Size) {
		return fmt.Errorf("%w: mft2 tag %q tables need %d bytes, tag has %d",
			ErrMalformed, t.Signature, total, t.Size)
	}
	t.MFT2 = &MFT2Info{
		InputChannels:  inCh,
		OutputChannels: outCh,
		GridPoints:     grid,
		CLUTBytes:      clut,
	}
	return nil
}

// clutBytes computes gridPoints^inputChannels * outputChannels * 2
// with overflow-checked multiplication.
func clutBytes(gridPoints, inputChannels, outputChannels uint8) (uint64, bool) {
	cells := uint64(1)
	for i := uint8(0); i < inputChannels; i++ {
		hi, lo := bits.Mul64(cells, uint64(gridPoints))
		if hi != 0 {
			return 0, false
		}
		cells = lo
	}
	hi, lo := bits.Mul64(cells, uint64(outputChannels)*2)
	if hi != 0 {
		return 0, false
	}
	return lo, true
}

func mapErr(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrTruncated
	}
	return err
}
