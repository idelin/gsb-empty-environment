package netsec

import "crypto/elliptic"

const (
	tpmGeneratedMagic uint16 = 0xFF54
	tpmStAttestQuote  uint16 = 0x8018

	// objectAttrSign is TPMA_OBJECT.sign (bit 16); an AK must carry it.
	objectAttrSign uint32 = 1 << 16
)

var eccCurves = map[AlgorithmID]elliptic.Curve{
	0x0003: elliptic.P256(),
	0x0004: elliptic.P384(),
	0x0005: elliptic.P521(),
}

type byteReader struct {
	buf []byte
	pos int
}

func newByteReader(buf []byte) *byteReader {
	return &byteReader{buf: buf}
}

func (r *byteReader) remaining() int {
	return len(r.buf) - r.pos
}

func (r *byteReader) read(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.buf) {
		return nil, ErrMalformedInput
	}
	out := r.buf[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

func (r *byteReader) readU8() (byte, error) {
	b, err := r.read(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *byteReader) readU16() (uint16, error) {
	b, err := r.read(2)
	if err != nil {
		return 0, err
	}
	return uint16(b[0])<<8 | uint16(b[1]), nil
}

func (r *byteReader) readU32() (uint32, error) {
	b, err := r.read(4)
	if err != nil {
		return 0, err
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]), nil
}

func (r *byteReader) readSized16() ([]byte, error) {
	n, err := r.readU16()
	if err != nil {
		return nil, err
	}
	return r.read(int(n))
}
