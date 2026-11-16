package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var errShortWire = errors.New("short SSH wire value")

func appendString(dst, value []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	dst = append(dst, length[:]...)
	return append(dst, value...)
}

func appendUint32(dst []byte, value uint32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	return append(dst, encoded[:]...)
}

func consumeString(src []byte, offset *int) ([]byte, error) {
	if len(src)-*offset < 4 {
		return nil, errShortWire
	}
	length := uint64(binary.BigEndian.Uint32(src[*offset : *offset+4]))
	*offset += 4
	if length > uint64(len(src)-*offset) {
		return nil, fmt.Errorf("%w: string length %d exceeds remaining %d", errShortWire, length, len(src)-*offset)
	}
	value := src[*offset : *offset+int(length)]
	*offset += int(length)
	return value, nil
}

func consumeUint32(src []byte, offset *int) (uint32, error) {
	if len(src)-*offset < 4 {
		return 0, errShortWire
	}
	value := binary.BigEndian.Uint32(src[*offset : *offset+4])
	*offset += 4
	return value, nil
}

func parseStringList(src []byte) ([][]byte, error) {
	const maxItems = 1024

	values := make([][]byte, 0, 4)
	offset := 0
	for offset < len(src) {
		if len(values) == maxItems {
			return nil, fmt.Errorf("too many SSH wire values (maximum %d)", maxItems)
		}
		value, err := consumeString(src, &offset)
		if err != nil {
			return nil, err
		}
		if len(value) == 0 {
			return nil, errors.New("empty SSH wire value")
		}
		values = append(values, value)
	}
	return values, nil
}

func marshalStringList(values [][]byte) []byte {
	var out []byte
	for _, value := range values {
		out = appendString(out, value)
	}
	return out
}
