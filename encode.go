package wirebuf

// AppendVarint encodes v as a base-128 varint onto dst.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// AppendTag encodes a field key (field number + wire type) onto dst.
func AppendTag(dst []byte, num int, wt WireType) []byte {
	return AppendVarint(dst, uint64(num)<<3|uint64(wt))
}

// AppendZigZag64 encodes a signed integer with the ZigZag transform
// and appends it as a varint (protobuf sint64).
func AppendZigZag64(dst []byte, v int64) []byte {
	return AppendVarint(dst, uint64(v<<1)^uint64(v>>63))
}

// AppendZigZag32 encodes a signed 32-bit integer with ZigZag (sint32).
func AppendZigZag32(dst []byte, v int32) []byte {
	return AppendVarint(dst, uint64(uint32(v<<1)^uint32(v>>31)))
}

// AppendFixed32 appends a 4-byte little-endian value.
func AppendFixed32(dst []byte, v uint32) []byte {
	return append(dst,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// AppendFixed64 appends an 8-byte little-endian value.
func AppendFixed64(dst []byte, v uint64) []byte {
	return append(dst,
		byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// AppendBytes appends a length-delimited payload.
func AppendBytes(dst, payload []byte) []byte {
	dst = AppendVarint(dst, uint64(len(payload)))
	return append(dst, payload...)
}
