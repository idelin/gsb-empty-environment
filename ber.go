package netsec

import (
	"fmt"
	"io"
)

const (
	berTagInteger    byte = 0x02
	berTagBoolean    byte = 0x01
	berTagOctet      byte = 0x04
	berTagEnumerated byte = 0x0a
	berTagSequence   byte = 0x30
	berTagSet        byte = 0x31
)

func berLength(n int) []byte {
	if n < 0 {
		panic("negative BER length")
	}
	if n < 128 {
		return []byte{byte(n)}
	}
	var tmp [4]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte(n)
		n >>= 8
	}
	out := []byte{byte(0x80 | (len(tmp) - i))}
	return append(out, tmp[i:]...)
}

func berElement(tag byte, content []byte) []byte {
	out := make([]byte, 0, 1+len(berLength(len(content)))+len(content))
	out = append(out, tag)
	out = append(out, berLength(len(content))...)
	out = append(out, content...)
	return out
}

func berSequence(children ...[]byte) []byte {
	var content []byte
	for _, child := range children {
		content = append(content, child...)
	}
	return berElement(berTagSequence, content)
}

func berSet(children ...[]byte) []byte {
	var content []byte
	for _, child := range children {
		content = append(content, child...)
	}
	return berElement(berTagSet, content)
}

func berIntegerValue(v int) []byte {
	if v == 0 {
		return []byte{0}
	}
	var tmp [8]byte
	i := len(tmp)
	for v > 0 {
		i--
		tmp[i] = byte(v)
		v >>= 8
	}
	if tmp[i]&0x80 != 0 {
		i--
		tmp[i] = 0
	}
	return tmp[i:]
}

func berInteger(v int) []byte {
	return berElement(berTagInteger, berIntegerValue(v))
}

func berEnumerated(v int) []byte {
	return berElement(berTagEnumerated, berIntegerValue(v))
}

func berOctetString(s string) []byte {
	return berElement(berTagOctet, []byte(s))
}

func berOctetValue(b []byte) []byte {
	return berElement(berTagOctet, b)
}

func berBoolean(v bool) []byte {
	if v {
		return berElement(berTagBoolean, []byte{0xff})
	}
	return berElement(berTagBoolean, []byte{0})
}

// readBERElement reads one definite-length BER element from r.
func readBERElement(r io.Reader) (tag byte, content []byte, err error) {
	var tagBuf [1]byte
	if _, err = io.ReadFull(r, tagBuf[:]); err != nil {
		return 0, nil, err
	}
	tag = tagBuf[0]

	var first [1]byte
	if _, err = io.ReadFull(r, first[:]); err != nil {
		return 0, nil, err
	}
	length := int(first[0])
	if first[0]&0x80 != 0 {
		count := int(first[0] & 0x7f)
		if count == 0 {
			return 0, nil, fmt.Errorf("indefinite BER lengths are not supported")
		}
		if count > 4 {
			return 0, nil, fmt.Errorf("BER length is too large")
		}
		var lengthBytes [4]byte
		if _, err = io.ReadFull(r, lengthBytes[:count]); err != nil {
			return 0, nil, err
		}
		length = 0
		for _, b := range lengthBytes[:count] {
			length = (length << 8) | int(b)
		}
	}
	if length < 0 || length > 16<<20 {
		return 0, nil, fmt.Errorf("invalid BER length %d", length)
	}
	content = make([]byte, length)
	if _, err = io.ReadFull(r, content); err != nil {
		return 0, nil, err
	}
	return tag, content, nil
}

// readBERElementBytes parses one element from a byte slice and returns the
// unconsumed suffix.
func readBERElementBytes(data []byte) (tag byte, content, rest []byte, err error) {
	if len(data) < 2 {
		return 0, nil, nil, fmt.Errorf("truncated BER element")
	}
	tag = data[0]
	length := int(data[1])
	offset := 2
	if data[1]&0x80 != 0 {
		count := int(data[1] & 0x7f)
		if count == 0 {
			return 0, nil, nil, fmt.Errorf("indefinite BER lengths are not supported")
		}
		if count > 4 || len(data) < 2+count {
			return 0, nil, nil, fmt.Errorf("invalid BER length")
		}
		length = 0
		for _, b := range data[2 : 2+count] {
			length = (length << 8) | int(b)
		}
		offset += count
	}
	if length < 0 || len(data) < offset+length {
		return 0, nil, nil, fmt.Errorf("truncated BER content")
	}
	return tag, data[offset : offset+length], data[offset+length:], nil
}

func parseBERInt(content []byte) (int, error) {
	if len(content) == 0 || len(content) > 8 {
		return 0, fmt.Errorf("invalid BER integer")
	}
	v := 0
	for _, b := range content {
		v = (v << 8) | int(b)
	}
	return v, nil
}
