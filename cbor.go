package netsec

import (
	"encoding/binary"
	"errors"
	"math"
)

// CBOR major types (RFC 8949).
const (
	majorUint   byte = 0
	majorNint   byte = 1
	majorBstr   byte = 2
	majorTstr   byte = 3
	majorArray  byte = 4
	majorMap    byte = 5
	majorTag    byte = 6
	majorSimple byte = 7
)

var (
	errCBOREOF        = errors.New("cbor: unexpected end of input")
	errCBORIndefinite = errors.New("cbor: indefinite-length or reserved additional information")
	errCBORType       = errors.New("cbor: unexpected major type")
	errCBOROverflow   = errors.New("cbor: integer out of range")
	errCBORDepth      = errors.New("cbor: nesting too deep")
)

const maxCBORDepth = 32

// cborDecoder decodes definite-length CBOR only. Indefinite-length items
// (additional information 31) and reserved forms (28-30) are rejected.
type cborDecoder struct {
	data []byte
	off  int
}

func (d *cborDecoder) read(n int) ([]byte, error) {
	if n < 0 || len(d.data)-d.off < n {
		return nil, errCBOREOF
	}
	b := d.data[d.off : d.off+n]
	d.off += n
	return b, nil
}

// readHead reads the initial byte and any extended argument.
func (d *cborDecoder) readHead() (major byte, arg uint64, err error) {
	if d.off >= len(d.data) {
		return 0, 0, errCBOREOF
	}
	ib := d.data[d.off]
	d.off++
	major = ib >> 5
	ai := ib & 0x1f
	switch {
	case ai <= 23:
		arg = uint64(ai)
	case ai == 24:
		b, err := d.read(1)
		if err != nil {
			return 0, 0, err
		}
		arg = uint64(b[0])
	case ai == 25:
		b, err := d.read(2)
		if err != nil {
			return 0, 0, err
		}
		arg = uint64(binary.BigEndian.Uint16(b))
	case ai == 26:
		b, err := d.read(4)
		if err != nil {
			return 0, 0, err
		}
		arg = uint64(binary.BigEndian.Uint32(b))
	case ai == 27:
		b, err := d.read(8)
		if err != nil {
			return 0, 0, err
		}
		arg = binary.BigEndian.Uint64(b)
	default:
		// 28-30 are reserved, 31 is indefinite-length: both rejected.
		return 0, 0, errCBORIndefinite
	}
	return major, arg, nil
}

// skip consumes exactly one well-formed definite-length item and returns its
// raw encoded bytes.
func (d *cborDecoder) skip(depth int) ([]byte, error) {
	if depth > maxCBORDepth {
		return nil, errCBORDepth
	}
	start := d.off
	major, arg, err := d.readHead()
	if err != nil {
		return nil, err
	}
	remaining := uint64(len(d.data) - d.off)
	switch major {
	case majorUint, majorNint, majorSimple:
		// Argument already consumed by readHead.
	case majorBstr, majorTstr:
		if arg > remaining {
			return nil, errCBOREOF
		}
		if _, err := d.read(int(arg)); err != nil {
			return nil, err
		}
	case majorArray:
		if arg > remaining {
			return nil, errCBOREOF
		}
		for i := uint64(0); i < arg; i++ {
			if _, err := d.skip(depth + 1); err != nil {
				return nil, err
			}
		}
	case majorMap:
		if arg > remaining/2 {
			return nil, errCBOREOF
		}
		for i := uint64(0); i < arg; i++ {
			if _, err := d.skip(depth + 1); err != nil {
				return nil, err
			}
			if _, err := d.skip(depth + 1); err != nil {
				return nil, err
			}
		}
	case majorTag:
		if _, err := d.skip(depth + 1); err != nil {
			return nil, err
		}
	}
	return d.data[start:d.off], nil
}

// readInt reads a positive or negative CBOR integer.
func (d *cborDecoder) readInt() (int64, error) {
	major, arg, err := d.readHead()
	if err != nil {
		return 0, err
	}
	switch major {
	case majorUint:
		if arg > math.MaxInt64 {
			return 0, errCBOROverflow
		}
		return int64(arg), nil
	case majorNint:
		if arg > math.MaxInt64 {
			return 0, errCBOROverflow
		}
		return -1 - int64(arg), nil
	}
	return 0, errCBORType
}

// readBstr reads a byte string and returns its content bytes.
func (d *cborDecoder) readBstr() ([]byte, error) {
	major, arg, err := d.readHead()
	if err != nil {
		return nil, err
	}
	if major != majorBstr {
		return nil, errCBORType
	}
	if arg > uint64(len(d.data)-d.off) {
		return nil, errCBOREOF
	}
	return d.read(int(arg))
}

// readTstr reads a text string and returns its content.
func (d *cborDecoder) readTstr() (string, error) {
	major, arg, err := d.readHead()
	if err != nil {
		return "", err
	}
	if major != majorTstr {
		return "", errCBORType
	}
	if arg > uint64(len(d.data)-d.off) {
		return "", errCBOREOF
	}
	b, err := d.read(int(arg))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// cborEncoder produces canonical (shortest-form) definite-length CBOR for
// the structures this package builds itself (Sig_structure, external AAD).
type cborEncoder struct {
	buf []byte
}

func (e *cborEncoder) head(major byte, arg uint64) {
	switch {
	case arg <= 23:
		e.buf = append(e.buf, major<<5|byte(arg))
	case arg <= math.MaxUint8:
		e.buf = append(e.buf, major<<5|24, byte(arg))
	case arg <= math.MaxUint16:
		e.buf = append(e.buf, major<<5|25, byte(arg>>8), byte(arg))
	case arg <= math.MaxUint32:
		e.buf = append(e.buf, major<<5|26,
			byte(arg>>24), byte(arg>>16), byte(arg>>8), byte(arg))
	default:
		e.buf = append(e.buf, major<<5|27,
			byte(arg>>56), byte(arg>>48), byte(arg>>40), byte(arg>>32),
			byte(arg>>24), byte(arg>>16), byte(arg>>8), byte(arg))
	}
}

func (e *cborEncoder) encodeInt(v int64) {
	if v >= 0 {
		e.head(majorUint, uint64(v))
	} else {
		e.head(majorNint, uint64(-1-v))
	}
}

func (e *cborEncoder) bstr(b []byte) {
	e.head(majorBstr, uint64(len(b)))
	e.buf = append(e.buf, b...)
}

func (e *cborEncoder) tstr(s string) {
	e.head(majorTstr, uint64(len(s)))
	e.buf = append(e.buf, s...)
}

func (e *cborEncoder) array(n uint64) {
	e.head(majorArray, n)
}
