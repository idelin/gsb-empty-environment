package webauthn

import "encoding/binary"

// Minimal deterministic CBOR encoder used only by the test suite to build
// well-formed (and deliberately malformed) attestation fixtures.

func cborHead(major byte, value uint64) []byte {
	initial := major << 5
	switch {
	case value < 24:
		return []byte{initial | byte(value)}
	case value <= 0xff:
		return []byte{initial | 24, byte(value)}
	case value <= 0xffff:
		buf := make([]byte, 3)
		buf[0] = initial | 25
		binary.BigEndian.PutUint16(buf[1:], uint16(value))
		return buf
	case value <= 0xffffffff:
		buf := make([]byte, 5)
		buf[0] = initial | 26
		binary.BigEndian.PutUint32(buf[1:], uint32(value))
		return buf
	default:
		buf := make([]byte, 9)
		buf[0] = initial | 27
		binary.BigEndian.PutUint64(buf[1:], value)
		return buf
	}
}

func encInt(value int64) []byte {
	if value >= 0 {
		return cborHead(cborUint, uint64(value))
	}
	return cborHead(cborNint, uint64(-value-1))
}

func encBytes(value []byte) []byte {
	return append(cborHead(cborBytes, uint64(len(value))), value...)
}

func encText(value string) []byte {
	return append(cborHead(cborText, uint64(len(value))), value...)
}

func encBool(value bool) []byte {
	if value {
		return []byte{(cborSimple << 5) | cborTrue}
	}
	return []byte{(cborSimple << 5) | cborFalse}
}

type mapEntry struct {
	key, value []byte
}

func encMap(entries ...mapEntry) []byte {
	out := cborHead(cborMap, uint64(len(entries)))
	for _, entry := range entries {
		out = append(out, entry.key...)
		out = append(out, entry.value...)
	}
	return out
}

func encArray(items ...[]byte) []byte {
	out := cborHead(cborArray, uint64(len(items)))
	for _, item := range items {
		out = append(out, item...)
	}
	return out
}

func intKey(value int64) []byte   { return encInt(value) }
func textKey(value string) []byte { return encText(value) }
