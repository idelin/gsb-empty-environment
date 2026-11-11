package h264bs

import (
	"encoding/binary"
	"fmt"
)

// AVCC (MP4/ISOBMFF style) stores NAL units prefixed by their length
// encoded as a big-endian integer. The prefix width (lengthSizeMinusOne
// in avcC) may be 1, 2 or 4 bytes.

// SplitAVCC splits an AVCC stream into NAL units. lengthSize is the
// length-prefix width in bytes and must be 1, 2 or 4. The returned
// slices alias the input buffer.
func SplitAVCC(stream []byte, lengthSize int) ([][]byte, error) {
	if lengthSize != 1 && lengthSize != 2 && lengthSize != 4 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidLengthSize, lengthSize)
	}
	var nals [][]byte
	for pos := 0; pos < len(stream); {
		if pos+lengthSize > len(stream) {
			return nil, fmt.Errorf("h264bs: %w: length prefix", ErrTruncated)
		}
		var n int
		switch lengthSize {
		case 1:
			n = int(stream[pos])
		case 2:
			n = int(binary.BigEndian.Uint16(stream[pos:]))
		case 4:
			n = int(binary.BigEndian.Uint32(stream[pos:]))
		}
		pos += lengthSize
		if n == 0 {
			return nil, ErrEmptyNAL
		}
		if pos+n > len(stream) {
			return nil, fmt.Errorf("h264bs: %w: NAL payload declares %d bytes, %d remain",
				ErrTruncated, n, len(stream)-pos)
		}
		nals = append(nals, stream[pos:pos+n])
		pos += n
	}
	return nals, nil
}

// PackAVCC joins NAL units into an AVCC stream using the given
// length-prefix width (1, 2 or 4 bytes).
func PackAVCC(nals [][]byte, lengthSize int) ([]byte, error) {
	if lengthSize != 1 && lengthSize != 2 && lengthSize != 4 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidLengthSize, lengthSize)
	}
	size := 0
	for _, nal := range nals {
		if len(nal) == 0 {
			return nil, ErrEmptyNAL
		}
		if !fitsPrefix(len(nal), lengthSize) {
			return nil, fmt.Errorf("%w: %d bytes with %d-byte prefix",
				ErrNALTooLong, len(nal), lengthSize)
		}
		size += lengthSize + len(nal)
	}

	out := make([]byte, 0, size)
	prefix := make([]byte, 4)
	for _, nal := range nals {
		switch lengthSize {
		case 1:
			out = append(out, byte(len(nal)))
		case 2:
			binary.BigEndian.PutUint16(prefix, uint16(len(nal)))
			out = append(out, prefix[:2]...)
		case 4:
			binary.BigEndian.PutUint32(prefix, uint32(len(nal)))
			out = append(out, prefix[:4]...)
		}
		out = append(out, nal...)
	}
	return out, nil
}

func fitsPrefix(n, lengthSize int) bool {
	switch lengthSize {
	case 1:
		return n <= 0xff
	case 2:
		return n <= 0xffff
	default:
		return uint64(n) <= 0xffffffff
	}
}
