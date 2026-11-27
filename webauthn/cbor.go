package webauthn

import (
	"encoding/binary"
	"fmt"
)

// This file implements a small, deterministic subset of CBOR (RFC 8949)
// sufficient to parse WebAuthn attestation objects and COSE public keys.
//
// Supported major types:
//
//	0,1 - unsigned / negative integers
//	2,3 - byte strings / text strings
//	4,5 - arrays / maps (definite length only)
//	7   - simple values limited to false (20) and true (21)
//
// Rejected explicitly: indefinite-length encodings, floating point numbers,
// tags, non-minimal integer encodings and non-canonically ordered map keys.

const (
	cborUint      = 0
	cborNint      = 1
	cborBytes     = 2
	cborText      = 3
	cborArray     = 4
	cborMap       = 5
	cborSimple    = 7
	cborFalse     = 20
	cborTrue      = 21
	cborIndefMask = 0x1f
)

// cborNode is one decoded CBOR value. Exactly one of the scalar fields is set
// unless the node is a composite.
type cborNode struct {
	major   byte
	uintVal uint64
	nintVal int64
	bytes   []byte
	text    string
	boolVal bool
	array   []*cborNode
	entries []cborMapEntry
}

type cborMapEntry struct {
	key, value *cborNode
}

type cborDecoder struct {
	data []byte
	pos  int
}

// decodeCBOR parses the complete byte slice: trailing bytes are an error.
func decodeCBOR(data []byte) (*cborNode, error) {
	dec := &cborDecoder{data: data}
	node, err := dec.readNode()
	if err != nil {
		return nil, err
	}
	if dec.pos != len(data) {
		return nil, fmt.Errorf("%w: trailing %d bytes after CBOR value", ErrMalformed, len(data)-dec.pos)
	}
	return node, nil
}

func (d *cborDecoder) readNode() (*cborNode, error) {
	if d.pos >= len(d.data) {
		return nil, fmt.Errorf("%w: unexpected end of CBOR data", ErrMalformed)
	}
	initial := d.data[d.pos]
	d.pos++
	major := initial >> 5
	info := initial & 0x1f

	switch major {
	case cborArray, cborMap:
		if info == cborIndefMask {
			return nil, fmt.Errorf("%w: indefinite-length containers are not supported", ErrMalformed)
		}
	default:
		if info == cborIndefMask {
			return nil, fmt.Errorf("%w: indefinite-length encoding is not supported", ErrMalformed)
		}
	}

	argument, err := d.readArgument(info)
	if err != nil {
		return nil, err
	}

	node := &cborNode{major: major}
	switch major {
	case cborUint:
		node.uintVal = argument
	case cborNint:
		// -(argument + 1); guard against int64 overflow.
		if argument > uint64(1<<63) {
			return nil, fmt.Errorf("%w: negative integer out of range", ErrMalformed)
		}
		node.nintVal = -int64(argument) - 1
	case cborBytes, cborText:
		if argument > uint64(len(d.data)-d.pos) {
			return nil, fmt.Errorf("%w: string length exceeds remaining data", ErrMalformed)
		}
		chunk := d.data[d.pos : d.pos+int(argument)]
		d.pos += int(argument)
		if major == cborBytes {
			node.bytes = append([]byte(nil), chunk...)
		} else {
			node.text = string(chunk)
		}
	case cborArray:
		node.array = make([]*cborNode, argument)
		for i := range node.array {
			node.array[i], err = d.readNode()
			if err != nil {
				return nil, err
			}
		}
	case cborMap:
		node.entries = make([]cborMapEntry, argument)
		var previousKey []byte
		for i := range node.entries {
			keyStart := d.pos
			key, err := d.readNode()
			if err != nil {
				return nil, err
			}
			keyBytes := d.data[keyStart:d.pos]
			if previousKey != nil && !canonicalKeyLess(previousKey, keyBytes) {
				return nil, fmt.Errorf("%w: map keys are not in canonical order", ErrMalformed)
			}
			value, err := d.readNode()
			if err != nil {
				return nil, err
			}
			node.entries[i] = cborMapEntry{key: key, value: value}
			previousKey = keyBytes
		}
	case cborSimple:
		// info 25/26/27 encode half/single/double precision floats, which are
		// intentionally outside the supported deterministic subset.
		if info >= 25 && info <= 27 {
			return nil, fmt.Errorf("%w: floating point values are not supported", ErrMalformed)
		}
		switch argument {
		case cborFalse:
			node.boolVal = false
		case cborTrue:
			node.boolVal = true
		default:
			return nil, fmt.Errorf("%w: unsupported simple value %d", ErrMalformed, argument)
		}
	default:
		return nil, fmt.Errorf("%w: unsupported CBOR major type %d", ErrMalformed, major)
	}
	return node, nil
}

