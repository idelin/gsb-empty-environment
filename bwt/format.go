package bwt

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math"
)

// On-disk block layout (all integers little-endian):
//
//	offset  size  field
//	0       4     magic "BWTK"
//	4       1     format version (= Version)
//	5       1     flags (bit 0: FlagEmpty)
//	6       2     reserved, must be zero
//	8       8     original block length in bytes
//	16      8     primary index
//	24      4     CRC-32 (IEEE) of the payload
//	28      ...   payload: BWT last column, exactly length bytes
//
// An empty block is defined as FlagEmpty set, length 0, primary 0 and
// no payload; its inverse is the empty byte string. Blocks may be
// concatenated; Decode consumes exactly one block from the reader.
const (
	Version   = 1
	FlagEmpty = 1 << 0

	knownFlags = FlagEmpty
	headerSize = 28

	// MaxBlockSize bounds a single block so that decoded output and
	// index structures stay within int32-addressable memory.
	MaxBlockSize = math.MaxInt32
)

var magic = [4]byte{'B', 'W', 'T', 'K'}

var (
	ErrBadMagic      = errors.New("bwt: bad magic")
	ErrVersion       = errors.New("bwt: unsupported format version")
	ErrFlags         = errors.New("bwt: unknown or reserved flags set")
	ErrEmptyMismatch = errors.New("bwt: empty flag inconsistent with length")
	ErrBlockTooLarge = errors.New("bwt: block length exceeds maximum")
	ErrOutputLimit   = errors.New("bwt: block exceeds decode output budget")
	ErrTruncated     = errors.New("bwt: truncated block")
	ErrChecksum      = errors.New("bwt: payload checksum mismatch")
	ErrLength        = errors.New("bwt: decoded length does not match header")
)

// Block is a decoded BWT block: the last column plus the metadata
// needed to invert it.
type Block struct {
	Length  uint64 // original (and decoded) length
	Primary uint64 // primary index, valid range [0, Length)
	L       []byte // BWT last column, len(L) == Length
}

// Encode transforms src and writes one block to w.
func Encode(w io.Writer, src []byte) error {
	if len(src) > MaxBlockSize {
		return ErrBlockTooLarge
	}
	last, primary := Transform(src)
	var hdr [headerSize]byte
	copy(hdr[0:4], magic[:])
	hdr[4] = Version
	if len(src) == 0 {
		hdr[5] = FlagEmpty
	}
	binary.LittleEndian.PutUint64(hdr[8:16], uint64(len(src)))
	binary.LittleEndian.PutUint64(hdr[16:24], uint64(primary))
	binary.LittleEndian.PutUint32(hdr[24:28], crc32.ChecksumIEEE(last))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(last)
	return err
}

// Decode reads exactly one block from r. maxOutput bounds the decoded
// block length: a header declaring a larger length is rejected with
// ErrOutputLimit before any payload is allocated, so memory use to
// scale with the budget rather than with untrusted input.
func Decode(r io.Reader, maxOutput uint64) (*Block, error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, ErrTruncated
	}
	if string(hdr[0:4]) != string(magic[:]) {
		return nil, ErrBadMagic
	}
	if hdr[4] != Version {
		return nil, ErrVersion
	}
	flags := hdr[5]
	if flags&^knownFlags != 0 || hdr[6] != 0 || hdr[7] != 0 {
		return nil, ErrFlags
	}
	length := binary.LittleEndian.Uint64(hdr[8:16])
	primary := binary.LittleEndian.Uint64(hdr[16:24])
	crc := binary.LittleEndian.Uint32(hdr[24:28])

	empty := flags&FlagEmpty != 0
	if empty != (length == 0) {
		return nil, ErrEmptyMismatch
	}
	if length > MaxBlockSize {
		return nil, ErrBlockTooLarge
	}
	if length > maxOutput {
		return nil, ErrOutputLimit
	}
	if length == 0 {
		if primary != 0 {
			return nil, ErrPrimaryIndex
		}
	} else if primary >= length {
		return nil, ErrPrimaryIndex
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, ErrTruncated
	}
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, ErrChecksum
	}
	return &Block{Length: length, Primary: primary, L: payload}, nil
}

// Inverse decodes the block back to the original bytes. The result is
// guaranteed to have exactly Length bytes.
func (b *Block) Inverse() ([]byte, error) {
	if uint64(len(b.L)) != b.Length {
		return nil, ErrLength
	}
	if b.Primary > uint64(math.MaxInt32) {
		return nil, ErrPrimaryIndex
	}
	out, err := Inverse(b.L, int(b.Primary))
	if err != nil {
		return nil, err
	}
	if uint64(len(out)) != b.Length {
		return nil, ErrLength
	}
	return out, nil
}

// Index builds the search index for this block.
func (b *Block) Index() (*Index, error) {
	if uint64(len(b.L)) != b.Length {
		return nil, ErrLength
	}
	if b.Primary > uint64(math.MaxInt32) {
		return nil, ErrPrimaryIndex
	}
	return NewIndex(b.L, int(b.Primary))
}
