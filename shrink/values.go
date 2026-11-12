package shrink

import "sort"

// integerValues enumerates [min, max] and orders values by signed varint
// length, then numerically so ties are deterministic.
func integerValues(min, max int64) []val {
	values := make([]val, 0, max-min+1)
	for v := min; ; v++ {
		values = append(values, val{integer: true, i: v, cost: signedVarintLen(v)})
		if v == max {
			break
		}
	}
	sort.Slice(values, func(i, j int) bool { return lessVal(values[i], values[j]) })
	return values
}

// stringValues enumerates one representative string for every length in
// [0, maxLen]. Length is the encoded cost, so the result is already ordered.
func stringValues(maxLen int) []val {
	values := make([]val, 0, maxLen+1)
	for n := 0; n <= maxLen; n++ {
		values = append(values, val{s: repeatByte('a', n), cost: n})
	}
	return values
}

func repeatByte(b byte, n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = b
	}
	return string(buf)
}

// signedVarintLen returns the length used by encoding/binary.PutVarint for v.
func signedVarintLen(v int64) int {
	ux := uint64(v) << 1
	if v < 0 {
		ux = ^ux
	}
	n := 1
	for ux >= 0x80 {
		ux >>= 7
		n++
	}
	return n
}
