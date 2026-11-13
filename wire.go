package netsec

import (
	"encoding/binary"
	"fmt"
	"math/big"
)

// wireReader reads the primitive types used by the OpenSSH wire format.
// All strings are length-prefixed byte strings, not Go strings.
type wireReader struct {
	data []byte
	off  int
}

func (r *wireReader) remaining() int {
	return len(r.data) - r.off
}

func (r *wireReader) readUint32() (uint32, error) {
	if r.remaining() < 4 {
		return 0, fmt.Errorf("short wire uint32: %d bytes left", r.remaining())
	}
	v := binary.BigEndian.Uint32(r.data[r.off : r.off+4])
	r.off += 4
	return v, nil
}

func (r *wireReader) readUint64() (uint64, error) {
	if r.remaining() < 8 {
		return 0, fmt.Errorf("short wire uint64: %d bytes left", r.remaining())
	}
	v := binary.BigEndian.Uint64(r.data[r.off : r.off+8])
	r.off += 8
	return v, nil
}

func (r *wireReader) readString() ([]byte, error) {
	n, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	if uint64(n) > uint64(r.remaining()) {
		return nil, fmt.Errorf("wire string length %d exceeds %d remaining bytes", n, r.remaining())
	}
	v := r.data[r.off : r.off+int(n)]
	r.off += int(n)
	return v, nil
}

func (r *wireReader) readMPInt() (*big.Int, error) {
	b, err := r.readString()
	if err != nil {
		return nil, err
	}
	if len(b) > 0 && b[0]&0x80 != 0 {
		return nil, fmt.Errorf("negative mpint in public key")
	}
	return new(big.Int).SetBytes(b), nil
}

func appendUint32(dst []byte, v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return append(dst, b[:]...)
}

func appendUint64(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}

func appendString(dst, value []byte) []byte {
	dst = appendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendMPInt(dst []byte, value *big.Int) []byte {
	if value == nil || value.Sign() == 0 {
		return appendString(dst, nil)
	}
	b := value.Bytes()
	if b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return appendString(dst, b)
}
