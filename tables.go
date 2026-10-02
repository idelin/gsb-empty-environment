package deflate

// RFC 1951 length codes 257..285 (index 0..28).
var lengthBase = [29]int{
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
var distBase = [30]int{
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

// Order in which code-length code lengths are transmitted.
var clCodeOrder = [19]uint8{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}

// lengthCodeOf maps a match length 3..258 to a length code index 0..28.
// Length 258 is always encoded as code 285 (index 28), never as 284+extra.
var lengthCodeOf [259]uint8

func init() {
	c := 0
	for l := 3; l <= 258; l++ {
		for c < 28 && lengthBase[c+1] <= l {
			c++
		}
		lengthCodeOf[l] = uint8(c)
	}
	lengthCodeOf[258] = 28
}

// distCodeOf maps a distance 1..32768 to a distance code index 0..29.
func distCodeOf(d int) int {
	lo, hi := 0, 29
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if distBase[mid] <= d {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

const (
	maxLitCodes  = 286
	maxDistCodes = 30
	endOfBlock   = 256
)

// Fixed (static) Huffman tables from RFC 1951 section 3.2.6.
var staticLitLengths = func() []uint8 {
	l := make([]uint8, maxLitCodes)
	for i := 0; i <= 143; i++ {
		l[i] = 8
	}
	for i := 144; i <= 255; i++ {
		l[i] = 9
	}
	for i := 256; i <= 279; i++ {
		l[i] = 7
	}
	for i := 280; i <= 285; i++ {
		l[i] = 8
	}
	return l
}()

var staticDistLengths = func() []uint8 {
	l := make([]uint8, maxDistCodes)
	for i := range l {
		l[i] = 5
	}
	return l
}()

var staticLitCodes = canonicalCodes(staticLitLengths)
var staticDistCodes = canonicalCodes(staticDistLengths)
