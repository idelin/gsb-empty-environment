package h2h1

import "errors"

func appendHPACKInt(out []byte, value uint64, prefix uint8, highBits byte) []byte {
	mask := uint64(1<<prefix) - 1
	if value < mask {
		return append(out, highBits|byte(value))
	}
	out = append(out, highBits|byte(mask))
	value -= mask
	for value >= 128 {
		out = append(out, byte(value&0x7f)|0x80)
		value >>= 7
	}
	return append(out, byte(value))
}

func appendHPACKString(out []byte, value string, huffman bool) []byte {
	raw := []byte(value)
	if huffman {
		raw = HuffmanEncode(raw)
	}
	high := byte(0)
	if huffman {
		high = 0x80
	}
	out = appendHPACKInt(out, uint64(len(raw)), 7, high)
	return append(out, raw...)
}

// EncodeLiteralHeader encodes one literal header field. A false indexing value
// uses the "without indexing" representation; sensitive selects "never
// indexed". Header names are always emitted as literal strings, so malformed
// names can be deliberately generated for adversarial tests.
func EncodeLiteralHeader(name, value string, indexing, sensitive bool) []byte {
	var out []byte
	switch {
	case sensitive:
		out = appendHPACKInt(out, 0, 4, 0x10)
	case indexing:
		out = appendHPACKInt(out, 0, 6, 0x40)
	default:
		out = appendHPACKInt(out, 0, 4, 0x00)
	}
	out = appendHPACKString(out, name, false)
	out = appendHPACKString(out, value, false)
	return out
}

// EncodeIndexedHeader builds an indexed HPACK representation.
func EncodeIndexedHeader(index uint64) ([]byte, error) {
	if index == 0 {
		return nil, errors.New("h2h1: HPACK index 0 is invalid")
	}
	return appendHPACKInt(nil, index, 7, 0x80), nil
}

// EncodeTableSizeUpdate builds a dynamic-table-size update representation.
func EncodeTableSizeUpdate(size uint32) []byte {
	return appendHPACKInt(nil, uint64(size), 5, 0x20)
}

// EncodeHPACKString encodes one HPACK string, optionally with Huffman
// compression.
func EncodeHPACKString(value string, huffman bool) []byte {
	return appendHPACKString(nil, value, huffman)
}

// EncodeHeaderBlock encodes a complete block using literal-without-indexing
// representations. It is deterministic, legal HPACK, and preserves arbitrary
// bytes for adversarial tests.
func EncodeHeaderBlock(fields []HeaderField) []byte {
	var out []byte
	for _, field := range fields {
		indexing := false
		out = append(out, EncodeLiteralHeader(field.Name, field.Value, indexing, field.Sensitive)...)
	}
	return out
}

// EncodeHeaderBlockHuffman is EncodeHeaderBlock with Huffman-coded name and
// value strings.
func EncodeHeaderBlockHuffman(fields []HeaderField) []byte {
	var out []byte
	for _, field := range fields {
		out = appendHPACKInt(out, 0, 4, 0x00)
		out = appendHPACKString(out, field.Name, true)
		out = appendHPACKString(out, field.Value, true)
	}
	return out
}
