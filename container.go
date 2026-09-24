package secded

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// Container format (all integers little-endian):
//
//	offset  size  field
//	0       4     magic "SCD1"
//	4       2     format version (= FormatVersion)
//	6       1     M (Hamming parity bits of the code)
//	7       1     interleave depth (words per block)
//	8       2     flags (currently 0)
//	10      2     reserved (0)
//	12      8     DataLen: original payload length in bytes
//	20      8     BlockCount: number of interleaved blocks
//	28      4     CRC-32C of the ORIGINAL (decoded) data
//	32      4     CRC-32C of header bytes 0..31
//	36      ...   interleaved payload, BlockCount * BlockStreamBytes bytes
//
// The CRC covers the decoded data, not the transmitted payload: the
// whole point of the ECC is that the payload may legitimately arrive
// corrupted and be repaired. After decoding, the data CRC is verified,
// which also catches miscorrections (e.g. 3 bit errors aliasing to a
// single-error syndrome). Note that a matching CRC is strong evidence,
// not proof: a 32-bit CRC can in principle collide, so Decode still
// reports exactly which blocks were corrected instead of claiming
// certainty.
//
// The decoder verifies magic, version, header CRC and exact structural
// lengths before touching any payload, and refuses to allocate more
// than Options.MaxOutputBytes for the decoded output.

const (
	FormatVersion = 1
	headerSize    = 36
)

var magic = [4]byte{'S', 'C', 'D', '1'}

var (
	ErrBadMagic       = errors.New("secded: bad magic")
	ErrBadVersion     = errors.New("secded: unsupported format version")
	ErrBadHeaderCRC   = errors.New("secded: header CRC mismatch")
	ErrBadLength      = errors.New("secded: declared lengths inconsistent with input size")
	ErrOutputTooLarge = errors.New("secded: declared output exceeds MaxOutputBytes quota")
	// ErrDataChecksum: every block decoded, but the decoded data does
	// not match the header CRC. This is how miscorrected or otherwise
	// inconsistent data surfaces; the data must not be trusted.
	ErrDataChecksum = errors.New("secded: decoded data CRC mismatch")
	// ErrUncorrectableBlocks: at least one block could not be repaired.
	ErrUncorrectableBlocks = errors.New("secded: uncorrectable blocks present")
)

// Options controls decoding.
type Options struct {
	// MaxOutputBytes bounds the decoded output size; 0 means DefaultMaxOutput.
	MaxOutputBytes int64
	// Erasures marks half-open ranges [Start, Start+Len) of payload
	// stream bits known to be corrupted (e.g. by a transport checksum).
	Erasures []BitRange
	// WithMapping records a BitSource for every decoded output bit.
	WithMapping bool
}

// BitRange is a half-open range of bit indices.
type BitRange struct {
	Start int64
	Len   int64
}

// DefaultMaxOutput is the default decode quota (256 MiB).
const DefaultMaxOutput = 1 << 28

// BitSource records where one decoded output bit came from and what
// happened to its codeword, so callers can audit every bit.
type BitSource struct {
	OutputBit int64    // index in the decoded data
	Block     int      // block index
	Word      int      // word index inside the block
	Position  int      // 1-indexed codeword position carrying this bit
	Fate      WordFate // how the word decoded
}

// Result is the outcome of Decode.
type Result struct {
	Data   []byte
	Blocks []BlockReport
	// Uncorrectable lists block indices that could not be repaired.
	// Their output bytes are zero-filled. A non-empty list means the
	// data is INCOMPLETE; it is never presented as recovered.
	Uncorrectable []int
	// Corrected lists block indices where at least one bit was flipped
	// back or recovered from erasures. Per SECDED limits, 3+ bit errors
	// can alias to a single error, so "corrected" is maximum-likelihood,
	// not proven - even when the data CRC matches.
	Corrected []int
	Mapping   []BitSource // only if Options.WithMapping
}

