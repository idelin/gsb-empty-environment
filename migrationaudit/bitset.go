package migrationaudit

type bitSet []uint64

func newBitSet(size int) bitSet {
	return make(bitSet, (size+63)/64)
}

func cloneBitSet(values bitSet) bitSet {
	copied := make(bitSet, len(values))
	copy(copied, values)
	return copied
}

func (values bitSet) set(index int) {
	values[index/64] |= 1 << uint(index%64)
}

func (values bitSet) clear(index int) {
	values[index/64] &^= 1 << uint(index%64)
}

func (values bitSet) contains(index int) bool {
	return values[index/64]&(1<<uint(index%64)) != 0
}

func (values bitSet) count() int {
	total := 0
	for _, block := range values {
		total += countOnes(block)
	}
	return total
}

func countOnes(value uint64) int {
	value = value - ((value >> 1) & 0x5555555555555555)
	value = (value & 0x3333333333333333) + ((value >> 2) & 0x0f0f0f0f0f0f0f0f)
	return int((value * 0x0101010101010101) >> 56)
}

func (values bitSet) union(other bitSet) {
	for index := range values {
		values[index] |= other[index]
	}
}

func (values bitSet) equals(other bitSet) bool {
	if len(values) != len(other) {
		return false
	}
	for index := range values {
		if values[index] != other[index] {
			return false
		}
	}
	return true
}

func encodeBitSet(values bitSet) string {
	if len(values) == 0 {
		return ""
	}
	encoded := make([]byte, 0, len(values)*16)
	for _, block := range values {
		encoded = appendHex(encoded, block)
	}
	return string(encoded)
}

func decodeBitSet(encoded string, size int) (bitSet, bool) {
	expected := ((size + 63) / 64) * 16
	if len(encoded) != expected {
		return nil, false
	}
	values := make(bitSet, (size+63)/64)
	for index := range values {
		block, ok := parseHexBlock(encoded[index*16 : index*16+16])
		if !ok {
			return nil, false
		}
		values[index] = block
	}
	trailingBits := size % 64
	if trailingBits != 0 {
		mask := uint64(1)<<uint(trailingBits) - 1
		if values[len(values)-1]&^mask != 0 {
			return nil, false
		}
	}
	return values, true
}

func appendHex(destination []byte, value uint64) []byte {
	const digits = "0123456789abcdef"
	for shift := 60; shift >= 0; shift -= 4 {
		destination = append(destination, digits[(value>>uint(shift))&0xf])
	}
	return destination
}

func parseHexBlock(encoded string) (uint64, bool) {
	var value uint64
	for _, char := range []byte(encoded) {
		var digit uint64
		switch {
		case char >= '0' && char <= '9':
			digit = uint64(char - '0')
		case char >= 'a' && char <= 'f':
			digit = uint64(char-'a') + 10
		default:
			return 0, false
		}
		value = value<<4 | digit
	}
	return value, true
}
