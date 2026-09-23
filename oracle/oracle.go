// Package oracle is an independent specification parser for the KVS1
// format. It is the test oracle: it shares no code with package format
// (the implementation under test) and re-derives validity purely from
// the written spec, so oracle verdicts never just loop through the
// encoder/decoder being tested.
package oracle

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const (
	magic      = "KVS1"
	maxEntries = 64
	maxKeyLen  = 32
	maxValLen  = 512
	budget     = 4096
)

// Pair is one key/value pair as raw bytes.
type Pair struct {
	Key, Value []byte
}

// cursor walks the buffer with explicit bounds checks.
type cursor struct {
	buf []byte
	pos int
}

func (c *cursor) take(n int) ([]byte, bool) {
	if n < 0 || c.pos+n > len(c.buf) {
		return nil, false
	}
	b := c.buf[c.pos : c.pos+n]
	c.pos += n
	return b, true
}

// parse walks data and returns the violated constraint class ("" if
// valid) plus the decoded pairs when valid.
func parse(data []byte) (string, []Pair) {
	if len(data) > budget {
		return "budget", nil
	}
	c := &cursor{buf: data}
	head, ok := c.take(4)
	if !ok {
		return "truncated", nil
	}
	if string(head) != magic {
		return "magic", nil
	}
	raw, ok := c.take(2)
	if !ok {
		return "truncated", nil
	}
	n := int(binary.LittleEndian.Uint16(raw))
	if n > maxEntries {
		return "count", nil
	}
	pairs := make([]Pair, 0, n)
	for i := 0; i < n; i++ {
		kb, ok := c.take(1)
		if !ok {
			return "truncated", nil
		}
		klen := int(kb[0])
		if klen < 1 || klen > maxKeyLen {
			return "keylen", nil
		}
		key, ok := c.take(klen)
		if !ok {
			return "truncated", nil
		}
		for _, ch := range key {
			if ch < 0x21 || ch > 0x7e || ch == '=' {
				return "keychar", nil
			}
		}
		vb, ok := c.take(2)
		if !ok {
			return "truncated", nil
		}
		vlen := int(binary.LittleEndian.Uint16(vb))
		if vlen > maxValLen {
			return "vallen", nil
		}
		val, ok := c.take(vlen)
		if !ok {
			return "truncated", nil
		}
		pairs = append(pairs, Pair{Key: key, Value: val})
	}
	sum, ok := c.take(4)
	if !ok {
		return "truncated", nil
	}
	if c.pos != len(data) {
		return "trailing", nil
	}
	if binary.LittleEndian.Uint32(sum) != crc32.ChecksumIEEE(data[:c.pos-4]) {
		return "crc", nil
	}
	return "", pairs
}

// Classify returns "" when data is a valid KVS1 document, otherwise
// the class of the first violated constraint.
func Classify(data []byte) string {
	class, _ := parse(data)
	return class
}

// Valid reports whether data is a valid KVS1 document.
func Valid(data []byte) bool { return Classify(data) == "" }

// Parse decodes a valid document or returns an error naming the
// violated constraint class.
func Parse(data []byte) ([]Pair, error) {
	class, pairs := parse(data)
	if class != "" {
		return nil, errors.New("oracle: invalid document, class " + class)
	}
	return pairs, nil
}
