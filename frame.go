package adaptivehuffman

import (
	"encoding/binary"
	"io"
)

// Frame layout constants. See FORMAT.md for the full specification.
const (
	// FrameMagic is the 4-byte magic prefix of every frame ("AHF1").
	FrameMagic uint32 = 0x41484631
	// FrameVersion is the format/state version written into the frame
	// header. Version 1 means: initial state is a single NYT node and the
	// rescale threshold is DefaultMaxWeight.
	FrameVersion uint32 = 1
	// FrameHeaderSize is the frame header length in bytes:
	// magic(4) + version(4) + original length(8).
	FrameHeaderSize = 16
)

// EncodeFrame writes a complete self-contained frame for data to w, using a
// fresh encoder with default parameters. The payload is zero-padded to a
// byte boundary.
func EncodeFrame(w io.Writer, data []byte) error {
	var hdr [FrameHeaderSize]byte
	binary.BigEndian.PutUint32(hdr[0:4], FrameMagic)
	binary.BigEndian.PutUint32(hdr[4:8], FrameVersion)
	binary.BigEndian.PutUint64(hdr[8:16], uint64(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	enc := NewEncoder()
	bw := NewBitWriter(w)
	for _, b := range data {
		if err := enc.EncodeSymbol(bw, b); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// DecodeFrame reads one complete frame from r and returns the decoded
// payload. After the declared number of symbols has been decoded, the
// remaining bits of the final byte must be zero padding and no further bytes
// may follow. Malformed frames are reported as *Error with byte/bit offsets
// relative to the start of the frame.
func DecodeFrame(r io.Reader) ([]byte, error) {
	var hdr [FrameHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, &Error{Kind: ErrKindTruncated, Byte: 0, Bit: 0}
	}
	if binary.BigEndian.Uint32(hdr[0:4]) != FrameMagic {
		return nil, &Error{Kind: ErrKindBadMagic, Byte: 0, Bit: 0}
	}
	if v := binary.BigEndian.Uint32(hdr[4:8]); v != FrameVersion {
		return nil, &Error{Kind: ErrKindBadVersion, Byte: 4, Bit: 0}
	}
	length := binary.BigEndian.Uint64(hdr[8:16])

	br := NewBitReader(r)
	br.base = FrameHeaderSize
	dec := NewDecoder()
	prealloc := length
	if prealloc > 1<<20 {
		prealloc = 1 << 20
	}
	out := make([]byte, 0, prealloc)
	for i := uint64(0); i < length; i++ {
		b, err := dec.DecodeSymbol(br)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}

	// The rest of the current byte, if any, must be zero padding.
	if br.used != 0 {
		for i := br.used; i < 8; i++ {
			if br.cur>>(7-i)&1 != 0 {
				pos := br.base*8 + int64(br.total) + int64(i-br.used)
				return nil, &Error{Kind: ErrKindNonZeroPadding, Byte: pos / 8, Bit: uint8(pos % 8)}
			}
		}
	}
	// Nothing may follow the padding.
	more, err := br.hasMore()
	if err != nil {
		return nil, err
	}
	if more {
		bytePos := br.base + int64((br.total+7)/8)
		return nil, &Error{Kind: ErrKindTrailingData, Byte: bytePos, Bit: 0}
	}
	return out, nil
}
