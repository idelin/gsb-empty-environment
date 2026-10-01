package cidx

import "encoding/binary"

func uvarintLen(x uint64) int {
	n := 1
	for x >= 0x80 {
		x >>= 7
		n++
	}
	return n
}

// recordSize returns the exact encoded size of one record.
func recordSize(key, value []byte) int {
	return uvarintLen(uint64(len(key))) + len(key) +
		uvarintLen(uint64(len(value))) + len(value)
}

func appendRecord(dst, key, value []byte) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], uint64(len(key)))
	dst = append(dst, tmp[:n]...)
	dst = append(dst, key...)
	n = binary.PutUvarint(tmp[:], uint64(len(value)))
	dst = append(dst, tmp[:n]...)
	dst = append(dst, value...)
	return dst
}

// parseRecord decodes one record. The record must consume its extent
// exactly; anything less is structural corruption, not a partial success.
func parseRecord(rec []byte) (key, value []byte, err error) {
	klen, n := binary.Uvarint(rec)
	if n <= 0 {
		return nil, nil, ErrRecordMalformed
	}
	rest := rec[n:]
	if klen > uint64(len(rest)) {
		return nil, nil, ErrRecordMalformed
	}
	key = rest[:int(klen)]
	rest = rest[int(klen):]
	vlen, m := binary.Uvarint(rest)
	if m <= 0 {
		return nil, nil, ErrRecordMalformed
	}
	rest = rest[m:]
	if vlen != uint64(len(rest)) {
		return nil, nil, ErrRecordMalformed
	}
	return key, rest, nil
}
