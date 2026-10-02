package deflate

// RFC 1951 length codes 257..285 (index 0..28 here).
var lengthBase = [29]uint16{
	3, 4, 5, 6, 7, 8, 9, 10,
	11, 13, 15, 17, 19, 23, 27, 31,
	35, 43, 51, 59, 67, 83, 99, 115,
	131, 163, 195, 227, 258,
}

var lengthExtra = [29]uint8{
	0, 0, 0, 0, 0, 0, 0, 0,
	1, 1, 1, 1, 2, 2, 2, 2,
	3, 3, 3, 3, 4, 4, 4, 4,
	5, 5, 5, 5, 0,
}

// RFC 1951 distance codes 0..29.
var distBase = [30]uint16{
	1, 2, 3, 4, 5, 7, 9, 13,
	17, 25, 33, 49, 65, 97, 129, 193,
	257, 385, 513, 769, 1025, 1537, 2049, 3073,
	4097, 6145, 8193, 12289, 16385, 24577,
}

var distExtra = [30]uint8{
	0, 0, 0, 0, 1, 1, 2, 2,
	3, 3, 4, 4, 5, 5, 6, 6,
	7, 7, 8, 8, 9, 9, 10, 10,
	11, 11, 12, 12, 13, 13,
}

// lengthCodeTable maps match length 3..258 to length-code index 0..28.
var lengthCodeTable [259]uint8

func init() {
	for i := 0; i < 29; i++ {
		base := int(lengthBase[i])
		end := base + (1 << uint(lengthExtra[i]))
		for l := base; l < end && l <= 258; l++ {
			lengthCodeTable[l] = uint8(i)
		}
	}
	lengthCodeTable[258] = 28 // 258 must use code 285, not 284
}

// distCodeIndex returns the distance-code index for distance d (1..32768).
func distCodeIndex(d int) int {
	lo, hi := 0, 29
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if int(distBase[mid]) <= d {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// clOrder is the transmission order of code-length code lengths (RFC 1951 3.2.7).
var clOrder = [19]int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}

// fixedLitLenLengths returns the fixed literal/length code lengths (RFC 1951 3.2.6).
func fixedLitLenLengths() []uint8 {
	l := make([]uint8, 288)
	for i := 0; i < 144; i++ {
		l[i] = 8
	}
	for i := 144; i < 256; i++ {
		l[i] = 9
	}
	for i := 256; i < 280; i++ {
		l[i] = 7
	}
	for i := 280; i < 288; i++ {
		l[i] = 8
	}
	return l
}

// fixedDistLengths returns the fixed distance code lengths: 32 codes of 5 bits.
func fixedDistLengths() []uint8 {
	l := make([]uint8, 32)
	for i := range l {
		l[i] = 5
	}
	return l
}