// readArgument decodes the "argument" of an initial byte and enforces the
// minimal-encoding rule of canonical CBOR.
func (d *cborDecoder) readArgument(info byte) (uint64, error) {
	switch {
	case info < 24:
		return uint64(info), nil
	case info == 24:
		if d.pos >= len(d.data) {
			return 0, fmt.Errorf("%w: truncated one-byte argument", ErrMalformed)
		}
		value := d.data[d.pos]
		d.pos++
		if value < 24 {
			return 0, fmt.Errorf("%w: non-minimal integer encoding", ErrMalformed)
		}
		return uint64(value), nil
	case info == 25:
		if d.pos+2 > len(d.data) {
			return 0, fmt.Errorf("%w: truncated two-byte argument", ErrMalformed)
		}
		value := binary.BigEndian.Uint16(d.data[d.pos:])
		d.pos += 2
		if value <= 0xff {
			return 0, fmt.Errorf("%w: non-minimal integer encoding", ErrMalformed)
		}
		return uint64(value), nil
	case info == 26:
		if d.pos+4 > len(d.data) {
			return 0, fmt.Errorf("%w: truncated four-byte argument", ErrMalformed)
		}
		value := binary.BigEndian.Uint32(d.data[d.pos:])
		d.pos += 4
		if value <= 0xffff {
			return 0, fmt.Errorf("%w: non-minimal integer encoding", ErrMalformed)
		}
		return uint64(value), nil
	case info == 27:
		if d.pos+8 > len(d.data) {
			return 0, fmt.Errorf("%w: truncated eight-byte argument", ErrMalformed)
		}
		value := binary.BigEndian.Uint64(d.data[d.pos:])
		d.pos += 8
		if value <= 0xffffffff {
			return 0, fmt.Errorf("%w: non-minimal integer encoding", ErrMalformed)
		}
		return value, nil
	default:
		// 28-30 are reserved; 31 is the break / indefinite marker handled by
		// the caller, floats (25/26/27 with major 7) never reach here.
		return 0, fmt.Errorf("%w: unsupported additional info %d", ErrMalformed, info)
	}
}

// canonicalKeyLess implements the canonical CBOR key ordering of RFC 8949
// section 4.2.1: shorter encoded key first, otherwise lexicographic order of
// the canonical bytes.
func canonicalKeyLess(a, b []byte) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false // equal keys => duplicate
}

// Map accessors.

func (n *cborNode) mapIndex(key int64) *cborNode {
	if n == nil || n.major != cborMap {
		return nil
	}
	for i := range n.entries {
		k := n.entries[i].key
		if k.major == cborUint && int64(k.uintVal) == key {
			return n.entries[i].value
		}
		if k.major == cborNint && k.nintVal == key {
			return n.entries[i].value
		}
	}
	return nil
}

func (n *cborNode) mapIndexText(key string) *cborNode {
	if n == nil || n.major != cborMap {
		return nil
	}
	for i := range n.entries {
		k := n.entries[i].key
		if k.major == cborText && k.text == key {
			return n.entries[i].value
		}
	}
	return nil
}

func (n *cborNode) asInt64() (int64, bool) {
	switch {
	case n == nil:
		return 0, false
	case n.major == cborUint:
		if n.uintVal > uint64(1<<63-1) {
			return 0, false
		}
		return int64(n.uintVal), true
	case n.major == cborNint:
		return n.nintVal, true
	default:
		return 0, false
	}
}

func (n *cborNode) asBytes() ([]byte, bool) {
	if n == nil || n.major != cborBytes {
		return nil, false
	}
	return n.bytes, true
}

func (n *cborNode) asText() (string, bool) {
	if n == nil || n.major != cborText {
		return "", false
	}
	return n.text, true
}

func (n *cborNode) asBool() (bool, bool) {
	if n == nil || n.major != cborSimple {
		return false, false
	}
	return n.boolVal, true
}

// errCBORType builds a consistent malformed error for type mismatches.
func errCBORType(field string, want string, node *cborNode) error {
	major := -1
	if node != nil {
		major = int(node.major)
	}
	return fmt.Errorf("%w: field %q expects %s (got major type %d)", ErrMalformed, field, want, major)
}
