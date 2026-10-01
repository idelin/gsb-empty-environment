package wire

import "sync"

// Reader is a resettable, zero-copy cursor over a protobuf wire stream.
// Readers are pooled: obtain one with AcquireReader and return it with
// Release so hot decode paths allocate nothing.
type Reader struct {
	buf []byte
	pos int
}

var readerPool = sync.Pool{New: func() any { return &Reader{} }}

// AcquireReader returns a pooled Reader bound to buf. buf is not copied;
// every read is a zero-copy view into it.
func AcquireReader(buf []byte) *Reader {
	r := readerPool.Get().(*Reader)
	r.buf = buf
	r.pos = 0
	return r
}

// Release returns the Reader to the pool. The Reader must not be used
// afterwards.
func (r *Reader) Release() {
	r.buf = nil
	r.pos = 0
	readerPool.Put(r)
}

// Len reports the number of unread bytes remaining.
func (r *Reader) Len() int { return len(r.buf) - r.pos }

// Pos reports the current read offset, useful for error reporting.
func (r *Reader) Pos() int { return r.pos }

// Done reports whether the stream is fully consumed.
func (r *Reader) Done() bool { return r.pos >= len(r.buf) }

// ReadVarint reads a base-128 varint (7 bits per byte, LSB first).
// It reports ErrTruncated if the stream ends mid-varint and ErrOverflow
// if the varint exceeds 64 bits (more than 10 bytes, or a 10th byte
// carrying more than one payload bit).
func (r *Reader) ReadVarint() (uint64, error) {
	buf := r.buf
	pos := r.pos
	var v uint64
	for i := 0; i < maxVarintBytes; i++ {
		if pos >= len(buf) {
			return 0, ErrTruncated
		}
		b := buf[pos]
		pos++
		if i == maxVarintBytes-1 {
			// 10th byte may carry at most 1 payload bit (bit 63).
			if b > 1 {
				return 0, ErrOverflow
			}
			v |= uint64(b) << 63
			r.pos = pos
			return v, nil
		}
		v |= uint64(b&0x7f) << (7 * uint(i))
		if b < 0x80 {
			r.pos = pos
			return v, nil
		}
	}
	return 0, ErrOverflow
}

// ReadZigZag64 reads a varint and applies zigzag decoding (sint64).
func (r *Reader) ReadZigZag64() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return DecodeZigZag64(v), nil
}

// ReadZigZag32 reads a varint and applies zigzag decoding (sint32).
func (r *Reader) ReadZigZag32() (int32, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return DecodeZigZag32(uint32(v)), nil
}

// ReadFixed32 reads 4 bytes little-endian.
func (r *Reader) ReadFixed32() (uint32, error) {
	if r.Len() < 4 {
		return 0, ErrTruncated
	}
	b := r.buf[r.pos:]
	r.pos += 4
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, nil
}

// ReadFixed64 reads 8 bytes little-endian.
func (r *Reader) ReadFixed64() (uint64, error) {
	if r.Len() < 8 {
		return 0, ErrTruncated
	}
	b := r.buf[r.pos:]
	r.pos += 8
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56, nil
}

// ReadTag reads a field tag and returns the field number and wire type.
func (r *Reader) ReadTag() (fieldNum int, wireType int, err error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	wireType = int(v & 0x7)
	fieldNum = int(v >> 3)
	if fieldNum <= 0 {
		return 0, 0, ErrInvalidTag
	}
	switch wireType {
	case WireVarint, WireFixed64, WireBytes, WireFixed32:
		return fieldNum, wireType, nil
	default:
		// Wire types 3 and 4 (deprecated groups) and 6/7 are rejected.
		return 0, 0, ErrInvalidWireType
	}
}

// ReadBytes reads exactly n bytes as a zero-copy sub-slice of the input.
func (r *Reader) ReadBytes(n int) ([]byte, error) {
	if n < 0 || r.Len() < n {
		return nil, ErrTruncated
	}
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

// ReadLengthDelimited reads a varint length prefix followed by that many
// bytes, returning a zero-copy view of the payload. Used for strings,
// bytes, embedded messages and packed repeated fields.
func (r *Reader) ReadLengthDelimited() ([]byte, error) {
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Len()) {
		return nil, ErrTruncated
	}
	return r.ReadBytes(int(n))
}

// Sub reads a length-delimited field and returns a pooled Reader scoped to
// the embedded message. The caller must Release the sub-reader.
func (r *Reader) Sub() (*Reader, error) {
	b, err := r.ReadLengthDelimited()
	if err != nil {
		return nil, err
	}
	return AcquireReader(b), nil
}

// Skip discards one field of the given wire type, with full bounds checks.
func (r *Reader) Skip(wireType int) error {
	switch wireType {
	case WireVarint:
		_, err := r.ReadVarint()
		return err
	case WireFixed64:
		_, err := r.ReadFixed64()
		return err
	case WireBytes:
		_, err := r.ReadLengthDelimited()
		return err
	case WireFixed32:
		_, err := r.ReadFixed32()
		return err
	default:
		return ErrInvalidWireType
	}
}