// Clean reports whether every block decoded without any correction.
func (r *Result) Clean() bool { return len(r.Uncorrectable) == 0 && len(r.Corrected) == 0 }

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// Encode packs src into the container format.
func Encode(src []byte, m, depth int) ([]byte, error) {
	code, err := New(m)
	if err != nil {
		return nil, err
	}
	bc, err := NewBlockCodec(code, depth)
	if err != nil {
		return nil, err
	}
	dataBits := len(src) * 8
	blockCount := (dataBits + bc.BlockDataBits() - 1) / bc.BlockDataBits()
	if blockCount == 0 {
		blockCount = 1
	}
	payload := make([]byte, blockCount*bc.BlockStreamBytes())
	padded := make([]byte, blockCount*bc.BlockDataBits()/8)
	copy(padded, src)
	for blk := 0; blk < blockCount; blk++ {
		bc.EncodeBlock(padded[blk*bc.BlockDataBits()/8:], payload, blk*bc.BlockStreamBits())
	}

	out := make([]byte, headerSize+len(payload))
	copy(out[0:4], magic[:])
	binary.LittleEndian.PutUint16(out[4:6], FormatVersion)
	out[6] = byte(m)
	out[7] = byte(depth)
	binary.LittleEndian.PutUint64(out[12:20], uint64(len(src)))
	binary.LittleEndian.PutUint64(out[20:28], uint64(blockCount))
	binary.LittleEndian.PutUint32(out[28:32], crc32.Checksum(src, crcTable))
	binary.LittleEndian.PutUint32(out[32:36], crc32.Checksum(out[0:32], crcTable))
	copy(out[headerSize:], payload)
	return out, nil
}

// header is the parsed, verified container header.
type header struct {
	m, depth   int
	dataLen    uint64
	blockCount uint64
	dataCRC    uint32
}

func parseHeader(blob []byte) (header, error) {
	var h header
	if len(blob) < headerSize {
		return h, ErrBadLength
	}
	if !bytes.Equal(blob[0:4], magic[:]) {
		return h, ErrBadMagic
	}
	if binary.LittleEndian.Uint16(blob[4:6]) != FormatVersion {
		return h, ErrBadVersion
	}
	want := binary.LittleEndian.Uint32(blob[32:36])
	got := crc32.Checksum(blob[0:32], crcTable)
	if want != got {
		return h, ErrBadHeaderCRC
	}
	h.m, h.depth = int(blob[6]), int(blob[7])
	h.dataLen = binary.LittleEndian.Uint64(blob[12:20])
	h.blockCount = binary.LittleEndian.Uint64(blob[20:28])
	h.dataCRC = binary.LittleEndian.Uint32(blob[28:32])
	return h, nil
}

// verify performs all structural checks and returns the codec, the
// parsed header and the payload slice. It enforces the output quota
// BEFORE any output buffer is allocated.
func verify(blob []byte, opts *Options) (*BlockCodec, header, []byte, error) {
	h, err := parseHeader(blob)
	if err != nil {
		return nil, h, nil, err
	}
	code, err := New(h.m)
	if err != nil {
		return nil, h, nil, err
	}
	bc, err := NewBlockCodec(code, h.depth)
	if err != nil {
		return nil, h, nil, err
	}
	// Structural length check: payload must be exactly blockCount blocks.
	payloadBits := h.blockCount * uint64(bc.BlockStreamBits())
	if payloadBits%8 != 0 || headerSize+payloadBits/8 != uint64(len(blob)) {
		return nil, h, nil, ErrBadLength
	}
	// The declared data length must fit in the declared blocks.
	if h.dataLen*8 > h.blockCount*uint64(bc.BlockDataBits()) {
		return nil, h, nil, ErrBadLength
	}
	// Output quota check before allocation.
	quota := opts.MaxOutputBytes
	if quota <= 0 {
		quota = DefaultMaxOutput
	}
	if h.dataLen > uint64(quota) {
		return nil, h, nil, ErrOutputTooLarge
	}
	return bc, h, blob[headerSize:], nil
}

