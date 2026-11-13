package netsec

// GNUHash returns the 32-bit GNU hash of name. The bytes are hashed exactly
// as supplied, without Unicode normalization or case folding.
func GNUHash(name string) uint32 {
	return GNUHashBytes([]byte(name))
}

// GNUHashBytes returns the 32-bit GNU hash of b.
//
// The hash starts at 5381 and is updated with h = 33*h + byte for every byte.
// Unsigned 32-bit arithmetic supplies the required modulo-2^32 reduction.
func GNUHashBytes(b []byte) uint32 {
	h := uint32(5381)
	for _, c := range b {
		h = h*33 + uint32(c)
	}
	return h
}

// SysVHash returns the 32-bit System V hash of name. The bytes are hashed
// exactly as supplied, without Unicode normalization or case folding.
func SysVHash(name string) uint32 {
	return SysVHashBytes([]byte(name))
}

// SysVHashBytes returns the 32-bit System V hash of b.
func SysVHashBytes(b []byte) uint32 {
	var h uint32
	for _, c := range b {
		h = (h << 4) + uint32(c)
		g := h & 0xf0000000
		h ^= g >> 24
		h &^= g
	}
	return h
}

// GNUHashString and SysVHashString are explicit-string aliases.
func GNUHashString(name string) uint32 { return GNUHash(name) }

// SysVHashString is an explicit-string alias for SysVHash.
func SysVHashString(name string) uint32 { return SysVHash(name) }
