package doublesign

import "encoding/binary"

// Canonical binary encoding helpers. All audit hashing and signing uses this
// length-prefixed, big-endian encoding so that a conviction can be re-verified
// offline without any host-specific serialization context.

func appendU64(dst []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(dst, buf[:]...)
}

func appendBytes(dst, data []byte) []byte {
	dst = appendU64(dst, uint64(len(data)))
	return append(dst, data...)
}

func appendString(dst []byte, s string) []byte {
	return appendBytes(dst, []byte(s))
}
