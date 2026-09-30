package fpcodec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Wire format (self-defined, fixed 10-byte header, then payload):
//
//	[0]   magic 'F'
//	[1]   magic 'P'
//	[2]   version (currently 1)
//	[3]   width: 16, 32 or 64
//	[4]   endianness of the payload: 0 = little, 1 = big
//	[5]   reserved, must be 0
//	[6:10] element count, uint32 big-endian
//
// Payload: count elements, each width/8 bytes, stored in the declared
// endianness. Each element is the raw bit pattern of one float; no host
// floating-point registers or code are ever involved in decoding.
const (
	magic0        = 'F'
	magic1        = 'P'
	formatVersion = 1
	headerSize    = 10

	// MaxElements bounds the declared element count so a corrupt or
	// hostile header can never trigger a huge allocation.
	MaxElements = 1 << 24
)

// Endian selects the payload byte order on the wire.
type Endian byte

const (
	Little Endian = 0
	Big    Endian = 1
)

var (
	ErrBadMagic       = errors.New("fpcodec: bad magic")
	ErrBadVersion     = errors.New("fpcodec: unsupported format version")
	ErrBadEndian      = errors.New("fpcodec: invalid endianness marker")
	ErrReserved       = errors.New("fpcodec: reserved header byte must be zero")
	ErrTooMany        = errors.New("fpcodec: element count exceeds MaxElements")
	ErrLengthMismatch = errors.New("fpcodec: payload length does not match header")
)

type header struct {
	width  Width
	endian Endian
	count  uint32
}

// elemSize returns the wire size of one element.
func elemSize(w Width) (int, error) {
	switch w {
	case Width16:
		return 2, nil
	case Width32:
		return 4, nil
	case Width64:
		return 8, nil
	}
	return 0, ErrUnsupportedWidth
}

// parseHeader validates a raw header. Width and count are checked before
// any allocation happens anywhere.
func parseHeader(h []byte) (header, error) {
	if len(h) < headerSize {
		return header{}, io.ErrUnexpectedEOF
	}
	if h[0] != magic0 || h[1] != magic1 {
		return header{}, ErrBadMagic
	}
	if h[2] != formatVersion {
		return header{}, ErrBadVersion
	}
	w := Width(h[3])
	if _, err := elemSize(w); err != nil {
		return header{}, err
	}
	e := Endian(h[4])
	if e != Little && e != Big {
		return header{}, ErrBadEndian
	}
	if h[5] != 0 {
		return header{}, ErrReserved
	}
	count := binary.BigEndian.Uint32(h[6:10])
	if count > MaxElements {
		return header{}, ErrTooMany
	}
	return header{width: w, endian: e, count: count}, nil
}

func (h header) payloadSize() int64 {
	sz, _ := elemSize(h.width)
	return int64(h.count) * int64(sz)
}

// Marshal encodes vals (raw bit patterns of the given width) into a
// self-contained byte slice.
func Marshal(width Width, endian Endian, vals []uint64) ([]byte, error) {
	if _, err := elemSize(width); err != nil {
		return nil, err
	}
	if endian != Little && endian != Big {
		return nil, ErrBadEndian
	}
	if len(vals) > MaxElements {
		return nil, ErrTooMany
	}
	h := header{width: width, endian: endian, count: uint32(len(vals))}
	out := make([]byte, 0, headerSize+int(h.payloadSize()))
	hdr := make([]byte, headerSize)
	hdr[0], hdr[1], hdr[2] = magic0, magic1, formatVersion
	hdr[3] = byte(width)
	hdr[4] = byte(endian)
	binary.BigEndian.PutUint32(hdr[6:10], uint32(len(vals)))
	out = append(out, hdr...)
	return appendPayload(out, h, vals), nil
}

func appendPayload(out []byte, h header, vals []uint64) []byte {
	var tmp [8]byte
	for _, v := range vals {
		v = maskBits(h.width, v)
		n := int(h.width) / 8
		if h.endian == Little {
			binary.LittleEndian.PutUint64(tmp[:], v)
		} else {
			binary.BigEndian.PutUint64(tmp[:], v)
			out = append(out, tmp[8-n:]...)
			continue
		}
		out = append(out, tmp[:n]...)
	}
	return out
}

// Encode streams the encoding of vals to w.
func Encode(w io.Writer, width Width, endian Endian, vals []uint64) error {
	data, err := Marshal(width, endian, vals)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// Unmarshal decodes a complete in-memory buffer. The declared count is
// validated against the actual buffer length before any allocation, so a
// forged header cannot cause an oversized allocation.
func Unmarshal(data []byte) (Width, []uint64, error) {
	h, err := parseHeader(data)
	if err != nil {
		return 0, nil, err
	}
	if int64(len(data)-headerSize) != h.payloadSize() {
		return 0, nil, fmt.Errorf("%w: want %d payload bytes, got %d",
			ErrLengthMismatch, h.payloadSize(), len(data)-headerSize)
	}
	vals := make([]uint64, h.count)
	decodePayload(h, data[headerSize:], vals)
	return h.width, vals, nil
}

// Decode reads one encoded stream from r. It tolerates arbitrarily chunked
// readers: bytes are accumulated with io.ReadFull regardless of how the
// underlying reader fragments them. The element count is validated before
// the destination slice is allocated.
func Decode(r io.Reader) (Width, []uint64, error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	h, err := parseHeader(hdr[:])
	if err != nil {
		return 0, nil, err
	}
	vals := make([]uint64, h.count)
	buf := make([]byte, h.payloadSize())
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	decodePayload(h, buf, vals)
	return h.width, vals, nil
}

// decodePayload converts raw payload bytes into bit patterns using shifts
// only — no unsafe, no reflection, no host float instructions.
func decodePayload(h header, payload []byte, vals []uint64) {
	n := int(h.width) / 8
	for i := range vals {
		chunk := payload[i*n : (i+1)*n]
		var v uint64
		if h.endian == Little {
			for j := n - 1; j >= 0; j-- {
				v = v<<8 | uint64(chunk[j])
			}
		} else {
			for j := 0; j < n; j++ {
				v = v<<8 | uint64(chunk[j])
			}
		}
		vals[i] = v
	}
}