// Decode verifies and decodes a container. On structural or integrity
// failure it returns a nil Result. When some blocks are uncorrectable
// it still returns the *Result (Uncorrectable set, those output bytes
// zeroed) together with ErrUncorrectableBlocks.
func Decode(blob []byte, opts *Options) (*Result, error) {
	if opts == nil {
		opts = &Options{}
	}
	bc, h, payload, err := verify(blob, opts)
	if err != nil {
		return nil, err
	}
	res := &Result{Data: make([]byte, h.dataLen)}
	blockDataBytes := bc.BlockDataBits() / 8
	tmp := make([]byte, blockDataBytes)

	// Bucket erasures per block.
	erasures := make([][]int, h.blockCount)
	for _, r := range opts.Erasures {
		for t := r.Start; t < r.Start+r.Len; t++ {
			blk := t / int64(bc.BlockStreamBits())
			if blk < 0 || blk >= int64(h.blockCount) {
				continue
			}
			erasures[blk] = append(erasures[blk], int(t%int64(bc.BlockStreamBits())))
		}
	}

	for blk := 0; blk < int(h.blockCount); blk++ {
		for i := range tmp {
			tmp[i] = 0
		}
		rep := bc.DecodeBlock(payload, blk*bc.BlockStreamBits(), erasures[blk], tmp, 0)
		rep.Block = blk
		res.Blocks = append(res.Blocks, rep)
		if !rep.OK {
			res.Uncorrectable = append(res.Uncorrectable, blk)
			continue
		}
		corrected := false
		for _, w := range rep.Words {
			if w.Fate == FateCorrected || w.Fate == FateErasureRecovered {
				corrected = true
			}
		}
		if corrected {
			res.Corrected = append(res.Corrected, blk)
		}
		// Copy the still-valid prefix of this block's data.
		start := int64(blk) * int64(bc.BlockDataBits()) / 8
		n := int64(blockDataBytes)
		if rem := int64(h.dataLen) - start; rem < n {
			n = rem
		}
		if n > 0 {
			copy(res.Data[start:start+n], tmp[:n])
		}
		if opts.WithMapping {
			appendMapping(bc, res, rep, blk, h.dataLen)
		}
	}
	if len(res.Uncorrectable) > 0 {
		return res, fmt.Errorf("%w: %v", ErrUncorrectableBlocks, res.Uncorrectable)
	}
	// End-to-end integrity check on the decoded data. This catches
	// miscorrections that the ECC itself cannot (e.g. 3 bit errors
	// aliasing to a single-error syndrome).
	if crc32.Checksum(res.Data, crcTable) != h.dataCRC {
		return res, ErrDataChecksum
	}
	return res, nil
}

// appendMapping records the source of every output bit of one block.
func appendMapping(bc *BlockCodec, res *Result, rep BlockReport, blk int, dataLen uint64) {
	k := bc.Code.K
	dataPos := bc.Code.DataPositions()
	base := int64(blk) * int64(bc.BlockDataBits())
	for i := 0; i < bc.Depth; i++ {
		for j := 0; j < k; j++ {
			outBit := base + int64(i*k+j)
			if outBit >= int64(dataLen)*8 {
				return
			}
			res.Mapping = append(res.Mapping, BitSource{
				OutputBit: outBit,
				Block:     blk,
				Word:      i,
				Position:  dataPos[j],
				Fate:      rep.Words[i].Fate,
			})
		}
	}
}

// --- batch stream processing -------------------------------------------
//
// EncodeStream and DecodeStream give a streaming façade over the
// block-oriented codec. Decoding verifies the header and enforces the
// output quota before any output byte is produced; nothing from an
// uncorrectable block is ever emitted.

// EncodeStream reads all of r and writes the container to w.
func EncodeStream(w io.Writer, r io.Reader, m, depth int) error {
	src, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	blob, err := Encode(src, m, depth)
	if err != nil {
		return err
	}
	_, err = w.Write(blob)
	return err
}

// DecodeStream verifies the container from r and, only if every block
// is intact, writes the decoded bytes to w. It returns the full Result
// (including per-block status) in all cases where decoding ran.
func DecodeStream(w io.Writer, r io.Reader, opts *Options) (*Result, error) {
	blob, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	res, err := Decode(blob, opts)
	if res == nil {
		return nil, err
	}
	if len(res.Uncorrectable) == 0 && err == nil {
		if _, werr := w.Write(res.Data); werr != nil {
			return res, werr
		}
	}
	return res, err
}
