package netsec

import (
	"encoding/binary"
	"errors"
)

// cborWriter is a minimal canonical (deterministic) CBOR encoder covering
// only the types needed for mdoc reader authentication structures.
type cborWriter struct {
	buf []byte
}

func (w *cborWriter) head(major byte, val uint64) {
	switch {
	case val < 24:
		w.buf = append(w.buf, major<<5|byte(val))
	case val < 1<<8:
		w.buf = append(w.buf, major<<5|24, byte(val))
	case val < 1<<16:
		w.buf = append(w.buf, major<<5|25, 0, 0)
		binary.BigEndian.PutUint16(w.buf[len(w.buf)-2:], uint16(val))
	case val < 1<<32:
		w.buf = append(w.buf, major<<5|26, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(w.buf[len(w.buf)-4:], uint32(val))
	default:
		w.buf = append(w.buf, major<<5|27, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(w.buf[len(w.buf)-8:], val)
	}
}

func (w *cborWriter) bytes(b []byte) {
	w.head(2, uint64(len(b)))
	w.buf = append(w.buf, b...)
}

func (w *cborWriter) text(s string) {
	w.head(3, uint64(len(s)))
	w.buf = append(w.buf, s...)
}

func (w *cborWriter) array(n int) {
	w.head(4, uint64(n))
}

func (w *cborWriter) mapHead(n int) {
	w.head(5, uint64(n))
}

// tag writes a CBOR semantic tag (e.g. 24 for embedded CBOR data item).
func (w *cborWriter) tag(t uint64) {
	w.head(6, t)
}

// int64Val writes a signed integer (major type 0 or 1).
func (w *cborWriter) int64Val(v int64) {
	if v >= 0 {
		w.head(0, uint64(v))
	} else {
		w.head(1, uint64(-1-v))
	}
}

// taggedBytes encodes #6.24(bstr) - a tagged, encoded CBOR data item,
// used for DeviceEngagementBytes, EReaderKeyBytes and ItemsRequestBytes.
func taggedBytes(inner []byte) []byte {
	w := &cborWriter{}
	w.tag(24)
	w.bytes(inner)
	return w.buf
}

var errCBOR = errors.New("netsec: malformed CBOR")

// cborReader is a minimal CBOR decoder used to parse COSE_Sign1 structures.
type cborReader struct {
	data []byte
	off  int
}

func (r *cborReader) head() (major byte, val uint64, err error) {
	if r.off >= len(r.data) {
		return 0, 0, errCBOR
	}
	b := r.data[r.off]
	r.off++
	major, info := b>>5, b&0x1f
	switch {
	case info < 24:
		val = uint64(info)
	case info == 24:
		if r.off+1 > len(r.data) {
			return 0, 0, errCBOR
		}
		val = uint64(r.data[r.off])
		r.off++
	case info == 25:
		if r.off+2 > len(r.data) {
			return 0, 0, errCBOR
		}
		val = uint64(r.data[r.off])<<8 | uint64(r.data[r.off+1])
		r.off += 2
	case info == 26:
		if r.off+4 > len(r.data) {
			return 0, 0, errCBOR
		}
		val = uint64(binary.BigEndian.Uint32(r.data[r.off:]))
		r.off += 4
	case info == 27:
		if r.off+8 > len(r.data) {
			return 0, 0, errCBOR
		}
		val = binary.BigEndian.Uint64(r.data[r.off:])
		r.off += 8
	default:
		return 0, 0, errCBOR
	}
	return major, val, nil
}

func (r *cborReader) bytes() ([]byte, error) {
	major, n, err := r.head()
	if err != nil || major != 2 {
		return nil, errCBOR
	}
	if r.off+int(n) > len(r.data) {
		return nil, errCBOR
	}
	b := r.data[r.off : r.off+int(n)]
	r.off += int(n)
	return b, nil
}

func (r *cborReader) array() (int, error) {
	major, n, err := r.head()
	if err != nil || major != 4 {
		return 0, errCBOR
	}
	return int(n), nil
}

// skip skips one data item, recursing into arrays and maps.
func (r *cborReader) skip() error {
	major, n, err := r.head()
	if err != nil {
		return err
	}
	switch major {
	case 0, 1, 7:
		return nil
	case 2, 3:
		if r.off+int(n) > len(r.data) {
			return errCBOR
		}
		r.off += int(n)
		return nil
	case 4:
		for i := 0; i < int(n); i++ {
			if err := r.skip(); err != nil {
				return err
			}
		}
		return nil
	case 5:
		for i := 0; i < 2*int(n); i++ {
			if err := r.skip(); err != nil {
				return err
			}
		}
		return nil
	case 6:
		return r.skip()
	}
	return errCBOR
}
