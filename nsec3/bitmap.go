package nsec3

import "sort"

// Type bitmap field layout (RFC 4034 section 4.1.2): a sequence of
// (window block number: uint8, bitmap length: uint8, bitmap: 1..32 bytes)
// triples. Bit (bitmap[o] >> (7-b)) of window w represents type
// w*256 + o*8 + b.

// ParseTypeBitmap parses an RFC 4034 NSEC/NSEC3 type bitmap into the set
// of DNS type numbers present.
func ParseTypeBitmap(bitmap []byte) (map[uint16]bool, error) {
	types := make(map[uint16]bool)
	i := 0
	for i < len(bitmap) {
		if i+2 > len(bitmap) {
			return nil, ErrInvalidRecord
		}
		window := bitmap[i]
		length := int(bitmap[i+1])
		i += 2
		if length == 0 || length > 32 || i+length > len(bitmap) {
			return nil, ErrInvalidRecord
		}
		block := bitmap[i : i+length]
		i += length
		for octet, b := range block {
			for bit := 0; bit < 8; bit++ {
				if b&(1<<(7-bit)) != 0 {
					t := uint16(window)*256 + uint16(octet*8+bit)
					types[t] = true
				}
			}
		}
	}
	return types, nil
}

// EncodeTypeBitmap renders a set of DNS type numbers into the RFC 4034
// window block wire encoding. Empty windows (no types in a window) are
// omitted, as required.
func EncodeTypeBitmap(types []uint16) []byte {
	if len(types) == 0 {
		return nil
	}
	unique := make(map[uint16]bool, len(types))
	for _, t := range types {
		unique[t] = true
	}
	windows := make(map[uint8][]byte)
	for t := range unique {
		window := uint8(t / 256)
		octet := (t % 256) / 8
		bit := t % 8
		block := windows[window]
		if len(block) <= int(octet) {
			grown := make([]byte, octet+1)
			copy(grown, block)
			block = grown
			windows[window] = block
		}
		block[octet] |= 1 << (7 - bit)
	}
	order := make([]int, 0, len(windows))
	for w := range windows {
		order = append(order, int(w))
	}
	sort.Ints(order)
	out := make([]byte, 0, len(order)*2)
	for _, w := range order {
		block := windows[uint8(w)]
		out = append(out, uint8(w), byte(len(block)))
		out = append(out, block...)
	}
	return out
}
